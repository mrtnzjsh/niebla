package grpc

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log"
	"strings"

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

	fetchTree := func(treeHash []byte) ([]tree.Entry, error) {
		return s.db.GetTreeEntries(ctx, treeHash)
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

	var rebased []int64

	for _, child := range children {
		mineLabel := fmt.Sprintf("CHILD CL %d", child.CLID)
		oldChildTree := child.RootTreeHash

		mergedHash, conflicts, mergeErr := tree.ThreeWayTreeMerge(
			ctx,
			oldParentTree,
			newParentTree,
			oldChildTree,
			fetchTree,
			readBlob,
			writeBlob,
			writeTree,
			theirsLabel,
			mineLabel,
		)

		if mergeErr != nil || len(conflicts) > 0 {
			var sb strings.Builder
			for _, c := range conflicts {
				if c.ConflictHunks > 0 {
					sb.WriteString(fmt.Sprintf("%s (%d conflict hunks); ", c.Path, c.ConflictHunks))
				} else {
					sb.WriteString(fmt.Sprintf("%s (%s); ", c.Path, c.Reason))
				}
			}
			summary := strings.TrimSuffix(sb.String(), "; ")

			log.Printf("[CascadeRebase] CL %d has rebase conflict with parent %d: %s", child.CLID, parentCLID, summary)
			_ = s.db.MarkCLRebaseConflict(ctx, child.CLID, summary)
			_ = s.db.RecordRebaseEvent(ctx, child.CLID, parentCLID, child.BasePatchsetNumber, 0, "conflict", summary)
			// Halt recursion down this branch until author resolves the conflict
			continue
		}

		// Clean merge: create automated patchset
		nextPS, err := s.db.GetNextPatchsetNumber(ctx, child.CLID)
		if err != nil {
			log.Printf("[CascadeRebase] failed to get next patchset number for CL %d: %v", child.CLID, err)
			continue
		}

		if err := s.db.CreateAutoRebasePatchset(ctx, child.CLID, nextPS, mergedHash); err != nil {
			log.Printf("[CascadeRebase] failed to record auto-rebase patchset for CL %d: %v", child.CLID, err)
			continue
		}

		if err := s.db.UpdateCLRootTreeHash(ctx, child.CLID, mergedHash); err != nil {
			log.Printf("[CascadeRebase] failed to update root tree hash for CL %d: %v", child.CLID, err)
			continue
		}

		_ = s.db.ClearCLRebaseConflict(ctx, child.CLID)
		_ = s.db.RecordRebaseEvent(ctx, child.CLID, parentCLID, child.BasePatchsetNumber, nextPS, "clean", "Auto-rebased successfully")

		rebased = append(rebased, child.CLID)
		log.Printf("[CascadeRebase] Successfully auto-rebased CL %d (Patchset %d) onto parent %d", child.CLID, nextPS, parentCLID)

		// Recurse down to child's children
		downstream, err := s.CascadeStackRebase(ctx, child.CLID, oldChildTree, mergedHash)
		if err == nil && len(downstream) > 0 {
			rebased = append(rebased, downstream...)
		}
	}

	return rebased, nil
}
