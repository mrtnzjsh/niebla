CREATE TABLE IF NOT EXISTS cl_patchsets (
    patchset_id SERIAL PRIMARY KEY,
    cl_id BIGINT NOT NULL REFERENCES changelists(cl_id) ON DELETE CASCADE,
    patchset_number INT NOT NULL,
    root_tree_hash BYTEA NOT NULL,
    created_at TIMESTAMPTZ DEFAULT now(),
    UNIQUE(cl_id, patchset_number)
);

CREATE TABLE IF NOT EXISTS cl_comments (
    id BIGSERIAL PRIMARY KEY,
    cl_id BIGINT NOT NULL REFERENCES changelists(cl_id) ON DELETE CASCADE,
    patchset_number INT NOT NULL DEFAULT 1,
    file_path TEXT NOT NULL,
    line_number INT NOT NULL,
    end_line_number INT NOT NULL DEFAULT 0,
    side TEXT NOT NULL CHECK (side IN ('base', 'modified')),
    author TEXT NOT NULL,
    body TEXT NOT NULL,
    resolved BOOLEAN NOT NULL DEFAULT false,
    parent_comment_id BIGINT REFERENCES cl_comments(id),
    created_at TIMESTAMPTZ DEFAULT now()
);
ALTER TABLE cl_comments ADD COLUMN IF NOT EXISTS end_line_number INT NOT NULL DEFAULT 0;
CREATE INDEX IF NOT EXISTS idx_comments_cl_file ON cl_comments(cl_id, file_path);

CREATE TABLE IF NOT EXISTS cl_approvals (
    cl_id BIGINT NOT NULL REFERENCES changelists(cl_id) ON DELETE CASCADE,
    reviewer TEXT NOT NULL,
    verdict TEXT NOT NULL CHECK (verdict IN ('approved', 'changes_requested', 'lgtm')),
    updated_at TIMESTAMPTZ DEFAULT now(),
    PRIMARY KEY (cl_id, reviewer)
);
