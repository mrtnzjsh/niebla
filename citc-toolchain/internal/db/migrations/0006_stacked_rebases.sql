-- Migration: 0006_stacked_rebases.sql
-- Enables downstream changelist stack re-parenting and rebase status tracking

-- 1. Extend changelists table with rebase flags and base patchset tracking
ALTER TABLE changelists 
    ADD COLUMN IF NOT EXISTS needs_rebase BOOLEAN NOT NULL DEFAULT false,
    ADD COLUMN IF NOT EXISTS rebase_conflict_details TEXT,
    ADD COLUMN IF NOT EXISTS base_patchset_number INT NOT NULL DEFAULT 1;

-- 2. Index parent_cl for fast recursive child lookups among active shelved CLs
CREATE INDEX IF NOT EXISTS idx_changelists_parent_shelved 
    ON changelists(parent_cl) 
    WHERE status = 'shelved';

-- 3. Extend cl_patchsets to distinguish automated rebases from author-shelved revisions
ALTER TABLE cl_patchsets
    ADD COLUMN IF NOT EXISTS is_auto_rebase BOOLEAN NOT NULL DEFAULT false,
    ADD COLUMN IF NOT EXISTS parent_patchset_id INT REFERENCES cl_patchsets(patchset_id);

-- 4. Audit log table for stack rebase events
CREATE TABLE IF NOT EXISTS cl_rebase_events (
    event_id BIGSERIAL PRIMARY KEY,
    cl_id BIGINT NOT NULL REFERENCES changelists(cl_id) ON DELETE CASCADE,
    parent_cl BIGINT NOT NULL REFERENCES changelists(cl_id) ON DELETE CASCADE,
    trigger_patchset_number INT NOT NULL,
    resulting_patchset_number INT,
    status TEXT NOT NULL CHECK (status IN ('clean', 'conflict', 'failed')),
    conflict_summary TEXT,
    created_at TIMESTAMPTZ DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_rebase_events_cl ON cl_rebase_events(cl_id);
