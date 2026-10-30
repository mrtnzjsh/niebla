package db

import (
	"context"
	"errors"
	"fmt"
	"strings"
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
	IsAutoRebase   bool
}

// Comment represents an inline or file-level code review comment thread entry.
// For single-line comments, EndLineNumber equals LineNumber.
// For multi-line code block comments, LineNumber is start line and EndLineNumber is end line.
type Comment struct {
	ID                  int64
	CLID                int64
	PatchsetNumber      int
	FilePath            string
	LineNumber          int
	EndLineNumber       int
	Side                string // 'base' or 'modified'
	Author              string
	Body                string
	Resolved            bool
	ParentCommentID     *int64
	IsDraft             bool
	HasSuggestion       bool
	SuggestionStartLine int
	SuggestionEndLine   int
	SuggestionCode      string
	SuggestionAppliedPS int // 0 = pending, >0 = applied in PS N, -1 = rejected
	CreatedAt           time.Time
}

// BodyWithoutSuggestionFence returns the comment text with any ```suggestion ... ``` block stripped,
// so the UI can render the explanatory text above a dedicated Suggested Change card.
func (c Comment) BodyWithoutSuggestionFence() string {
	idx := strings.Index(c.Body, "```suggestion")
	if idx == -1 {
		return c.Body
	}
	endIdx := strings.Index(c.Body[idx+13:], "```")
	if endIdx == -1 {
		return strings.TrimSpace(c.Body[:idx])
	}
	before := strings.TrimSpace(c.Body[:idx])
	after := strings.TrimSpace(c.Body[idx+13+endIdx+3:])
	if before != "" && after != "" {
		return before + "\n\n" + after
	}
	if before != "" {
		return before
	}
	return after
}

// ParseSuggestionFromMarkdown extracts a ```suggestion ... ``` code block from markdown text if present.
func ParseSuggestionFromMarkdown(body string) (code string, ok bool) {
	idx := strings.Index(body, "```suggestion")
	if idx == -1 {
		return "", false
	}
	rest := body[idx+len("```suggestion"):]
	if strings.HasPrefix(rest, "\r\n") {
		rest = rest[2:]
	} else if strings.HasPrefix(rest, "\n") {
		rest = rest[1:]
	}
	endIdx := strings.Index(rest, "```")
	if endIdx == -1 {
		return "", false
	}
	code = strings.TrimSuffix(rest[:endIdx], "\n")
	code = strings.TrimSuffix(code, "\r")
	return code, true
}

// FormatSuggestionMarkdown ensures the comment body includes a ```suggestion ... ``` markdown fence
// for dual-storage compatibility.
func FormatSuggestionMarkdown(body, suggestionCode string) string {
	if _, hasFence := ParseSuggestionFromMarkdown(body); hasFence || suggestionCode == "" {
		return body
	}
	trimmed := strings.TrimSpace(body)
	if trimmed == "" {
		return fmt.Sprintf("```suggestion\n%s\n```", suggestionCode)
	}
	return fmt.Sprintf("%s\n\n```suggestion\n%s\n```", trimmed, suggestionCode)
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
		SELECT patchset_id, cl_id, patchset_number, root_tree_hash, created_at, is_auto_rebase
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
		if err := rows.Scan(&p.PatchsetID, &p.CLID, &p.PatchsetNumber, &p.RootTreeHash, &p.CreatedAt, &p.IsAutoRebase); err != nil {
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
				INSERT INTO cl_patchsets (cl_id, patchset_number, root_tree_hash, is_auto_rebase)
				VALUES ($1, 1, $2, false)
				ON CONFLICT (cl_id, patchset_number) DO NOTHING
				RETURNING patchset_id, created_at;
			`
			var p Patchset
			p.CLID = clID
			p.PatchsetNumber = 1
			p.RootTreeHash = cl.RootTreeHash
			p.IsAutoRebase = false
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
		SELECT patchset_id, cl_id, patchset_number, root_tree_hash, created_at, is_auto_rebase
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
		&p.IsAutoRebase,
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
						IsAutoRebase:   false,
					}, nil
				}
			}
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("failed to query patchset %d for cl %d: %w", patchsetNumber, clID, err)
	}
	return &p, nil
}

// GetNextPatchsetNumber determines the next sequential patchset number for a changelist.
func (d *DB) GetNextPatchsetNumber(ctx context.Context, clID int64) (int, error) {
	query := `
		SELECT COALESCE(MAX(patchset_number), 0) + 1
		FROM cl_patchsets
		WHERE cl_id = $1;
	`
	var nextNum int
	err := d.pool.QueryRow(ctx, query, clID).Scan(&nextNum)
	if err != nil {
		return 1, fmt.Errorf("failed to get next patchset number for CL %d: %w", clID, err)
	}
	return nextNum, nil
}

// CreatePatchset registers a new patchset under a changelist.
func (d *DB) CreatePatchset(ctx context.Context, clID int64, patchsetNumber int, rootTreeHash []byte) error {
	query := `
		INSERT INTO cl_patchsets (cl_id, patchset_number, root_tree_hash, is_auto_rebase)
		VALUES ($1, $2, $3, false)
		ON CONFLICT (cl_id, patchset_number) DO UPDATE
		SET root_tree_hash = EXCLUDED.root_tree_hash;
	`
	_, err := d.pool.Exec(ctx, query, clID, patchsetNumber, rootTreeHash)
	if err != nil {
		return fmt.Errorf("failed to insert patchset: %w", err)
	}
	return nil
}

// CreateAutoRebasePatchset registers an auto-generated rebase patchset under a changelist.
func (d *DB) CreateAutoRebasePatchset(ctx context.Context, clID int64, patchsetNumber int, rootTreeHash []byte) error {
	query := `
		INSERT INTO cl_patchsets (cl_id, patchset_number, root_tree_hash, is_auto_rebase)
		VALUES ($1, $2, $3, true)
		ON CONFLICT (cl_id, patchset_number) DO UPDATE
		SET root_tree_hash = EXCLUDED.root_tree_hash, is_auto_rebase = true;
	`
	_, err := d.pool.Exec(ctx, query, clID, patchsetNumber, rootTreeHash)
	if err != nil {
		return fmt.Errorf("failed to insert auto-rebase patchset: %w", err)
	}
	return nil
}

const commentSelectCols = `id, cl_id, patchset_number, file_path, line_number, end_line_number, side, author, body, resolved, parent_comment_id, is_draft, has_suggestion, suggestion_start_line, suggestion_end_line, suggestion_code, suggestion_applied_ps, created_at`

func scanCommentRow(scanner interface{ Scan(dest ...any) error }) (Comment, error) {
	var c Comment
	err := scanner.Scan(
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
		&c.IsDraft,
		&c.HasSuggestion,
		&c.SuggestionStartLine,
		&c.SuggestionEndLine,
		&c.SuggestionCode,
		&c.SuggestionAppliedPS,
		&c.CreatedAt,
	)
	if err != nil {
		return Comment{}, err
	}
	if c.EndLineNumber <= 0 {
		c.EndLineNumber = c.LineNumber
	}
	if !c.HasSuggestion {
		if code, ok := ParseSuggestionFromMarkdown(c.Body); ok {
			c.HasSuggestion = true
			c.SuggestionCode = code
			if c.SuggestionStartLine <= 0 {
				c.SuggestionStartLine = c.LineNumber
			}
			if c.SuggestionEndLine < c.SuggestionStartLine {
				c.SuggestionEndLine = c.EndLineNumber
			}
		}
	}
	return c, nil
}

// ListComments retrieves all comments for a specific file in a changelist.
func (d *DB) ListComments(ctx context.Context, clID int64, filePath string) ([]Comment, error) {
	query := `
		SELECT ` + commentSelectCols + `
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
		c, err := scanCommentRow(rows)
		if err != nil {
			return nil, fmt.Errorf("failed to scan comment: %w", err)
		}
		comments = append(comments, c)
	}
	return comments, rows.Err()
}

// ListCommentsForLine retrieves comments for a specific file, line, and side.
func (d *DB) ListCommentsForLine(ctx context.Context, clID int64, filePath string, line int, side string) ([]Comment, error) {
	query := `
		SELECT ` + commentSelectCols + `
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
		c, err := scanCommentRow(rows)
		if err != nil {
			return nil, fmt.Errorf("failed to scan comment: %w", err)
		}
		comments = append(comments, c)
	}
	return comments, rows.Err()
}

// ListAllComments retrieves all comments for a changelist across all files.
func (d *DB) ListAllComments(ctx context.Context, clID int64) ([]Comment, error) {
	query := `
		SELECT ` + commentSelectCols + `
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
		c, err := scanCommentRow(rows)
		if err != nil {
			return nil, fmt.Errorf("failed to scan comment: %w", err)
		}
		comments = append(comments, c)
	}
	return comments, rows.Err()
}

// CreateComment inserts a new comment into cl_comments with range support and draft status.
// If the comment body contains a ```suggestion ... ``` markdown block, the suggestion columns
// are automatically populated as well.
func (d *DB) CreateComment(ctx context.Context, clID int64, patchsetNumber int, filePath string, line, endLine int, side, author, body string, parentID *int64, isDraft bool) (*Comment, error) {
	if endLine < line {
		endLine = line
	}
	var suggStart, suggEnd int
	var suggCode string
	if parsed, ok := ParseSuggestionFromMarkdown(body); ok {
		suggStart = line
		suggEnd = endLine
		suggCode = parsed
	}
	return d.CreateCommentWithSuggestion(ctx, clID, patchsetNumber, filePath, line, endLine, side, author, body, parentID, isDraft, suggStart, suggEnd, suggCode)
}

// CreateCommentWithSuggestion inserts a comment along with dual-stored suggestion metadata (both markdown fence in body and DB columns).
func (d *DB) CreateCommentWithSuggestion(
	ctx context.Context,
	clID int64,
	patchsetNumber int,
	filePath string,
	line, endLine int,
	side, author, body string,
	parentID *int64,
	isDraft bool,
	suggestionStart, suggestionEnd int,
	suggestionCode string,
) (*Comment, error) {
	if endLine < line {
		endLine = line
	}
	hasSuggestion := false
	if suggestionCode != "" {
		hasSuggestion = true
		if suggestionStart <= 0 {
			suggestionStart = line
		}
		if suggestionEnd < suggestionStart {
			suggestionEnd = endLine
		}
		body = FormatSuggestionMarkdown(body, suggestionCode)
	} else if parsed, ok := ParseSuggestionFromMarkdown(body); ok {
		hasSuggestion = true
		suggestionCode = parsed
		if suggestionStart <= 0 {
			suggestionStart = line
		}
		if suggestionEnd < suggestionStart {
			suggestionEnd = endLine
		}
	}

	query := `
		INSERT INTO cl_comments (
			cl_id, patchset_number, file_path, line_number, end_line_number, side, author, body, parent_comment_id, is_draft,
			has_suggestion, suggestion_start_line, suggestion_end_line, suggestion_code
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)
		RETURNING ` + commentSelectCols + `;
	`
	c, err := scanCommentRow(d.pool.QueryRow(
		ctx,
		query,
		clID,
		patchsetNumber,
		filePath,
		line,
		endLine,
		side,
		author,
		body,
		parentID,
		isDraft,
		hasSuggestion,
		suggestionStart,
		suggestionEnd,
		suggestionCode,
	))
	if err != nil {
		return nil, fmt.Errorf("failed to insert comment: %w", err)
	}
	return &c, nil
}

// MarkSuggestionApplied records the patchset where a comment's suggestion was applied
// and automatically resolves the comment thread.
func (d *DB) MarkSuggestionApplied(ctx context.Context, commentID int64, appliedPS int) error {
	c, err := d.GetComment(ctx, commentID)
	if err != nil {
		return err
	}
	query := `
		UPDATE cl_comments
		SET suggestion_applied_ps = $1, resolved = true
		WHERE id = $2;
	`
	if _, err := d.pool.Exec(ctx, query, appliedPS, commentID); err != nil {
		return fmt.Errorf("failed to mark suggestion applied: %w", err)
	}
	// Also resolve all comments in the same inline thread
	threadQuery := `
		UPDATE cl_comments
		SET resolved = true
		WHERE cl_id = $1 AND file_path = $2 AND (end_line_number = $3 OR (end_line_number <= 0 AND line_number = $3)) AND side = $4;
	`
	_, _ = d.pool.Exec(ctx, threadQuery, c.CLID, c.FilePath, c.EndLineNumber, c.Side)
	return nil
}

// RejectCommentSuggestion marks a comment's code suggestion as rejected (suggestion_applied_ps = -1).
func (d *DB) RejectCommentSuggestion(ctx context.Context, commentID int64) error {
	query := `
		UPDATE cl_comments
		SET suggestion_applied_ps = -1
		WHERE id = $1;
	`
	res, err := d.pool.Exec(ctx, query, commentID)
	if err != nil {
		return fmt.Errorf("failed to reject comment suggestion: %w", err)
	}
	if res.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ListDraftComments retrieves all unposted draft comments by a specific author on a changelist.
func (d *DB) ListDraftComments(ctx context.Context, clID int64, author string) ([]Comment, error) {
	query := `
		SELECT ` + commentSelectCols + `
		FROM cl_comments
		WHERE cl_id = $1 AND author = $2 AND is_draft = true
		ORDER BY file_path ASC, line_number ASC, created_at ASC;
	`
	rows, err := d.pool.Query(ctx, query, clID, author)
	if err != nil {
		return nil, fmt.Errorf("failed to query draft comments: %w", err)
	}
	defer rows.Close()

	var comments []Comment
	for rows.Next() {
		c, err := scanCommentRow(rows)
		if err != nil {
			return nil, fmt.Errorf("failed to scan draft comment: %w", err)
		}
		comments = append(comments, c)
	}
	return comments, rows.Err()
}

// CountDraftComments returns the number of unposted drafts for a reviewer on a changelist.
func (d *DB) CountDraftComments(ctx context.Context, clID int64, author string) (int, error) {
	query := `
		SELECT COUNT(*)
		FROM cl_comments
		WHERE cl_id = $1 AND author = $2 AND is_draft = true;
	`
	var count int
	err := d.pool.QueryRow(ctx, query, clID, author).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("failed to count draft comments: %w", err)
	}
	return count, nil
}

// PublishDraftComments marks all draft comments for a reviewer as published.
func (d *DB) PublishDraftComments(ctx context.Context, clID int64, author string) (int64, error) {
	query := `
		UPDATE cl_comments
		SET is_draft = false
		WHERE cl_id = $1 AND author = $2 AND is_draft = true;
	`
	res, err := d.pool.Exec(ctx, query, clID, author)
	if err != nil {
		return 0, fmt.Errorf("failed to publish draft comments: %w", err)
	}
	return res.RowsAffected(), nil
}

// DiscardDraftComment deletes an unposted draft comment.
func (d *DB) DiscardDraftComment(ctx context.Context, commentID int64, author string) error {
	query := `
		DELETE FROM cl_comments
		WHERE id = $1 AND author = $2 AND is_draft = true;
	`
	res, err := d.pool.Exec(ctx, query, commentID, author)
	if err != nil {
		return fmt.Errorf("failed to discard draft comment: %w", err)
	}
	if res.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
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
		SELECT ` + commentSelectCols + `
		FROM cl_comments
		WHERE id = $1;
	`
	c, err := scanCommentRow(d.pool.QueryRow(ctx, query, commentID))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("failed to query comment %d: %w", commentID, err)
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
		SELECT cl_id, author, description, root_tree_hash, parent_cl, status, trunk_revision, committed_at,
		       needs_rebase, rebase_conflict_details, base_patchset_number
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
			&cl.TrunkRevision,
			&cl.CommittedAt,
			&cl.NeedsRebase,
			&cl.RebaseConflictDetails,
			&cl.BasePatchsetNumber,
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

// ReviewerAssignment captures an assigned reviewer, their review verdict, and timestamp.
type ReviewerAssignment struct {
	CLID      int64     `json:"cl_id"`
	Reviewer  string    `json:"reviewer"`
	Verdict   string    `json:"verdict"` // "pending", "lgtm", "changes_requested", "approved"
	UpdatedAt time.Time `json:"updated_at"`
}

// AddReviewers registers a list of reviewers to a changelist.
func (d *DB) AddReviewers(ctx context.Context, clID int64, reviewers []string) error {
	if len(reviewers) == 0 {
		return nil
	}
	query := `
		INSERT INTO cl_reviewers (cl_id, reviewer, created_at)
		VALUES ($1, $2, now())
		ON CONFLICT (cl_id, reviewer) DO NOTHING;
	`
	for _, r := range reviewers {
		trimmed := strings.TrimSpace(r)
		if trimmed == "" {
			continue
		}
		if _, err := d.pool.Exec(ctx, query, clID, trimmed); err != nil {
			return fmt.Errorf("failed to add reviewer %s to cl %d: %w", trimmed, clID, err)
		}
	}
	return nil
}

// RemoveReviewer unassigns a reviewer from a changelist.
func (d *DB) RemoveReviewer(ctx context.Context, clID int64, reviewer string) error {
	query := `DELETE FROM cl_reviewers WHERE cl_id = $1 AND reviewer = $2;`
	if _, err := d.pool.Exec(ctx, query, clID, strings.TrimSpace(reviewer)); err != nil {
		return fmt.Errorf("failed to remove reviewer %s from cl %d: %w", reviewer, clID, err)
	}
	return nil
}

// ListReviewers returns all reviewers assigned to a changelist along with any verdicts.
// Reviewers who submitted a verdict are included even if not explicitly pre-assigned.
func (d *DB) ListReviewers(ctx context.Context, clID int64) ([]ReviewerAssignment, error) {
	query := `
		SELECT COALESCE(r.cl_id, a.cl_id) as cl_id,
		       COALESCE(r.reviewer, a.reviewer) as reviewer,
		       COALESCE(a.verdict, 'pending') as verdict,
		       COALESCE(a.updated_at, r.created_at) as updated_at
		FROM cl_reviewers r
		FULL OUTER JOIN cl_approvals a ON r.cl_id = a.cl_id AND r.reviewer = a.reviewer
		WHERE COALESCE(r.cl_id, a.cl_id) = $1
		ORDER BY reviewer ASC;
	`
	rows, err := d.pool.Query(ctx, query, clID)
	if err != nil {
		return nil, fmt.Errorf("failed to list reviewers for cl %d: %w", clID, err)
	}
	defer rows.Close()

	var reviewers []ReviewerAssignment
	for rows.Next() {
		var ra ReviewerAssignment
		if err := rows.Scan(&ra.CLID, &ra.Reviewer, &ra.Verdict, &ra.UpdatedAt); err != nil {
			return nil, fmt.Errorf("failed to scan reviewer assignment: %w", err)
		}
		reviewers = append(reviewers, ra)
	}
	return reviewers, rows.Err()
}

// CountUnresolvedComments returns the number of published, unresolved comments on a changelist.
func (d *DB) CountUnresolvedComments(ctx context.Context, clID int64) (int, error) {
	query := `
		SELECT COUNT(*)
		FROM cl_comments
		WHERE cl_id = $1 AND resolved = false AND is_draft = false;
	`
	var count int
	if err := d.pool.QueryRow(ctx, query, clID).Scan(&count); err != nil {
		return 0, fmt.Errorf("failed to count unresolved comments for cl %d: %w", clID, err)
	}
	return count, nil
}

// GetAssignedCLIDsForUser returns a set of changelist IDs where the user or any of their groups is assigned as a reviewer.
func (d *DB) GetAssignedCLIDsForUser(ctx context.Context, username string, groupNames []string) (map[int64]bool, error) {
	targets := []string{username}
	for _, g := range groupNames {
		g = strings.TrimSpace(g)
		if g != "" {
			if !strings.HasPrefix(g, "@") {
				targets = append(targets, "@"+g)
			}
			targets = append(targets, g)
		}
	}

	query := `
		SELECT DISTINCT cl_id
		FROM cl_reviewers
		WHERE reviewer = ANY($1);
	`
	rows, err := d.pool.Query(ctx, query, targets)
	if err != nil {
		return nil, fmt.Errorf("failed to query assigned CLs: %w", err)
	}
	defer rows.Close()

	clIDs := make(map[int64]bool)
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		clIDs[id] = true
	}
	return clIDs, rows.Err()
}

// CountApprovals returns the number of reviewers who have granted an approval ("lgtm" or "approved") on a changelist.
func (d *DB) CountApprovals(ctx context.Context, clID int64) (int, error) {
	query := `
		SELECT COUNT(*)
		FROM cl_approvals
		WHERE cl_id = $1 AND verdict IN ('lgtm', 'approved');
	`
	var count int
	if err := d.pool.QueryRow(ctx, query, clID).Scan(&count); err != nil {
		return 0, fmt.Errorf("failed to count approvals for cl %d: %w", clID, err)
	}
	return count, nil
}

// GetApprovedReviewers returns the usernames of all reviewers who approved the changelist ("lgtm" or "approved").
func (d *DB) GetApprovedReviewers(ctx context.Context, clID int64) ([]string, error) {
	query := `
		SELECT reviewer
		FROM cl_approvals
		WHERE cl_id = $1 AND verdict IN ('lgtm', 'approved')
		ORDER BY reviewer ASC;
	`
	rows, err := d.pool.Query(ctx, query, clID)
	if err != nil {
		return nil, fmt.Errorf("failed to query approved reviewers for CL %d: %w", clID, err)
	}
	defer rows.Close()

	var reviewers []string
	for rows.Next() {
		var rev string
		if err := rows.Scan(&rev); err != nil {
			return nil, err
		}
		reviewers = append(reviewers, rev)
	}
	return reviewers, nil
}
