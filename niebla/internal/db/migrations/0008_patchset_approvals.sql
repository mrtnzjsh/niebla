-- Migration 0008: Patchset-Bound Approvals and Stale Invalidation

ALTER TABLE cl_approvals
    ADD COLUMN IF NOT EXISTS patchset_number INT NOT NULL DEFAULT 1,
    ADD COLUMN IF NOT EXISTS stale BOOLEAN NOT NULL DEFAULT false;

CREATE INDEX IF NOT EXISTS idx_approvals_cl_ps ON cl_approvals(cl_id, patchset_number, stale);

