package db

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"niebla/pkg/tree"
)

// ErrNotFound is returned when a requested record is not found in the database.
var ErrNotFound = errors.New("record not found")

// TrunkCommitAdvisoryLockID is the PostgreSQL transaction-scoped advisory lock key ('NIEB')
// used to serialize concurrent trunk submissions.
const TrunkCommitAdvisoryLockID int64 = 0x4e494542

// TrunkMergeFunc computes the 3-way merged Merkle root tree hash when a changelist's
// parent lags behind the current trunk head CL during submission.
type TrunkMergeFunc func(ctx context.Context, cl *Changelist, headCL *Changelist) (mergedRootTreeHash []byte, err error)

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
	query := `
		INSERT INTO blobs (sha256, byte_size, storage_path)
		VALUES ($1, $2, $3)
		ON CONFLICT (sha256) DO NOTHING;
	`
	_, err := d.pool.Exec(ctx, query, sha256, byteSize, storagePath)
	if err != nil {
		return fmt.Errorf("failed to insert blob: %w", err)
	}
	return nil
}

// GetBlob retrieves metadata for a CAS blob by its SHA-256 hash.
func (d *DB) GetBlob(ctx context.Context, sha256 []byte) (*BlobRecord, error) {
	query := `
		SELECT sha256, byte_size, storage_path, created_at
		FROM blobs
		WHERE sha256 = $1;
	`
	var b BlobRecord
	err := d.pool.QueryRow(ctx, query, sha256).Scan(
		&b.SHA256,
		&b.ByteSize,
		&b.StoragePath,
		&b.CreatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("failed to query blob: %w", err)
	}
	return &b, nil
}

// GetTreeEntries retrieves all direct child entries for a given tree hash.
func (d *DB) GetTreeEntries(ctx context.Context, treeHash []byte) ([]tree.Entry, error) {
	query := `
		SELECT manifest
		FROM trees
		WHERE tree_hash = $1;
	`
	var manifest []byte
	err := d.pool.QueryRow(ctx, query, treeHash).Scan(&manifest)
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

	query := `
		INSERT INTO trees (tree_hash, manifest, entry_count)
		VALUES ($1, $2, $3)
		ON CONFLICT (tree_hash) DO NOTHING;
	`
	if _, err := d.pool.Exec(ctx, query, treeHash, manifest, len(entries)); err != nil {
		return fmt.Errorf("failed to insert tree manifest: %w", err)
	}
	return nil
}

// GetHeadCL retrieves the latest committed (status = 'submitted') changelist ordered by sequential trunk_revision.
func (d *DB) GetHeadCL(ctx context.Context) (*Changelist, error) {
	query := `
		SELECT cl_id, author, description, root_tree_hash, parent_cl, status, trunk_revision, committed_at,
		       needs_rebase, rebase_conflict_details, base_patchset_number
		FROM changelists
		WHERE status = 'submitted'
		ORDER BY trunk_revision DESC NULLS LAST, cl_id DESC
		LIMIT 1;
	`
	var cl Changelist
	err := d.pool.QueryRow(ctx, query).Scan(
		&cl.CLID,
		&cl.Author,
		&cl.Description,
		&cl.RootTreeHash,
		&cl.ParentCL,
		&cl.Status,
		&cl.TrunkRevision,
		&cl.CommittedAt,
		&cl.NeedsRebase,
		&cl.RebaseConflictDetails,
		&cl.BasePatchsetNumber,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("failed to query head CL: %w", err)
	}
	return &cl, nil
}

// GetCL retrieves a changelist by ID.
func (d *DB) GetCL(ctx context.Context, clID int64) (*Changelist, error) {
	query := `
		SELECT cl_id, author, description, root_tree_hash, parent_cl, status, trunk_revision, committed_at,
		       needs_rebase, rebase_conflict_details, base_patchset_number
		FROM changelists
		WHERE cl_id = $1;
	`
	var cl Changelist
	err := d.pool.QueryRow(ctx, query, clID).Scan(
		&cl.CLID,
		&cl.Author,
		&cl.Description,
		&cl.RootTreeHash,
		&cl.ParentCL,
		&cl.Status,
		&cl.TrunkRevision,
		&cl.CommittedAt,
		&cl.NeedsRebase,
		&cl.RebaseConflictDetails,
		&cl.BasePatchsetNumber,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("failed to query CL %d: %w", clID, err)
	}
	return &cl, nil
}

// CreateShelvedCL creates a new changelist in 'shelved' status.
func (d *DB) CreateShelvedCL(ctx context.Context, author, description string, rootTreeHash []byte, parentCL int64) (int64, error) {
	var parentVal *int64
	if parentCL > 0 {
		parentVal = &parentCL
	}

	query := `
		INSERT INTO changelists (author, description, root_tree_hash, parent_cl, status, committed_at)
		VALUES ($1, $2, $3, $4, 'shelved', now())
		RETURNING cl_id;
	`
	var clID int64
	err := d.pool.QueryRow(ctx, query, author, description, rootTreeHash, parentVal).Scan(&clID)
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

	// 1. Acquire exclusive transaction-scoped trunk commit lock so concurrent distributed submits serialize cleanly
	if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock($1)", TrunkCommitAdvisoryLockID); err != nil {
		return nil, fmt.Errorf("failed to acquire trunk serialization lock: %w", err)
	}

	// 2. Lock the target CL row for update
	selectQuery := `
		SELECT cl_id, author, description, root_tree_hash, parent_cl, status, trunk_revision, committed_at,
		       needs_rebase, rebase_conflict_details, base_patchset_number
		FROM changelists
		WHERE cl_id = $1
		FOR UPDATE;
	`
	var cl Changelist
	err = tx.QueryRow(ctx, selectQuery, clID).Scan(
		&cl.CLID,
		&cl.Author,
		&cl.Description,
		&cl.RootTreeHash,
		&cl.ParentCL,
		&cl.Status,
		&cl.TrunkRevision,
		&cl.CommittedAt,
		&cl.NeedsRebase,
		&cl.RebaseConflictDetails,
		&cl.BasePatchsetNumber,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("failed to query CL for update: %w", err)
	}

	if cl.Status == "submitted" {
		// Already submitted
		_ = tx.Commit(ctx)
		return &cl, nil
	}

	// 3. Query current trunk head inside the serialized lock
	headQuery := `
		SELECT cl_id, author, description, root_tree_hash, parent_cl, status, trunk_revision, committed_at,
		       needs_rebase, rebase_conflict_details, base_patchset_number
		FROM changelists
		WHERE status = 'submitted'
		ORDER BY trunk_revision DESC NULLS LAST, cl_id DESC
		LIMIT 1;
	`
	var headCL Changelist
	headErr := tx.QueryRow(ctx, headQuery).Scan(
		&headCL.CLID,
		&headCL.Author,
		&headCL.Description,
		&headCL.RootTreeHash,
		&headCL.ParentCL,
		&headCL.Status,
		&headCL.TrunkRevision,
		&headCL.CommittedAt,
		&headCL.NeedsRebase,
		&headCL.RebaseConflictDetails,
		&headCL.BasePatchsetNumber,
	)

	finalTreeHash := cl.RootTreeHash
	var newParentCL *int64 = cl.ParentCL

	if headErr == nil {
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

	updateQuery := `
		UPDATE changelists
		SET status = 'submitted',
		    trunk_revision = nextval('trunk_revision_seq'),
		    root_tree_hash = $2,
		    parent_cl = $3,
		    committed_at = now(),
		    needs_rebase = false,
		    rebase_conflict_details = NULL
		WHERE cl_id = $1
		RETURNING trunk_revision, committed_at;
	`
	var trunkRev int64
	var committedAt time.Time
	err = tx.QueryRow(ctx, updateQuery, clID, finalTreeHash, newParentCL).Scan(&trunkRev, &committedAt)
	if err != nil {
		return nil, fmt.Errorf("failed to update CL status: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("failed to commit submit transaction: %w", err)
	}

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
	var targetCL *Changelist
	var err error

	if baseCL <= 0 {
		targetCL, err = d.GetHeadCL(ctx)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to determine head CL for workspace: %w", err)
		}
	} else {
		targetCL, err = d.GetCL(ctx, baseCL)
		if err != nil {
			return nil, nil, fmt.Errorf("specified base CL %d not found: %w", baseCL, err)
		}
	}

	query := `
		INSERT INTO workspaces (name, owner, base_cl)
		VALUES ($1, $2, $3)
		RETURNING id, name, owner, base_cl, created_at;
	`
	var ws Workspace
	err = d.pool.QueryRow(ctx, query, name, owner, targetCL.CLID).Scan(
		&ws.ID,
		&ws.Name,
		&ws.Owner,
		&ws.BaseCL,
		&ws.CreatedAt,
	)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to insert workspace: %w", err)
	}

	return &ws, targetCL.RootTreeHash, nil
}

// GetWorkspace fetches a workspace by its UUID string.
func (d *DB) GetWorkspace(ctx context.Context, id string) (*Workspace, error) {
	query := `
		SELECT id, name, owner, base_cl, created_at
		FROM workspaces
		WHERE id = $1;
	`
	var ws Workspace
	err := d.pool.QueryRow(ctx, query, id).Scan(
		&ws.ID,
		&ws.Name,
		&ws.Owner,
		&ws.BaseCL,
		&ws.CreatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("failed to query workspace %q: %w", id, err)
	}
	return &ws, nil
}

// GetShelvedChildren retrieves all changelists in 'shelved' status that declare parentCL as their parent.
func (d *DB) GetShelvedChildren(ctx context.Context, parentCL int64) ([]Changelist, error) {
	query := `
		SELECT cl_id, author, description, root_tree_hash, parent_cl, status, trunk_revision, committed_at,
		       needs_rebase, rebase_conflict_details, base_patchset_number
		FROM changelists
		WHERE parent_cl = $1 AND status = 'shelved'
		ORDER BY cl_id ASC;
	`
	rows, err := d.pool.Query(ctx, query, parentCL)
	if err != nil {
		return nil, fmt.Errorf("failed to query shelved children for CL %d: %w", parentCL, err)
	}
	defer rows.Close()

	var children []Changelist
	for rows.Next() {
		var cl Changelist
		if err := rows.Scan(
			&cl.CLID,
			&cl.Author,
			&cl.Description,
			&cl.RootTreeHash,
			&cl.ParentCL,
			&cl.Status,
			&cl.TrunkRevision,
			&cl.CommittedAt,
			&cl.NeedsRebase,
			&cl.RebaseConflictDetails,
			&cl.BasePatchsetNumber,
		); err != nil {
			return nil, fmt.Errorf("failed to scan child changelist: %w", err)
		}
		children = append(children, cl)
	}
	return children, rows.Err()
}

// UpdateCLRootTreeHash updates the Merkle root tree hash of a changelist.
func (d *DB) UpdateCLRootTreeHash(ctx context.Context, clID int64, newRootTreeHash []byte) error {
	query := `
		UPDATE changelists
		SET root_tree_hash = $2
		WHERE cl_id = $1;
	`
	_, err := d.pool.Exec(ctx, query, clID, newRootTreeHash)
	if err != nil {
		return fmt.Errorf("failed to update root tree hash for CL %d: %w", clID, err)
	}
	return nil
}

// UpdateCLBasePatchset updates the base patchset number of a changelist.
func (d *DB) UpdateCLBasePatchset(ctx context.Context, clID int64, basePatchsetNumber int) error {
	query := `
		UPDATE changelists
		SET base_patchset_number = $2
		WHERE cl_id = $1;
	`
	_, err := d.pool.Exec(ctx, query, clID, basePatchsetNumber)
	if err != nil {
		return fmt.Errorf("failed to update base patchset number for CL %d: %w", clID, err)
	}
	return nil
}

// MarkCLRebaseConflict flags a changelist as requiring rebase resolution due to a merge conflict.
func (d *DB) MarkCLRebaseConflict(ctx context.Context, clID int64, conflictDetails string) error {
	query := `
		UPDATE changelists
		SET needs_rebase = true, rebase_conflict_details = $2
		WHERE cl_id = $1;
	`
	_, err := d.pool.Exec(ctx, query, clID, conflictDetails)
	if err != nil {
		return fmt.Errorf("failed to mark rebase conflict for CL %d: %w", clID, err)
	}
	return nil
}

// ClearCLRebaseConflict clears the rebase conflict state of a changelist.
func (d *DB) ClearCLRebaseConflict(ctx context.Context, clID int64) error {
	query := `
		UPDATE changelists
		SET needs_rebase = false, rebase_conflict_details = NULL
		WHERE cl_id = $1;
	`
	_, err := d.pool.Exec(ctx, query, clID)
	if err != nil {
		return fmt.Errorf("failed to clear rebase conflict for CL %d: %w", clID, err)
	}
	return nil
}

// RecordRebaseEvent inserts an audit entry into cl_rebase_events.
func (d *DB) RecordRebaseEvent(ctx context.Context, clID, parentCL int64, triggerPS, resultingPS int, status, summary string) error {
	var resultPS *int
	if resultingPS > 0 {
		resultPS = &resultingPS
	}
	query := `
		INSERT INTO cl_rebase_events (cl_id, parent_cl, trigger_patchset_number, resulting_patchset_number, status, conflict_summary)
		VALUES ($1, $2, $3, $4, $5, $6);
	`
	_, err := d.pool.Exec(ctx, query, clID, parentCL, triggerPS, resultPS, status, summary)
	if err != nil {
		return fmt.Errorf("failed to record rebase event for CL %d: %w", clID, err)
	}
	return nil
}
