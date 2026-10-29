CREATE TABLE IF NOT EXISTS blobs (
    sha256 BYTEA PRIMARY KEY,
    byte_size BIGINT NOT NULL,
    storage_path TEXT NOT NULL, -- Portable relative sharded key (e.g. ab/cd/<sha256>)
    created_at TIMESTAMPTZ DEFAULT now()
);

CREATE TABLE IF NOT EXISTS trees (
    tree_hash BYTEA PRIMARY KEY,
    manifest BYTEA NOT NULL,
    entry_count INT NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ DEFAULT now()
);

CREATE TABLE IF NOT EXISTS changelists (
    cl_id BIGSERIAL PRIMARY KEY,
    author TEXT NOT NULL,
    description TEXT NOT NULL,
    root_tree_hash BYTEA NOT NULL,
    parent_cl BIGINT REFERENCES changelists(cl_id),
    status TEXT NOT NULL CHECK (status IN ('pending', 'shelved', 'submitted')),
    committed_at TIMESTAMPTZ DEFAULT now()
);

CREATE TABLE IF NOT EXISTS workspaces (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name TEXT NOT NULL,
    owner TEXT NOT NULL,
    base_cl BIGINT REFERENCES changelists(cl_id),
    created_at TIMESTAMPTZ DEFAULT now()
);

-- Seed initial empty root CL if database is empty
INSERT INTO changelists (cl_id, author, description, root_tree_hash, status)
VALUES (1, 'system', 'Depot root initialized', '\xe3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855', 'submitted')
ON CONFLICT DO NOTHING;

SELECT setval(pg_get_serial_sequence('changelists', 'cl_id'), COALESCE((SELECT MAX(cl_id) FROM changelists), 1));
