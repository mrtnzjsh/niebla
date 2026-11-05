-- name: CreateWorkspace :one
INSERT INTO workspaces (name, owner, base_cl)
VALUES ($1, $2, $3)
RETURNING id, name, owner, base_cl, created_at;

-- name: UpsertWorkspace :one
INSERT INTO workspaces (id, name, owner, base_cl, created_at)
VALUES ($1, $2, $3, $4, now())
ON CONFLICT (id) DO UPDATE
SET name = EXCLUDED.name,
    owner = EXCLUDED.owner,
    base_cl = EXCLUDED.base_cl
RETURNING id, name, owner, base_cl, created_at;

-- name: GetWorkspace :one
SELECT id, name, owner, base_cl, created_at
FROM workspaces
WHERE id = $1;
