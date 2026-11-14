-- name: ListApprovals :many
SELECT cl_id, reviewer, verdict, updated_at
FROM cl_approvals
WHERE cl_id = $1
ORDER BY updated_at ASC;

-- name: SetApproval :exec
INSERT INTO cl_approvals (cl_id, reviewer, verdict, updated_at)
VALUES ($1, $2, $3, now())
ON CONFLICT (cl_id, reviewer) DO UPDATE
SET verdict = EXCLUDED.verdict, updated_at = now();

-- name: MarkFileReviewed :exec
INSERT INTO cl_file_reviews (cl_id, file_path, reviewer, reviewed, updated_at)
VALUES ($1, $2, $3, $4, now())
ON CONFLICT (cl_id, file_path, reviewer) DO UPDATE
SET reviewed = EXCLUDED.reviewed, updated_at = now();

-- name: ListReviewedFiles :many
SELECT file_path
FROM cl_file_reviews
WHERE cl_id = $1 AND reviewer = $2 AND reviewed = true;

-- name: AddReviewer :exec
INSERT INTO cl_reviewers (cl_id, reviewer, created_at)
VALUES ($1, $2, now())
ON CONFLICT (cl_id, reviewer) DO NOTHING;

-- name: RemoveReviewer :exec
DELETE FROM cl_reviewers
WHERE cl_id = $1 AND reviewer = $2;

-- name: ListReviewers :many
SELECT COALESCE(r.cl_id, a.cl_id)::bigint as cl_id,
       COALESCE(r.reviewer, a.reviewer)::text as reviewer,
       COALESCE(a.verdict, 'pending')::text as verdict,
       COALESCE(a.updated_at, r.created_at)::timestamptz as updated_at
FROM cl_reviewers r
FULL OUTER JOIN cl_approvals a ON r.cl_id = a.cl_id AND r.reviewer = a.reviewer
WHERE COALESCE(r.cl_id, a.cl_id) = $1
ORDER BY COALESCE(r.reviewer, a.reviewer) ASC;

-- name: CountUnresolvedComments :one
SELECT COUNT(*)
FROM cl_comments
WHERE cl_id = $1 AND resolved = false AND is_draft = false;

-- name: GetAssignedCLIDsForUser :many
SELECT DISTINCT cl_id
FROM cl_reviewers
WHERE reviewer = ANY($1::text[]);

-- name: CountApprovals :one
SELECT COUNT(*)
FROM cl_approvals
WHERE cl_id = $1 AND verdict IN ('lgtm', 'approved') AND stale = false;

-- name: GetApprovedReviewers :many
SELECT reviewer
FROM cl_approvals
WHERE cl_id = $1 AND verdict IN ('lgtm', 'approved') AND stale = false
ORDER BY reviewer ASC;
