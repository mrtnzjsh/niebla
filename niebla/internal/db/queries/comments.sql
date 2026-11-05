-- name: GetComment :one
SELECT id, cl_id, patchset_number, file_path, line_number, end_line_number, side, author, body,
       resolved, parent_comment_id, created_at, is_draft,
       has_suggestion, suggestion_start_line, suggestion_end_line, suggestion_code, suggestion_applied_ps
FROM cl_comments
WHERE id = $1;

-- name: ListComments :many
SELECT id, cl_id, patchset_number, file_path, line_number, end_line_number, side, author, body,
       resolved, parent_comment_id, created_at, is_draft,
       has_suggestion, suggestion_start_line, suggestion_end_line, suggestion_code, suggestion_applied_ps
FROM cl_comments
WHERE cl_id = $1 AND file_path = $2
ORDER BY line_number ASC, end_line_number ASC, created_at ASC;

-- name: ListCommentsForLine :many
SELECT id, cl_id, patchset_number, file_path, line_number, end_line_number, side, author, body,
       resolved, parent_comment_id, created_at, is_draft,
       has_suggestion, suggestion_start_line, suggestion_end_line, suggestion_code, suggestion_applied_ps
FROM cl_comments
WHERE cl_id = $1 AND file_path = $2 AND (end_line_number = $3 OR (end_line_number <= 0 AND line_number = $3)) AND side = $4
ORDER BY created_at ASC;

-- name: ListAllComments :many
SELECT id, cl_id, patchset_number, file_path, line_number, end_line_number, side, author, body,
       resolved, parent_comment_id, created_at, is_draft,
       has_suggestion, suggestion_start_line, suggestion_end_line, suggestion_code, suggestion_applied_ps
FROM cl_comments
WHERE cl_id = $1
ORDER BY file_path ASC, line_number ASC, end_line_number ASC, created_at ASC;

-- name: InsertComment :one
INSERT INTO cl_comments (
    cl_id, patchset_number, file_path, line_number, end_line_number, side, author, body, parent_comment_id, is_draft,
    has_suggestion, suggestion_start_line, suggestion_end_line, suggestion_code
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)
RETURNING id, cl_id, patchset_number, file_path, line_number, end_line_number, side, author, body,
          resolved, parent_comment_id, created_at, is_draft,
          has_suggestion, suggestion_start_line, suggestion_end_line, suggestion_code, suggestion_applied_ps;

-- name: MarkSuggestionApplied :exec
UPDATE cl_comments
SET suggestion_applied_ps = $1, resolved = true
WHERE id = $2;

-- name: ResolveCommentThread :exec
UPDATE cl_comments
SET resolved = true
WHERE cl_id = $1 AND file_path = $2 AND (end_line_number = $3 OR (end_line_number <= 0 AND line_number = $3)) AND side = $4;

-- name: RejectCommentSuggestion :execrows
UPDATE cl_comments
SET suggestion_applied_ps = -1
WHERE id = $1;

-- name: ListDraftComments :many
SELECT id, cl_id, patchset_number, file_path, line_number, end_line_number, side, author, body,
       resolved, parent_comment_id, created_at, is_draft,
       has_suggestion, suggestion_start_line, suggestion_end_line, suggestion_code, suggestion_applied_ps
FROM cl_comments
WHERE cl_id = $1 AND author = $2 AND is_draft = true
ORDER BY file_path ASC, line_number ASC, created_at ASC;

-- name: CountDraftComments :one
SELECT COUNT(*)
FROM cl_comments
WHERE cl_id = $1 AND author = $2 AND is_draft = true;

-- name: PublishDraftComments :execrows
UPDATE cl_comments
SET is_draft = false
WHERE cl_id = $1 AND author = $2 AND is_draft = true;

-- name: DiscardDraftComment :execrows
DELETE FROM cl_comments
WHERE id = $1 AND author = $2 AND is_draft = true;

-- name: ResolveComment :execrows
UPDATE cl_comments
SET resolved = $1
WHERE id = $2;

