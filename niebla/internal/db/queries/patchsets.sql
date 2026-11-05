-- name: GetNextPatchsetNumber :one
SELECT COALESCE(MAX(patchset_number), 0) + 1
FROM cl_patchsets
WHERE cl_id = $1;

-- name: UpsertPatchset :exec
INSERT INTO cl_patchsets (cl_id, patchset_number, root_tree_hash, is_auto_rebase)
VALUES ($1, $2, $3, $4)
ON CONFLICT (cl_id, patchset_number) DO UPDATE
SET root_tree_hash = EXCLUDED.root_tree_hash, is_auto_rebase = EXCLUDED.is_auto_rebase;

-- name: SeedPatchsetOne :one
INSERT INTO cl_patchsets (cl_id, patchset_number, root_tree_hash, is_auto_rebase)
VALUES ($1, 1, $2, false)
ON CONFLICT (cl_id, patchset_number) DO NOTHING
RETURNING patchset_id, created_at;

-- name: GetLatestPatchset :one
SELECT patchset_id, cl_id, patchset_number, root_tree_hash, created_at, is_auto_rebase
FROM cl_patchsets
WHERE cl_id = $1
ORDER BY patchset_number DESC
LIMIT 1;

-- name: GetPatchsetByNumber :one
SELECT patchset_id, cl_id, patchset_number, root_tree_hash, created_at, is_auto_rebase
FROM cl_patchsets
WHERE cl_id = $1 AND patchset_number = $2;

-- name: ListPatchsets :many
SELECT patchset_id, cl_id, patchset_number, root_tree_hash, created_at, is_auto_rebase
FROM cl_patchsets
WHERE cl_id = $1
ORDER BY patchset_number ASC;

-- name: GetLatestPatchsetNumberForCL :one
SELECT COALESCE(MAX(patchset_number), 1)::int
FROM cl_patchsets
WHERE cl_id = $1;
