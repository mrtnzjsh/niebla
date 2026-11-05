-- name: InsertBlob :exec
INSERT INTO blobs (sha256, byte_size, storage_path)
VALUES ($1, $2, $3)
ON CONFLICT (sha256) DO NOTHING;

-- name: GetBlob :one
SELECT sha256, byte_size, storage_path, created_at
FROM blobs
WHERE sha256 = $1;

-- name: InsertTree :exec
INSERT INTO trees (tree_hash, manifest, entry_count)
VALUES ($1, $2, $3)
ON CONFLICT (tree_hash) DO NOTHING;

-- name: GetTreeManifest :one
SELECT manifest
FROM trees
WHERE tree_hash = $1;

