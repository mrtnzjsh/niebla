package grpc

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log"
	"strings"

	"niebla/internal/db"
	pkgevents "niebla/pkg/events"
	"niebla/pkg/tree"
)

// CascadeStackRebase traverses all active shelved changelists dependent on parentCLID
// and executes recursive 3-way Merkle tree merges to re-parent them onto newParentTree.
// Returns a slice of successfully rebased child CL IDs.
func (s *Server) CascadeStackRebase(
	ctx context.Context,
	parentCLID int64,
	oldParentTree, newParentTree []byte,
) ([]int64, error) {
	if bytes.Equal(oldParentTree, newParentTree) {
		return nil, nil
	}

	children, err := s.db.GetShelvedChildren(ctx, parentCLID)
	if err != nil {
		return nil, fmt.Errorf("failed to retrieve shelved children for CL %d: %w", parentCLID, err)
	}
	if len(children) == 0 {
		return nil, nil
	}

	var rebased []int64

	for _, child := range children {
		oldChildTree := child.RootTreeHash

		mergedHash, nextPS, conflicts, mergeErr := s.rebaseChild(ctx, parentCLID, oldParentTree, newParentTree, child)
		if mergeErr != nil || len(conflicts) > 0 {
			// Halt recursion down this branch until author resolves the conflict
			continue
		}

		rebased = append(rebased, child.CLID)
		log.Printf("[CascadeRebase] Successfully auto-rebased CL %d (Patchset %d) onto parent %d", child.CLID, nextPS, parentCLID)

		// Recurse down to child's children under branch lock
		var unlock func()
		if s.rebaseQueue != nil {
			unlock = s.rebaseQueue.locks.Lock(child.CLID)
		}
		downstream, err := s.CascadeStackRebase(ctx, child.CLID, oldChildTree, mergedHash)
		if unlock != nil {
			unlock()
		}
		if err == nil && len(downstream) > 0 {
			rebased = append(rebased, downstream...)
		}
	}

	return rebased, nil
}

// rebaseChild executes a 3-way Merkle tree merge for a single child CL against updated parent trees.
func (s *Server) rebaseChild(
	ctx context.Context,
	parentCLID int64,
	oldParentTree, newParentTree []byte,
	child db.Changelist,
) ([]byte, int, []tree.Conflict, error) {
	fetchTree := func(treeHash []byte) ([]tree.Entry, error) {
		return s.getTreeEntries(ctx, treeHash)
	}

	readBlob := func(sha []byte) ([]byte, error) {
		f, _, err := s.casStorage.OpenBlob(sha)
		if err != nil {
			return nil, err
		}
		defer f.Close()
		return io.ReadAll(f)
	}

	writeBlob := func(data []byte) ([]byte, error) {
		hashBytes, _, size, storagePath, err := s.casStorage.Put(bytes.NewReader(data))
		if err != nil {
			return nil, err
		}
		_ = s.db.InsertBlob(ctx, hashBytes, size, storagePath)
		return hashBytes, nil
	}

	writeTree := func(entries []tree.Entry) ([]byte, error) {
		treeHash := tree.ComputeTreeHash(entries)
		if err := s.db.InsertTreeNodes(ctx, treeHash, entries); err != nil {
			return nil, err
		}
		return treeHash, nil
	}

	theirsLabel := fmt.Sprintf("PARENT CL %d", parentCLID)
	mineLabel := fmt.Sprintf("CHILD CL %d", child.CLID)

	mergedHash, conflicts, mergeErr := tree.ThreeWayTreeMerge(
		ctx,
		oldParentTree,
		newParentTree,
		child.RootTreeHash,
		fetchTree,
		readBlob,
		writeBlob,
		writeTree,
		theirsLabel,
		mineLabel,
	)

	if mergeErr != nil || len(conflicts) > 0 {
		var sb strings.Builder
		for _, conflict := range conflicts {
			if conflict.ConflictHunks > 0 {
				sb.WriteString(fmt.Sprintf("%s (%d conflict hunks); ", conflict.Path, conflict.ConflictHunks))
			} else {
				sb.WriteString(fmt.Sprintf("%s (%s); ", conflict.Path, conflict.Reason))
			}
		}
		summary := strings.TrimSuffix(sb.String(), "; ")

		log.Printf("[CascadeRebase] CL %d has rebase conflict with parent %d: %s", child.CLID, parentCLID, summary)
		_ = s.db.MarkCLRebaseConflict(ctx, child.CLID, summary)
		_ = s.db.RecordRebaseEvent(ctx, child.CLID, parentCLID, child.BasePatchsetNumber, 0, "conflict", summary)

		if s.emitter != nil {
			conflictedPaths := make([]string, len(conflicts))
			for i, conflict := range conflicts {
				conflictedPaths[i] = conflict.Path
			}
			s.emitter.Emit(ctx, pkgevents.New(
				pkgevents.TypeCLRebaseConflict,
				"niebla-server",
				fmt.Sprintf("cl/%d", child.CLID),
				pkgevents.CLRebaseConflictPayload{
					CLID:            fmt.Sprintf("%d", child.CLID),
					ParentCLID:      fmt.Sprintf("%d", parentCLID),
					ConflictedFiles: conflictedPaths,
					Summary:         summary,
				},
			))
		}

		return nil, 0, conflicts, mergeErr
	}

	// Clean merge: create automated patchset
	nextPS, err := s.db.GetNextPatchsetNumber(ctx, child.CLID)
	if err != nil {
		log.Printf("[CascadeRebase] failed to get next patchset number for CL %d: %v", child.CLID, err)
		return nil, 0, nil, err
	}

	if err := s.db.CreateAutoRebasePatchset(ctx, child.CLID, nextPS, mergedHash); err != nil {
		log.Printf("[CascadeRebase] failed to record auto-rebase patchset for CL %d: %v", child.CLID, err)
		return nil, 0, nil, err
	}

	if err := s.db.UpdateCLRootTreeHash(ctx, child.CLID, mergedHash); err != nil {
		log.Printf("[CascadeRebase] failed to update root tree hash for CL %d: %v", child.CLID, err)
		return nil, 0, nil, err
	}

	if parentPSNum, err := s.db.GetNextPatchsetNumber(ctx, parentCLID); err == nil && parentPSNum > 1 {
		_ = s.db.UpdateCLBasePatchset(ctx, child.CLID, parentPSNum-1)
	}

	_ = s.db.ClearCLRebaseConflict(ctx, child.CLID)
	_ = s.db.RecordRebaseEvent(ctx, child.CLID, parentCLID, child.BasePatchsetNumber, nextPS, "clean", "Auto-rebased successfully")

	if s.emitter != nil {
		s.emitter.Emit(ctx, pkgevents.New(
			pkgevents.TypeCLRebased,
			"niebla-server",
			fmt.Sprintf("cl/%d", child.CLID),
			pkgevents.CLRebasedPayload{
				CLID:            fmt.Sprintf("%d", child.CLID),
				ParentCLID:      fmt.Sprintf("%d", parentCLID),
				NewRootTreeHash: hex.EncodeToString(mergedHash),
				PatchsetNumber:  nextPS,
			},
		))
	}

	return mergedHash, nextPS, nil, nil
}

// SyncThrough ensures a child CL is immediately re-parented onto its parent's latest root tree.
// If a background rebase is in flight or pending, it flushes the rebase on-demand before returning.
func (s *Server) SyncThrough(ctx context.Context, childCLID int64) error {
	child, err := s.db.GetCL(ctx, childCLID)
	if err != nil || child == nil || child.ParentCL == nil || *child.ParentCL <= 0 {
		return nil
	}
	parentCLID := *child.ParentCL

	// Recursively sync through ancestors first so the entire branch is up-to-date
	if err := s.SyncThrough(ctx, parentCLID); err != nil && !errors.Is(err, tree.ErrMergeConflict) {
		return err
	}

	if s.rebaseQueue != nil {
		unlock := s.rebaseQueue.locks.Lock(parentCLID)
		defer unlock()
	}

	// Re-fetch under lock to ensure any in-flight background workers finished
	child, err = s.db.GetCL(ctx, childCLID)
	if err != nil || child == nil {
		return err
	}
	parent, err := s.db.GetCL(ctx, parentCLID)
	if err != nil || parent == nil {
		return err
	}

	// If child already has an unresolved rebase conflict, no need to attempt rebase again
	if child.NeedsRebase {
		return nil
	}

	oldParentTree := resolveChildOldParentTree(ctx, s.db, child, parent, parentCLID)
	newParentTree := parent.RootTreeHash
	if bytes.Equal(oldParentTree, newParentTree) {
		return nil
	}

	_, _, _, err = s.rebaseChild(ctx, parentCLID, oldParentTree, newParentTree, *child)
	if errors.Is(err, tree.ErrMergeConflict) {
		return nil
	}
	return err
}
