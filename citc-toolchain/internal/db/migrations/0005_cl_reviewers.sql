CREATE TABLE IF NOT EXISTS cl_reviewers (
    cl_id BIGINT NOT NULL REFERENCES changelists(cl_id) ON DELETE CASCADE,
    reviewer TEXT NOT NULL,
    created_at TIMESTAMPTZ DEFAULT now(),
    PRIMARY KEY (cl_id, reviewer)
);

CREATE INDEX IF NOT EXISTS idx_cl_reviewers_cl ON cl_reviewers(cl_id);
CREATE INDEX IF NOT EXISTS idx_cl_reviewers_user ON cl_reviewers(reviewer);
