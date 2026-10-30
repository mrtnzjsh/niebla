-- Migration 0007: Structured AI & Reviewer Code Suggestions on Comments

ALTER TABLE cl_comments
    ADD COLUMN IF NOT EXISTS has_suggestion BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN IF NOT EXISTS suggestion_start_line INT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS suggestion_end_line INT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS suggestion_code TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS suggestion_applied_ps INT NOT NULL DEFAULT 0;

