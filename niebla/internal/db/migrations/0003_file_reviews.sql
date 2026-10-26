CREATE TABLE IF NOT EXISTS cl_file_reviews (
    cl_id BIGINT NOT NULL REFERENCES changelists(cl_id) ON DELETE CASCADE,
    file_path TEXT NOT NULL,
    reviewer TEXT NOT NULL,
    reviewed BOOLEAN NOT NULL DEFAULT true,
    updated_at TIMESTAMPTZ DEFAULT now(),
    PRIMARY KEY (cl_id, file_path, reviewer)
);

CREATE INDEX IF NOT EXISTS idx_file_reviews_cl_reviewer ON cl_file_reviews(cl_id, reviewer);
