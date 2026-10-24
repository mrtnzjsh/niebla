package db

import (
	"context"
	"errors"
	"fmt"
	"time"

	"citc-toolchain/pkg/tree"
	"github.com/jackc/pgx/v5"
)

// ErrNotFound is returned when a requested record is not found in the database.
var ErrNotFound = errors.New("record not found")

// BlobRecord represents an entry in the blobs table.
type BlobRecord struct {
	SHA256      []byte
	ByteSize    int64
	StoragePath string
	CreatedAt   time.Time
}

// Changelist represents an entry in the changelists table.
type Changelist struct {
	CLID         int64
	Author       string
	Description  string
	RootTreeHash []byte
	ParentCL     *int64
	Status       string
	CommittedAt  time.Time
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
		SELECT name, mode, size, blob_sha256, sub_tree_hash
		FROM tree_nodes
		WHERE tree_hash = $1
		ORDER BY name ASC;
	`
	rows, err := d.pool.Query(ctx, query, treeHash)
	if err != nil {
		return nil, fmt.Errorf("failed to query tree nodes: %w", err)
	}
	defer rows.Close()

	var entries []tree.Entry
	for rows.Next() {
		var (
			e           tree.Entry
			mode        int
			size        int64
			blobSHA     []byte
			subTreeHash []byte
		)
		if err := rows.Scan(&e.Name, &mode, &size, &blobSHA, &subTreeHash); err != nil {
			return nil, fmt.Errorf("failed to scan tree node: %w", err)
		}
		e.Mode = uint32(mode)
		e.Size = uint64(size)
		e.BlobSHA256 = blobSHA
		e.SubTreeHash = subTreeHash
		entries = append(entries, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows error while querying tree nodes: %w", err)
	}

	return entries, nil
}

// GetNodeEntry retrieves a specific child node by name within a parent tree.
func (d *DB) GetNodeEntry(ctx context.Context, parentTreeHash []byte, name string) (*tree.Entry, error) {
	query := `
		SELECT name, mode, size, blob_sha256, sub_tree_hash
		FROM tree_nodes
		WHERE tree_hash = $1 AND name = $2;
	`
	var (
		e           tree.Entry
		mode        int
		size        int64
		blobSHA     []byte
		subTreeHash []byte
	)
	err := d.pool.QueryRow(ctx, query, parentTreeHash, name).Scan(
		&e.Name,
		&mode,
		&size,
		&blobSHA,
		&subTreeHash,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("failed to query tree node %q: %w", name, err)
	}
	e.Mode = uint32(mode)
	e.Size = uint64(size)
	e.BlobSHA256 = blobSHA
	e.SubTreeHash = subTreeHash
	return &e, nil
}

// InsertTreeNodes persists multiple tree node entries under a tree hash.
func (d *DB) InsertTreeNodes(ctx context.Context, treeHash []byte, entries []tree.Entry) error {
	if len(entries) == 0 {
		return nil
	}

	tx, err := d.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("failed to start transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	query := `
		INSERT INTO tree_nodes (tree_hash, name, mode, size, blob_sha256, sub_tree_hash)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (tree_hash, name) DO NOTHING;
	`
	for _, e := range entries {
		var blobSHA interface{} = e.BlobSHA256
		if len(e.BlobSHA256) == 0 {
			blobSHA = nil
		}
		var subTree interface{} = e.SubTreeHash
		if len(e.SubTreeHash) == 0 {
			subTree = nil
		}

		if _, err := tx.Exec(ctx, query, treeHash, e.Name, int(e.Mode), int64(e.Size), blobSHA, subTree); err != nil {
			return fmt.Errorf("failed to insert tree node %q: %w", e.Name, err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("failed to commit tree nodes: %w", err)
	}
	return nil
}

// GetHeadCL retrieves the latest committed (status = 'submitted') changelist.
func (d *DB) GetHeadCL(ctx context.Context) (*Changelist, error) {
	query := `
		SELECT cl_id, author, description, root_tree_hash, parent_cl, status, committed_at
		FROM changelists
		WHERE status = 'submitted'
		ORDER BY cl_id DESC
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
		&cl.CommittedAt,
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
		SELECT cl_id, author, description, root_tree_hash, parent_cl, status, committed_at
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
		&cl.CommittedAt,
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

// SubmitCL atomically updates a changelist status to 'submitted' in a transaction.
func (d *DB) SubmitCL(ctx context.Context, clID int64) (*Changelist, error) {
	tx, err := d.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to start submit transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	// Lock the row for update
	selectQuery := `
		SELECT cl_id, author, description, root_tree_hash, parent_cl, status, committed_at
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
		&cl.CommittedAt,
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

	updateQuery := `
		UPDATE changelists
		SET status = 'submitted', committed_at = now()
		WHERE cl_id = $1
		RETURNING committed_at;
	`
	var committedAt time.Time
	err = tx.QueryRow(ctx, updateQuery, clID).Scan(&committedAt)
	if err != nil {
		return nil, fmt.Errorf("failed to update CL status: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("failed to commit submit transaction: %w", err)
	}

	cl.Status = "submitted"
	cl.CommittedAt = committedAt
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
