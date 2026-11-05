package db

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"niebla/internal/db/store"
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
	rows, err := d.queries.ListPatchsets(ctx, clID)
	if err != nil {
		return nil, fmt.Errorf("failed to query patchsets for cl %d: %w", clID, err)
	}

	if len(rows) == 0 {
		// Look up changelist to seed Patchset 1
		cl, err := d.GetCL(ctx, clID)
		if err == nil && len(cl.RootTreeHash) > 0 {
			res, scanErr := d.queries.SeedPatchsetOne(ctx, store.SeedPatchsetOneParams{
				ClID:         clID,
				RootTreeHash: cl.RootTreeHash,
			})
			if scanErr == nil {
				return []Patchset{{
					PatchsetID:     int64(res.PatchsetID),
					CLID:           clID,
					PatchsetNumber: 1,
					RootTreeHash:   cl.RootTreeHash,
					CreatedAt:      res.CreatedAt.Time,
					IsAutoRebase:   false,
				}}, nil
			}
		}
	}

	patchsets := make([]Patchset, len(rows))
	for i, r := range rows {
		patchsets[i] = Patchset{
			PatchsetID:     int64(r.PatchsetID),
			CLID:           r.ClID,
			PatchsetNumber: int(r.PatchsetNumber),
			RootTreeHash:   r.RootTreeHash,
			CreatedAt:      r.CreatedAt.Time,
			IsAutoRebase:   r.IsAutoRebase,
		}
	}
	return patchsets, nil
}

// GetPatchset retrieves a specific patchset by number for a changelist.
func (d *DB) GetPatchset(ctx context.Context, clID int64, patchsetNumber int) (*Patchset, error) {
	row, err := d.queries.GetPatchsetByNumber(ctx, store.GetPatchsetByNumberParams{
		ClID:           clID,
		PatchsetNumber: int32(patchsetNumber),
	})
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
	p := Patchset{
		PatchsetID:     int64(row.PatchsetID),
		CLID:           row.ClID,
		PatchsetNumber: int(row.PatchsetNumber),
		RootTreeHash:   row.RootTreeHash,
		CreatedAt:      row.CreatedAt.Time,
		IsAutoRebase:   row.IsAutoRebase,
	}
	return &p, nil
}

// GetNextPatchsetNumber determines the next sequential patchset number for a changelist.
func (d *DB) GetNextPatchsetNumber(ctx context.Context, clID int64) (int, error) {
	nextNum, err := d.queries.GetNextPatchsetNumber(ctx, clID)
	if err != nil {
		return 1, fmt.Errorf("failed to get next patchset number for CL %d: %w", clID, err)
	}
	return int(nextNum), nil
}

// CreatePatchset registers a new patchset under a changelist.
func (d *DB) CreatePatchset(ctx context.Context, clID int64, patchsetNumber int, rootTreeHash []byte) error {
	err := d.queries.UpsertPatchset(ctx, store.UpsertPatchsetParams{
		ClID:           clID,
		PatchsetNumber: int32(patchsetNumber),
		RootTreeHash:   rootTreeHash,
		IsAutoRebase:   false,
	})
	if err != nil {
		return fmt.Errorf("failed to insert patchset: %w", err)
	}
	return nil
}

// CreateAutoRebasePatchset registers an auto-generated rebase patchset under a changelist.
func (d *DB) CreateAutoRebasePatchset(ctx context.Context, clID int64, patchsetNumber int, rootTreeHash []byte) error {
	err := d.queries.UpsertPatchset(ctx, store.UpsertPatchsetParams{
		ClID:           clID,
		PatchsetNumber: int32(patchsetNumber),
		RootTreeHash:   rootTreeHash,
		IsAutoRebase:   true,
	})
	if err != nil {
		return fmt.Errorf("failed to insert auto-rebase patchset: %w", err)
	}
	return nil
}

// ListComments retrieves all comments for a specific file in a changelist.
func (d *DB) ListComments(ctx context.Context, clID int64, filePath string) ([]Comment, error) {
	rows, err := d.queries.ListComments(ctx, store.ListCommentsParams{
		ClID:     clID,
		FilePath: filePath,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to query comments: %w", err)
	}

	comments := make([]Comment, len(rows))
	for i, r := range rows {
		comments[i] = toComment(r)
	}
	return comments, nil
}

// ListCommentsForLine retrieves comments for a specific file, line, and side.
func (d *DB) ListCommentsForLine(ctx context.Context, clID int64, filePath string, line int, side string) ([]Comment, error) {
	rows, err := d.queries.ListCommentsForLine(ctx, store.ListCommentsForLineParams{
		ClID:          clID,
		FilePath:      filePath,
		EndLineNumber: int32(line),
		Side:          side,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to query comments for line: %w", err)
	}

	comments := make([]Comment, len(rows))
	for i, r := range rows {
		comments[i] = toComment(r)
	}
	return comments, nil
}

// ListAllComments retrieves all comments for a changelist across all files.
func (d *DB) ListAllComments(ctx context.Context, clID int64) ([]Comment, error) {
	rows, err := d.queries.ListAllComments(ctx, clID)
	if err != nil {
		return nil, fmt.Errorf("failed to query all comments for cl: %w", err)
	}

	comments := make([]Comment, len(rows))
	for i, r := range rows {
		comments[i] = toComment(r)
	}
	return comments, nil
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

	var parentVal pgtype.Int8
	if parentID != nil {
		parentVal = pgtype.Int8{Int64: *parentID, Valid: true}
	}

	row, err := d.queries.InsertComment(ctx, store.InsertCommentParams{
		ClID:                clID,
		PatchsetNumber:      int32(patchsetNumber),
		FilePath:            filePath,
		LineNumber:          int32(line),
		EndLineNumber:       int32(endLine),
		Side:                side,
		Author:              author,
		Body:                body,
		ParentCommentID:     parentVal,
		IsDraft:             isDraft,
		HasSuggestion:       hasSuggestion,
		SuggestionStartLine: int32(suggestionStart),
		SuggestionEndLine:   int32(suggestionEnd),
		SuggestionCode:      suggestionCode,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to insert comment: %w", err)
	}
	c := toComment(row)
	return &c, nil
}

// MarkSuggestionApplied records the patchset where a comment's suggestion was applied
// and automatically resolves the comment thread.
func (d *DB) MarkSuggestionApplied(ctx context.Context, commentID int64, appliedPS int) error {
	c, err := d.GetComment(ctx, commentID)
	if err != nil {
		return err
	}
	if err := d.queries.MarkSuggestionApplied(ctx, store.MarkSuggestionAppliedParams{
		SuggestionAppliedPs: int32(appliedPS),
		ID:                  commentID,
	}); err != nil {
		return fmt.Errorf("failed to mark suggestion applied: %w", err)
	}
	// Also resolve all comments in the same inline thread
	_ = d.queries.ResolveCommentThread(ctx, store.ResolveCommentThreadParams{
		ClID:          c.CLID,
		FilePath:      c.FilePath,
		EndLineNumber: int32(c.EndLineNumber),
		Side:          c.Side,
	})
	return nil
}

// RejectCommentSuggestion marks a comment's code suggestion as rejected (suggestion_applied_ps = -1).
func (d *DB) RejectCommentSuggestion(ctx context.Context, commentID int64) error {
	res, err := d.queries.RejectCommentSuggestion(ctx, commentID)
	if err != nil {
		return fmt.Errorf("failed to reject comment suggestion: %w", err)
	}
	if res == 0 {
		return ErrNotFound
	}
	return nil
}

// ListDraftComments retrieves all unposted draft comments by a specific author on a changelist.
func (d *DB) ListDraftComments(ctx context.Context, clID int64, author string) ([]Comment, error) {
	rows, err := d.queries.ListDraftComments(ctx, store.ListDraftCommentsParams{
		ClID:   clID,
		Author: author,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to query draft comments: %w", err)
	}

	comments := make([]Comment, len(rows))
	for i, r := range rows {
		comments[i] = toComment(r)
	}
	return comments, nil
}

// CountDraftComments returns the number of unposted drafts for a reviewer on a changelist.
func (d *DB) CountDraftComments(ctx context.Context, clID int64, author string) (int, error) {
	count, err := d.queries.CountDraftComments(ctx, store.CountDraftCommentsParams{
		ClID:   clID,
		Author: author,
	})
	if err != nil {
		return 0, fmt.Errorf("failed to count draft comments: %w", err)
	}
	return int(count), nil
}

// PublishDraftComments marks all draft comments for a reviewer as published.
func (d *DB) PublishDraftComments(ctx context.Context, clID int64, author string) (int64, error) {
	res, err := d.queries.PublishDraftComments(ctx, store.PublishDraftCommentsParams{
		ClID:   clID,
		Author: author,
	})
	if err != nil {
		return 0, fmt.Errorf("failed to publish draft comments: %w", err)
	}
	return res, nil
}

// DiscardDraftComment deletes an unposted draft comment.
func (d *DB) DiscardDraftComment(ctx context.Context, commentID int64, author string) error {
	res, err := d.queries.DiscardDraftComment(ctx, store.DiscardDraftCommentParams{
		ID:     commentID,
		Author: author,
	})
	if err != nil {
		return fmt.Errorf("failed to discard draft comment: %w", err)
	}
	if res == 0 {
		return ErrNotFound
	}
	return nil
}

// ResolveComment updates the resolved status of a comment.
func (d *DB) ResolveComment(ctx context.Context, commentID int64, resolved bool) error {
	res, err := d.queries.ResolveComment(ctx, store.ResolveCommentParams{
		Resolved: resolved,
		ID:       commentID,
	})
	if err != nil {
		return fmt.Errorf("failed to update comment resolved state: %w", err)
	}
	if res == 0 {
		return ErrNotFound
	}
	return nil
}

// GetComment retrieves a single comment by ID.
func (d *DB) GetComment(ctx context.Context, commentID int64) (*Comment, error) {
	row, err := d.queries.GetComment(ctx, commentID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("failed to query comment %d: %w", commentID, err)
	}
	c := toComment(row)
	return &c, nil
}

// ListApprovals retrieves all reviewer approvals and verdicts for a changelist.
func (d *DB) ListApprovals(ctx context.Context, clID int64) ([]Approval, error) {
	rows, err := d.queries.ListApprovals(ctx, clID)
	if err != nil {
		return nil, fmt.Errorf("failed to query approvals: %w", err)
	}

	approvals := make([]Approval, len(rows))
	for i, r := range rows {
		approvals[i] = Approval{
			CLID:      r.ClID,
			Reviewer:  r.Reviewer,
			Verdict:   r.Verdict,
			UpdatedAt: r.UpdatedAt.Time,
		}
	}
	return approvals, nil
}

// SetApproval sets or updates a reviewer's approval verdict on a changelist.
func (d *DB) SetApproval(ctx context.Context, clID int64, reviewer, verdict string) error {
	err := d.queries.SetApproval(ctx, store.SetApprovalParams{
		ClID:     clID,
		Reviewer: reviewer,
		Verdict:  verdict,
	})
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
	rows, err := d.queries.ListCLs(ctx, int32(limit))
	if err != nil {
		return nil, fmt.Errorf("failed to list changelists: %w", err)
	}

	cls := make([]Changelist, len(rows))
	for i, r := range rows {
		cls[i] = toChangelist(r)
	}
	return cls, nil
}

// UpdateCLStatus updates the status of a changelist (e.g. 'shelved', 'submitted').
func (d *DB) UpdateCLStatus(ctx context.Context, clID int64, status string) error {
	res, err := d.queries.UpdateCLStatus(ctx, store.UpdateCLStatusParams{
		Status: status,
		ClID:   clID,
	})
	if err != nil {
		return fmt.Errorf("failed to update changelist status: %w", err)
	}
	if res == 0 {
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
	err := d.queries.MarkFileReviewed(ctx, store.MarkFileReviewedParams{
		ClID:     clID,
		FilePath: filePath,
		Reviewer: reviewer,
		Reviewed: reviewed,
	})
	if err != nil {
		return fmt.Errorf("failed to mark file %s reviewed: %w", filePath, err)
	}
	return nil
}

// ListReviewedFiles returns a set of file paths marked as reviewed by a specific reviewer for a changelist.
func (d *DB) ListReviewedFiles(ctx context.Context, clID int64, reviewer string) (map[string]bool, error) {
	paths, err := d.queries.ListReviewedFiles(ctx, store.ListReviewedFilesParams{
		ClID:     clID,
		Reviewer: reviewer,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to list reviewed files for cl %d, reviewer %s: %w", clID, reviewer, err)
	}

	reviewed := make(map[string]bool, len(paths))
	for _, path := range paths {
		reviewed[path] = true
	}
	return reviewed, nil
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
	for _, reviewer := range reviewers {
		trimmed := strings.TrimSpace(reviewer)
		if trimmed == "" {
			continue
		}
		if err := d.queries.AddReviewer(ctx, store.AddReviewerParams{
			ClID:     clID,
			Reviewer: trimmed,
		}); err != nil {
			return fmt.Errorf("failed to add reviewer %s to cl %d: %w", trimmed, clID, err)
		}
	}
	return nil
}

// RemoveReviewer unassigns a reviewer from a changelist.
func (d *DB) RemoveReviewer(ctx context.Context, clID int64, reviewer string) error {
	trimmed := strings.TrimSpace(reviewer)
	if err := d.queries.RemoveReviewer(ctx, store.RemoveReviewerParams{
		ClID:     clID,
		Reviewer: trimmed,
	}); err != nil {
		return fmt.Errorf("failed to remove reviewer %s from cl %d: %w", reviewer, clID, err)
	}
	return nil
}

// ListReviewers returns all reviewers assigned to a changelist along with any verdicts.
// Reviewers who submitted a verdict are included even if not explicitly pre-assigned.
func (d *DB) ListReviewers(ctx context.Context, clID int64) ([]ReviewerAssignment, error) {
	rows, err := d.queries.ListReviewers(ctx, clID)
	if err != nil {
		return nil, fmt.Errorf("failed to list reviewers for cl %d: %w", clID, err)
	}

	reviewers := make([]ReviewerAssignment, len(rows))
	for i, r := range rows {
		reviewers[i] = ReviewerAssignment{
			CLID:      r.ClID,
			Reviewer:  r.Reviewer,
			Verdict:   r.Verdict,
			UpdatedAt: r.UpdatedAt.Time,
		}
	}
	return reviewers, nil
}

// CountUnresolvedComments returns the number of published, unresolved comments on a changelist.
func (d *DB) CountUnresolvedComments(ctx context.Context, clID int64) (int, error) {
	count, err := d.queries.CountUnresolvedComments(ctx, clID)
	if err != nil {
		return 0, fmt.Errorf("failed to count unresolved comments for cl %d: %w", clID, err)
	}
	return int(count), nil
}

// GetAssignedCLIDsForUser returns a set of changelist IDs where the user or any of their groups is assigned as a reviewer.
func (d *DB) GetAssignedCLIDsForUser(ctx context.Context, username string, groupNames []string) (map[int64]bool, error) {
	targets := []string{username}
	for _, groupName := range groupNames {
		groupName = strings.TrimSpace(groupName)
		if groupName != "" {
			if !strings.HasPrefix(groupName, "@") {
				targets = append(targets, "@"+groupName)
			}
			targets = append(targets, groupName)
		}
	}

	rows, err := d.queries.GetAssignedCLIDsForUser(ctx, targets)
	if err != nil {
		return nil, fmt.Errorf("failed to query assigned CLs: %w", err)
	}

	clIDs := make(map[int64]bool, len(rows))
	for _, id := range rows {
		clIDs[id] = true
	}
	return clIDs, nil
}

// CountApprovals returns the number of reviewers who have granted an approval ("lgtm" or "approved") on a changelist.
func (d *DB) CountApprovals(ctx context.Context, clID int64) (int, error) {
	count, err := d.queries.CountApprovals(ctx, clID)
	if err != nil {
		return 0, fmt.Errorf("failed to count approvals for cl %d: %w", clID, err)
	}
	return int(count), nil
}

// GetApprovedReviewers returns the usernames of all reviewers who approved the changelist ("lgtm" or "approved").
func (d *DB) GetApprovedReviewers(ctx context.Context, clID int64) ([]string, error) {
	reviewers, err := d.queries.GetApprovedReviewers(ctx, clID)
	if err != nil {
		return nil, fmt.Errorf("failed to query approved reviewers for CL %d: %w", clID, err)
	}
	return reviewers, nil
}
