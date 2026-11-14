package db

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"niebla/internal/db/store"
	"niebla/pkg/tree"
)

// TrunkCommitAdvisoryLockID is a 32-bit application-scoped PostgreSQL advisory lock key ("NIEB")
// used to serialize trunk commits and linear trunk_revision sequence increments across distributed servers.
const TrunkCommitAdvisoryLockID int64 = 0x4e494542

// TrunkMergeFunc is a callback invoked during SubmitCL when the changelist's parent lags behind trunk head.
// It performs a 3-way merge between the changelist root tree, the head trunk tree, and their common ancestor.
// Returns the merged root tree hash, or an error if there are unresolved merge conflicts.
type TrunkMergeFunc func(ctx context.Context, cl, headCL *Changelist) ([]byte, error)

// BlobRecord represents an entry in the blobs table.
type BlobRecord struct {
	SHA256      []byte
	ByteSize    int64
	StoragePath string
	CreatedAt   time.Time
}

// Changelist represents an entry in the changelists table.
type Changelist struct {
	CLID                  int64
	Author                string
	Description           string
	RootTreeHash          []byte
	ParentCL              *int64
	Status                string
	TrunkRevision         *int64
	CommittedAt           time.Time
	NeedsRebase           bool
	RebaseConflictDetails *string
	BasePatchsetNumber    int
}

// Workspace represents an entry in the workspaces table.
type Workspace struct {
	ID        string
	Name      string
	Owner     string
	BaseCL    *int64
	CreatedAt time.Time
}

// InsertBlob records a newly stored CAS blob into the database.
func (d *DB) InsertBlob(ctx context.Context, sha256 []byte, byteSize int64, storagePath string) error {
	err := d.queries.InsertBlob(ctx, store.InsertBlobParams{
		Sha256:      sha256,
		ByteSize:    byteSize,
		StoragePath: storagePath,
	})
	if err != nil {
		return fmt.Errorf("failed to insert blob: %w", err)
	}
	return nil
}

// GetBlob retrieves metadata for a CAS blob by its SHA-256 hash.
func (d *DB) GetBlob(ctx context.Context, sha256 []byte) (*BlobRecord, error) {
	b, err := d.queries.GetBlob(ctx, sha256)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("failed to query blob: %w", err)
	}
	record := toBlobRecord(b)
	return &record, nil
}

// GetTreeEntries retrieves all direct child entries for a given tree hash.
func (d *DB) GetTreeEntries(ctx context.Context, treeHash []byte) ([]tree.Entry, error) {
	manifest, err := d.queries.GetTreeManifest(ctx, treeHash)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return []tree.Entry{}, nil
		}
		return nil, fmt.Errorf("failed to query tree manifest: %w", err)
	}

	entries, err := tree.UnmarshalManifest(manifest)
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal tree manifest: %w", err)
	}
	return entries, nil
}

// GetNodeEntry retrieves a specific child node by name within a parent tree.
func (d *DB) GetNodeEntry(ctx context.Context, parentTreeHash []byte, name string) (*tree.Entry, error) {
	entries, err := d.GetTreeEntries(ctx, parentTreeHash)
	if err != nil {
		return nil, fmt.Errorf("failed to query tree node %q: %w", name, err)
	}
	if entry, ok := tree.FindEntry(entries, name); ok {
		return entry, nil
	}
	return nil, ErrNotFound
}

// InsertTreeNodes persists multiple tree node entries under a tree hash as a compact manifest.
func (d *DB) InsertTreeNodes(ctx context.Context, treeHash []byte, entries []tree.Entry) error {
	if len(entries) == 0 {
		return nil
	}

	manifest, err := tree.MarshalManifest(entries)
	if err != nil {
		return fmt.Errorf("failed to marshal tree manifest: %w", err)
	}

	err = d.queries.InsertTree(ctx, store.InsertTreeParams{
		TreeHash:   treeHash,
		Manifest:   manifest,
		EntryCount: int32(len(entries)),
	})
	if err != nil {
		return fmt.Errorf("failed to insert tree manifest: %w", err)
	}
	return nil
}

// GetHeadCL retrieves the latest committed (status = 'submitted') changelist ordered by sequential trunk_revision.
func (d *DB) GetHeadCL(ctx context.Context) (*Changelist, error) {
	c, err := d.queries.GetHeadCL(ctx)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("failed to query head CL: %w", err)
	}
	cl := toChangelist(c)
	return &cl, nil
}

// GetCL retrieves a changelist by ID.
func (d *DB) GetCL(ctx context.Context, clID int64) (*Changelist, error) {
	c, err := d.queries.GetCL(ctx, clID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("failed to query CL %d: %w", clID, err)
	}
	cl := toChangelist(c)
	return &cl, nil
}

// CreateShelvedCL creates a new changelist in 'shelved' status.
func (d *DB) CreateShelvedCL(ctx context.Context, author, description string, rootTreeHash []byte, parentCL int64) (int64, error) {
	var parentVal pgtype.Int8
	if parentCL > 0 {
		parentVal = pgtype.Int8{Int64: parentCL, Valid: true}
	}

	clID, err := d.queries.CreateShelvedCL(ctx, store.CreateShelvedCLParams{
		Author:       author,
		Description:  description,
		RootTreeHash: rootTreeHash,
		ParentCl:     parentVal,
	})
	if err != nil {
		return 0, fmt.Errorf("failed to create shelved CL: %w", err)
	}
	return clID, nil
}

// SubmitCL atomically updates a changelist status to 'submitted' in a serialized transaction,
// acquiring a PostgreSQL transaction-scoped advisory lock, executing an optional TrunkMergeFunc
// when the changelist's parent lags behind the current trunk head, and allocating a monotonic trunk_revision.
func (d *DB) SubmitCL(ctx context.Context, clID int64, mergeFns ...TrunkMergeFunc) (*Changelist, error) {
	tx, err := d.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to start submit transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	txQueries := d.queries.WithTx(tx)

	// Acquire exclusive transaction-scoped trunk commit lock so concurrent distributed submits serialize cleanly.
	if err := txQueries.AcquireTrunkCommitLock(ctx, TrunkCommitAdvisoryLockID); err != nil {
		return nil, fmt.Errorf("failed to acquire trunk serialization lock: %w", err)
	}

	rawCL, err := txQueries.GetCLForUpdate(ctx, clID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("failed to query CL for update: %w", err)
	}
	cl := toChangelist(rawCL)

	if cl.Status == "submitted" {
		// Already submitted
		_ = tx.Commit(ctx)
		return &cl, nil
	}

	rawHead, headErr := txQueries.GetHeadCL(ctx)

	finalTreeHash := cl.RootTreeHash
	var newParentCL *int64 = cl.ParentCL

	if headErr == nil {
		headCL := toChangelist(rawHead)
		// If a TrunkMergeFunc is provided and headCL differs from cl's parent, run 3-way merge
		if len(mergeFns) > 0 && mergeFns[0] != nil {
			mergedHash, mErr := mergeFns[0](ctx, &cl, &headCL)
			if mErr != nil {
				return nil, mErr
			}
			if len(mergedHash) > 0 {
				finalTreeHash = mergedHash
			}
		}
		headID := headCL.CLID
		newParentCL = &headID
	} else if !errors.Is(headErr, pgx.ErrNoRows) {
		return nil, fmt.Errorf("failed to query head CL during submit: %w", headErr)
	}

	var parentVal pgtype.Int8
	if newParentCL != nil {
		parentVal = pgtype.Int8{Int64: *newParentCL, Valid: true}
	}

	res, err := txQueries.SubmitCLUpdate(ctx, store.SubmitCLUpdateParams{
		ClID:         clID,
		RootTreeHash: finalTreeHash,
		ParentCl:     parentVal,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to update CL status: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("failed to commit submit transaction: %w", err)
	}

	trunkRev := res.TrunkRevision.Int64
	committedAt := res.CommittedAt.Time

	cl.Status = "submitted"
	cl.TrunkRevision = &trunkRev
	cl.RootTreeHash = finalTreeHash
	cl.ParentCL = newParentCL
	cl.CommittedAt = committedAt
	cl.NeedsRebase = false
	cl.RebaseConflictDetails = nil
	return &cl, nil
}

// CreateWorkspace provisions a new workspace entry pointing to baseCL or head.
func (d *DB) CreateWorkspace(ctx context.Context, name, owner string, baseCL int64) (*Workspace, []byte, error) {
	var targetCLID int64
	var rootTreeHash []byte

	if baseCL <= 0 {
		headRow, err := d.queries.GetHeadTreeHash(ctx)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to determine head CL for workspace: %w", err)
		}
		targetCLID = headRow.ClID
		rootTreeHash = headRow.RootTreeHash
	} else {
		targetCL, err := d.GetCL(ctx, baseCL)
		if err != nil {
			return nil, nil, fmt.Errorf("specified base CL %d not found: %w", baseCL, err)
		}
		targetCLID = targetCL.CLID
		rootTreeHash = targetCL.RootTreeHash
	}

	w, err := d.queries.CreateWorkspace(ctx, store.CreateWorkspaceParams{
		Name:   name,
		Owner:  owner,
		BaseCl: pgtype.Int8{Int64: targetCLID, Valid: true},
	})
	if err != nil {
		return nil, nil, fmt.Errorf("failed to insert workspace: %w", err)
	}

	ws := toWorkspace(w)
	return &ws, rootTreeHash, nil
}

// GetWorkspace fetches a workspace by its UUID string.
func (d *DB) GetWorkspace(ctx context.Context, id string) (*Workspace, error) {
	var wsUUID pgtype.UUID
	if err := wsUUID.Scan(id); err != nil {
		return nil, fmt.Errorf("invalid workspace uuid %q: %w", id, err)
	}

	w, err := d.queries.GetWorkspace(ctx, wsUUID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("failed to query workspace %q: %w", id, err)
	}
	ws := toWorkspace(w)
	return &ws, nil
}

// GetShelvedChildren retrieves all changelists in 'shelved' status that declare parentCL as their parent.
func (d *DB) GetShelvedChildren(ctx context.Context, parentCL int64) ([]Changelist, error) {
	rows, err := d.queries.GetShelvedChildren(ctx, pgtype.Int8{Int64: parentCL, Valid: true})
	if err != nil {
		return nil, fmt.Errorf("failed to query shelved children for CL %d: %w", parentCL, err)
	}

	children := make([]Changelist, len(rows))
	for i, r := range rows {
		children[i] = toChangelist(r)
	}
	return children, nil
}

// UpdateCLRootTreeHash updates the Merkle root tree hash of a changelist.
func (d *DB) UpdateCLRootTreeHash(ctx context.Context, clID int64, newRootTreeHash []byte) error {
	err := d.queries.UpdateCLRootTreeHash(ctx, store.UpdateCLRootTreeHashParams{
		ClID:         clID,
		RootTreeHash: newRootTreeHash,
	})
	if err != nil {
		return fmt.Errorf("failed to update root tree hash for CL %d: %w", clID, err)
	}
	return nil
}

// UpdateCLBasePatchset updates the base patchset number of a changelist.
func (d *DB) UpdateCLBasePatchset(ctx context.Context, clID int64, basePatchsetNumber int) error {
	err := d.queries.UpdateCLBasePatchset(ctx, store.UpdateCLBasePatchsetParams{
		ClID:               clID,
		BasePatchsetNumber: int32(basePatchsetNumber),
	})
	if err != nil {
		return fmt.Errorf("failed to update base patchset number for CL %d: %w", clID, err)
	}
	return nil
}

// MarkCLRebaseConflict flags a changelist as requiring rebase resolution due to a merge conflict.
func (d *DB) MarkCLRebaseConflict(ctx context.Context, clID int64, conflictDetails string) error {
	err := d.queries.MarkCLRebaseConflict(ctx, store.MarkCLRebaseConflictParams{
		ClID:                  clID,
		RebaseConflictDetails: pgtype.Text{String: conflictDetails, Valid: true},
	})
	if err != nil {
		return fmt.Errorf("failed to mark rebase conflict for CL %d: %w", clID, err)
	}
	return nil
}

// ClearCLRebaseConflict clears the rebase conflict state of a changelist.
func (d *DB) ClearCLRebaseConflict(ctx context.Context, clID int64) error {
	if err := d.queries.ClearCLRebaseConflict(ctx, clID); err != nil {
		return fmt.Errorf("failed to clear rebase conflict for CL %d: %w", clID, err)
	}
	return nil
}

// RecordRebaseEvent inserts an audit entry into cl_rebase_events.
func (d *DB) RecordRebaseEvent(ctx context.Context, clID, parentCL int64, triggerPS, resultingPS int, status, summary string) error {
	var resultVal pgtype.Int4
	if resultingPS > 0 {
		resultVal = pgtype.Int4{Int32: int32(resultingPS), Valid: true}
	}
	var conflictVal pgtype.Text
	if summary != "" {
		conflictVal = pgtype.Text{String: summary, Valid: true}
	}

	err := d.queries.RecordRebaseEvent(ctx, store.RecordRebaseEventParams{
		ClID:                    clID,
		ParentCl:                parentCL,
		TriggerPatchsetNumber:   int32(triggerPS),
		ResultingPatchsetNumber: resultVal,
		Status:                  status,
		ConflictSummary:         conflictVal,
	})
	if err != nil {
		return fmt.Errorf("failed to record rebase event for CL %d: %w", clID, err)
	}
	return nil
}

// InvalidateApprovals marks all existing approvals on a changelist as stale when a new patchset is uploaded.
func (d *DB) InvalidateApprovals(ctx context.Context, clID int64) error {
	_, err := d.pool.Exec(ctx, "UPDATE cl_approvals SET stale = true WHERE cl_id = $1", clID)
	return err
}
