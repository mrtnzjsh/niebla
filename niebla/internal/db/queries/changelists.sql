-- name: AcquireTrunkCommitLock :exec
SELECT pg_advisory_xact_lock($1);

-- name: GetHeadCL :one
SELECT cl_id, author, description, root_tree_hash, parent_cl, status, trunk_revision, committed_at,
       needs_rebase, rebase_conflict_details, base_patchset_number
FROM changelists
WHERE status = 'submitted'
ORDER BY trunk_revision DESC NULLS LAST, cl_id DESC
LIMIT 1;

-- name: GetHeadTreeHash :one
SELECT cl_id, root_tree_hash
FROM changelists
WHERE status = 'submitted'
ORDER BY trunk_revision DESC NULLS LAST, cl_id DESC
LIMIT 1;

-- name: GetCL :one
SELECT cl_id, author, description, root_tree_hash, parent_cl, status, trunk_revision, committed_at,
       needs_rebase, rebase_conflict_details, base_patchset_number
FROM changelists
WHERE cl_id = $1;

-- name: GetCLForUpdate :one
SELECT cl_id, author, description, root_tree_hash, parent_cl, status, trunk_revision, committed_at,
       needs_rebase, rebase_conflict_details, base_patchset_number
FROM changelists
WHERE cl_id = $1
FOR UPDATE;

-- name: CreateShelvedCL :one
INSERT INTO changelists (author, description, root_tree_hash, parent_cl, status, committed_at)
VALUES ($1, $2, $3, $4, 'shelved', now())
RETURNING cl_id;

-- name: SubmitCLUpdate :one
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

-- name: GetShelvedChildren :many
SELECT cl_id, author, description, root_tree_hash, parent_cl, status, trunk_revision, committed_at,
       needs_rebase, rebase_conflict_details, base_patchset_number
FROM changelists
WHERE parent_cl = $1 AND status = 'shelved'
ORDER BY cl_id ASC;

-- name: RecordRebaseEvent :exec
INSERT INTO cl_rebase_events (
    cl_id, parent_cl, trigger_patchset_number, resulting_patchset_number, status, conflict_summary
) VALUES ($1, $2, $3, $4, $5, $6);

-- name: ListCLs :many
SELECT cl_id, author, description, root_tree_hash, parent_cl, status, trunk_revision, committed_at,
       needs_rebase, rebase_conflict_details, base_patchset_number
FROM changelists
ORDER BY cl_id DESC
LIMIT $1;

-- name: UpdateCLStatus :execrows
UPDATE changelists
SET status = $1
WHERE cl_id = $2;

-- name: UpdateNeedsRebase :exec
UPDATE changelists
SET needs_rebase = $2,
    rebase_conflict_details = $3
WHERE cl_id = $1;

-- name: UpdateParentAndTree :exec
UPDATE changelists
SET parent_cl = $2,
    root_tree_hash = $3,
    needs_rebase = false,
    rebase_conflict_details = NULL
WHERE cl_id = $1;

-- name: UpdateCLRootTreeHash :exec
UPDATE changelists
SET root_tree_hash = $2
WHERE cl_id = $1;

-- name: UpdateCLBasePatchset :exec
UPDATE changelists
SET base_patchset_number = $2
WHERE cl_id = $1;

-- name: MarkCLRebaseConflict :exec
UPDATE changelists
SET needs_rebase = true,
    rebase_conflict_details = $2
WHERE cl_id = $1;

-- name: ClearCLRebaseConflict :exec
UPDATE changelists
SET needs_rebase = false,
    rebase_conflict_details = NULL
WHERE cl_id = $1;
