-- Migration: 0004_draft_comments.sql
-- Add is_draft flag to cl_comments to support multi-draft review workflows before publication.

ALTER TABLE cl_comments ADD COLUMN IF NOT EXISTS is_draft BOOLEAN NOT NULL DEFAULT false;

CREATE INDEX IF NOT EXISTS idx_comments_cl_draft ON cl_comments(cl_id, author, is_draft);
