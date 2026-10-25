package db

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// Patchset represents an immutable snapshot of a changelist's tree at a specific revision.
type Patchset struct {
	PatchsetID     int64
	CLID           int64
	PatchsetNumber int
	RootTreeHash   []byte
	CreatedAt      time.Time
}

// Comment represents an inline or file-level code review comment thread entry.
// For single-line comments, EndLineNumber equals LineNumber.
// For multi-line code block comments, LineNumber is start line and EndLineNumber is end line.
type Comment struct {
	ID              int64
	CLID            int64
	PatchsetNumber  int
	FilePath        string
	LineNumber      int
	EndLineNumber   int
	Side            string // 'base' or 'modified'
	Author          string
	Body            string
	Resolved        bool
	ParentCommentID *int64
	CreatedAt       time.Time
}

// LineRangeStr returns a human-friendly range representation (e.g. "Line 42" or "Lines 42–48").
func (c Comment) LineRangeStr() string {
	if c.EndLineNumber > c.LineNumber {
		return fmt.Sprintf("Lines %d–%d", c.LineNumber, c.EndLineNumber)
	}
	return fmt.Sprintf("Line %d", c.LineNumber)
}

// IsMultiLine returns true if this comment spans multiple lines.
func (c Comment) IsMultiLine() bool {
	return c.EndLineNumber > c.LineNumber
}

// Approval represents a reviewer's verdict on a changelist.
type Approval struct {
	CLID      int64
	Reviewer  string
	Verdict   string // 'approved', 'changes_requested', 'lgtm'
	UpdatedAt time.Time
}

// ListPatchsets retrieves all patchsets associated with a changelist.
// If no patchsets exist yet in cl_patchsets for this CL, it automatically seeds Patchset 1
// using the CL's root_tree_hash.
func (d *DB) ListPatchsets(ctx context.Context, clID int64) ([]Patchset, error) {
	query := `
		SELECT patchset_id, cl_id, patchset_number, root_tree_hash, created_at
		FROM cl_patchsets
		WHERE cl_id = $1
		ORDER BY patchset_number ASC;
	`
	rows, err := d.pool.Query(ctx, query, clID)
	if err != nil {
		return nil, fmt.Errorf("failed to query patchsets for cl %d: %w", clID, err)
	}
	defer rows.Close()

	var patchsets []Patchset
	for rows.Next() {
		var p Patchset
		if err := rows.Scan(&p.PatchsetID, &p.CLID, &p.PatchsetNumber, &p.RootTreeHash, &p.CreatedAt); err != nil {
			return nil, fmt.Errorf("failed to scan patchset: %w", err)
		}
		patchsets = append(patchsets, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error reading patchsets: %w", err)
	}

	if len(patchsets) == 0 {
		// Look up changelist to seed Patchset 1
		cl, err := d.GetCL(ctx, clID)
		if err == nil && len(cl.RootTreeHash) > 0 {
			insertQuery := `
				INSERT INTO cl_patchsets (cl_id, patchset_number, root_tree_hash)
				VALUES ($1, 1, $2)
				ON CONFLICT (cl_id, patchset_number) DO NOTHING
				RETURNING patchset_id, created_at;
			`
			var p Patchset
			p.CLID = clID
			p.PatchsetNumber = 1
			p.RootTreeHash = cl.RootTreeHash
			scanErr := d.pool.QueryRow(ctx, insertQuery, clID, cl.RootTreeHash).Scan(&p.PatchsetID, &p.CreatedAt)
			if scanErr == nil {
				return []Patchset{p}, nil
			}
		}
	}

	return patchsets, nil
}

// GetPatchset retrieves a specific patchset by number for a changelist.
func (d *DB) GetPatchset(ctx context.Context, clID int64, patchsetNumber int) (*Patchset, error) {
	query := `
		SELECT patchset_id, cl_id, patchset_number, root_tree_hash, created_at
		FROM cl_patchsets
		WHERE cl_id = $1 AND patchset_number = $2;
	`
	var p Patchset
	err := d.pool.QueryRow(ctx, query, clID, patchsetNumber).Scan(
		&p.PatchsetID,
		&p.CLID,
		&p.PatchsetNumber,
		&p.RootTreeHash,
		&p.CreatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// Fallback: If asking for patchset 1 and it's not yet recorded, inspect the CL
			if patchsetNumber == 1 {
				cl, clErr := d.GetCL(ctx, clID)
				if clErr == nil && len(cl.RootTreeHash) > 0 {
					return &Patchset{
						PatchsetID:     0,
						CLID:           clID,
						PatchsetNumber: 1,
						RootTreeHash:   cl.RootTreeHash,
						CreatedAt:      cl.CommittedAt,
					}, nil
				}
			}
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("failed to query patchset %d for cl %d: %w", patchsetNumber, clID, err)
	}
	return &p, nil
}

// CreatePatchset registers a new patchset under a changelist.
func (d *DB) CreatePatchset(ctx context.Context, clID int64, patchsetNumber int, rootTreeHash []byte) error {
	query := `
		INSERT INTO cl_patchsets (cl_id, patchset_number, root_tree_hash)
		VALUES ($1, $2, $3)
		ON CONFLICT (cl_id, patchset_number) DO UPDATE
		SET root_tree_hash = EXCLUDED.root_tree_hash;
	`
	_, err := d.pool.Exec(ctx, query, clID, patchsetNumber, rootTreeHash)
	if err != nil {
		return fmt.Errorf("failed to insert patchset: %w", err)
	}
	return nil
}

// ListComments retrieves all comments for a specific file in a changelist.
func (d *DB) ListComments(ctx context.Context, clID int64, filePath string) ([]Comment, error) {
	query := `
		SELECT id, cl_id, patchset_number, file_path, line_number, end_line_number, side, author, body, resolved, parent_comment_id, created_at
		FROM cl_comments
		WHERE cl_id = $1 AND file_path = $2
		ORDER BY line_number ASC, end_line_number ASC, created_at ASC;
	`
	rows, err := d.pool.Query(ctx, query, clID, filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to query comments: %w", err)
	}
	defer rows.Close()

	var comments []Comment
	for rows.Next() {
		var c Comment
		if err := rows.Scan(
			&c.ID,
			&c.CLID,
			&c.PatchsetNumber,
			&c.FilePath,
			&c.LineNumber,
			&c.EndLineNumber,
			&c.Side,
			&c.Author,
			&c.Body,
			&c.Resolved,
			&c.ParentCommentID,
			&c.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("failed to scan comment: %w", err)
		}
		if c.EndLineNumber <= 0 {
			c.EndLineNumber = c.LineNumber
		}
		comments = append(comments, c)
	}
	return comments, rows.Err()
}

// ListCommentsForLine retrieves comments for a specific file, line, and side.
func (d *DB) ListCommentsForLine(ctx context.Context, clID int64, filePath string, line int, side string) ([]Comment, error) {
	query := `
		SELECT id, cl_id, patchset_number, file_path, line_number, end_line_number, side, author, body, resolved, parent_comment_id, created_at
		FROM cl_comments
		WHERE cl_id = $1 AND file_path = $2 AND (end_line_number = $3 OR (end_line_number <= 0 AND line_number = $3)) AND side = $4
		ORDER BY created_at ASC;
	`
	rows, err := d.pool.Query(ctx, query, clID, filePath, line, side)
	if err != nil {
		return nil, fmt.Errorf("failed to query comments for line: %w", err)
	}
	defer rows.Close()

	var comments []Comment
	for rows.Next() {
		var c Comment
		if err := rows.Scan(
			&c.ID,
			&c.CLID,
			&c.PatchsetNumber,
			&c.FilePath,
			&c.LineNumber,
			&c.EndLineNumber,
			&c.Side,
			&c.Author,
			&c.Body,
			&c.Resolved,
			&c.ParentCommentID,
			&c.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("failed to scan comment: %w", err)
		}
		if c.EndLineNumber <= 0 {
			c.EndLineNumber = c.LineNumber
		}
		comments = append(comments, c)
	}
	return comments, rows.Err()
}

// ListAllComments retrieves all comments for a changelist across all files.
func (d *DB) ListAllComments(ctx context.Context, clID int64) ([]Comment, error) {
	query := `
		SELECT id, cl_id, patchset_number, file_path, line_number, end_line_number, side, author, body, resolved, parent_comment_id, created_at
		FROM cl_comments
		WHERE cl_id = $1
		ORDER BY file_path ASC, line_number ASC, end_line_number ASC, created_at ASC;
	`
	rows, err := d.pool.Query(ctx, query, clID)
	if err != nil {
		return nil, fmt.Errorf("failed to query all comments for cl: %w", err)
	}
	defer rows.Close()

	var comments []Comment
	for rows.Next() {
		var c Comment
		if err := rows.Scan(
			&c.ID,
			&c.CLID,
			&c.PatchsetNumber,
			&c.FilePath,
			&c.LineNumber,
			&c.EndLineNumber,
			&c.Side,
			&c.Author,
			&c.Body,
			&c.Resolved,
			&c.ParentCommentID,
			&c.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("failed to scan comment: %w", err)
		}
		if c.EndLineNumber <= 0 {
			c.EndLineNumber = c.LineNumber
		}
		comments = append(comments, c)
	}
	return comments, rows.Err()
}

// CreateComment inserts a new comment into cl_comments with range support.
func (d *DB) CreateComment(ctx context.Context, clID int64, patchsetNumber int, filePath string, line, endLine int, side, author, body string, parentID *int64) (*Comment, error) {
	if endLine < line {
		endLine = line
	}
	query := `
		INSERT INTO cl_comments (cl_id, patchset_number, file_path, line_number, end_line_number, side, author, body, parent_comment_id)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		RETURNING id, cl_id, patchset_number, file_path, line_number, end_line_number, side, author, body, resolved, parent_comment_id, created_at;
	`
	var c Comment
	err := d.pool.QueryRow(ctx, query, clID, patchsetNumber, filePath, line, endLine, side, author, body, parentID).Scan(
		&c.ID,
		&c.CLID,
		&c.PatchsetNumber,
		&c.FilePath,
		&c.LineNumber,
		&c.EndLineNumber,
		&c.Side,
		&c.Author,
		&c.Body,
		&c.Resolved,
		&c.ParentCommentID,
		&c.CreatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to insert comment: %w", err)
	}
	return &c, nil
}

// ResolveComment updates the resolved status of a comment.
func (d *DB) ResolveComment(ctx context.Context, commentID int64, resolved bool) error {
	query := `
		UPDATE cl_comments
		SET resolved = $1
		WHERE id = $2;
	`
	res, err := d.pool.Exec(ctx, query, resolved, commentID)
	if err != nil {
		return fmt.Errorf("failed to update comment resolved state: %w", err)
	}
	if res.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// GetComment retrieves a single comment by ID.
func (d *DB) GetComment(ctx context.Context, commentID int64) (*Comment, error) {
	query := `
		SELECT id, cl_id, patchset_number, file_path, line_number, end_line_number, side, author, body, resolved, parent_comment_id, created_at
		FROM cl_comments
		WHERE id = $1;
	`
	var c Comment
	err := d.pool.QueryRow(ctx, query, commentID).Scan(
		&c.ID,
		&c.CLID,
		&c.PatchsetNumber,
		&c.FilePath,
		&c.LineNumber,
		&c.EndLineNumber,
		&c.Side,
		&c.Author,
		&c.Body,
		&c.Resolved,
		&c.ParentCommentID,
		&c.CreatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("failed to query comment %d: %w", commentID, err)
	}
	if c.EndLineNumber <= 0 {
		c.EndLineNumber = c.LineNumber
	}
	return &c, nil
}

// ListApprovals retrieves all reviewer approvals and verdicts for a changelist.
func (d *DB) ListApprovals(ctx context.Context, clID int64) ([]Approval, error) {
	query := `
		SELECT cl_id, reviewer, verdict, updated_at
		FROM cl_approvals
		WHERE cl_id = $1
		ORDER BY updated_at ASC;
	`
	rows, err := d.pool.Query(ctx, query, clID)
	if err != nil {
		return nil, fmt.Errorf("failed to query approvals: %w", err)
	}
	defer rows.Close()

	var approvals []Approval
	for rows.Next() {
		var a Approval
		if err := rows.Scan(&a.CLID, &a.Reviewer, &a.Verdict, &a.UpdatedAt); err != nil {
			return nil, fmt.Errorf("failed to scan approval: %w", err)
		}
		approvals = append(approvals, a)
	}
	return approvals, rows.Err()
}

// SetApproval sets or updates a reviewer's approval verdict on a changelist.
func (d *DB) SetApproval(ctx context.Context, clID int64, reviewer, verdict string) error {
	query := `
		INSERT INTO cl_approvals (cl_id, reviewer, verdict, updated_at)
		VALUES ($1, $2, $3, now())
		ON CONFLICT (cl_id, reviewer) DO UPDATE
		SET verdict = EXCLUDED.verdict, updated_at = now();
	`
	_, err := d.pool.Exec(ctx, query, clID, reviewer, verdict)
	if err != nil {
		return fmt.Errorf("failed to set approval: %w", err)
	}
	return nil
}

// ListCLs retrieves the most recent changelists.
func (d *DB) ListCLs(ctx context.Context, limit int) ([]Changelist, error) {
	if limit <= 0 {
		limit = 50
	}
	query := `
		SELECT cl_id, author, description, root_tree_hash, parent_cl, status, committed_at
		FROM changelists
		ORDER BY cl_id DESC
		LIMIT $1;
	`
	rows, err := d.pool.Query(ctx, query, limit)
	if err != nil {
		return nil, fmt.Errorf("failed to list changelists: %w", err)
	}
	defer rows.Close()

	var cls []Changelist
	for rows.Next() {
		var cl Changelist
		if err := rows.Scan(
			&cl.CLID,
			&cl.Author,
			&cl.Description,
			&cl.RootTreeHash,
			&cl.ParentCL,
			&cl.Status,
			&cl.CommittedAt,
		); err != nil {
			return nil, fmt.Errorf("failed to scan changelist: %w", err)
		}
		cls = append(cls, cl)
	}
	return cls, rows.Err()
}

// UpdateCLStatus updates the status of a changelist (e.g. 'shelved', 'submitted').
func (d *DB) UpdateCLStatus(ctx context.Context, clID int64, status string) error {
	query := `
		UPDATE changelists
		SET status = $1
		WHERE cl_id = $2;
	`
	res, err := d.pool.Exec(ctx, query, status, clID)
	if err != nil {
		return fmt.Errorf("failed to update changelist status: %w", err)
	}
	if res.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// FileReview captures a reviewer's reviewed status on a specific file in a changelist.
type FileReview struct {
	CLID      int64
	FilePath  string
	Reviewer  string
	Reviewed  bool
	UpdatedAt time.Time
}

// MarkFileReviewed sets or updates a reviewer's reviewed state on a specific file.
func (d *DB) MarkFileReviewed(ctx context.Context, clID int64, filePath, reviewer string, reviewed bool) error {
	query := `
		INSERT INTO cl_file_reviews (cl_id, file_path, reviewer, reviewed, updated_at)
		VALUES ($1, $2, $3, $4, now())
		ON CONFLICT (cl_id, file_path, reviewer) DO UPDATE
		SET reviewed = EXCLUDED.reviewed, updated_at = now();
	`
	_, err := d.pool.Exec(ctx, query, clID, filePath, reviewer, reviewed)
	if err != nil {
		return fmt.Errorf("failed to mark file %s reviewed: %w", filePath, err)
	}
	return nil
}

// ListReviewedFiles returns a set of file paths marked as reviewed by a specific reviewer for a changelist.
func (d *DB) ListReviewedFiles(ctx context.Context, clID int64, reviewer string) (map[string]bool, error) {
	query := `
		SELECT file_path
		FROM cl_file_reviews
		WHERE cl_id = $1 AND reviewer = $2 AND reviewed = true;
	`
	rows, err := d.pool.Query(ctx, query, clID, reviewer)
	if err != nil {
		return nil, fmt.Errorf("failed to list reviewed files for cl %d, reviewer %s: %w", clID, reviewer, err)
	}
	defer rows.Close()

	reviewed := make(map[string]bool)
	for rows.Next() {
		var path string
		if err := rows.Scan(&path); err != nil {
			return nil, fmt.Errorf("failed to scan reviewed file path: %w", err)
		}
		reviewed[path] = true
	}
	return reviewed, rows.Err()
}

