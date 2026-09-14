-- Phase 33 cluster restore and disaster-recovery catalog.
-- Locality is local path, object key, or a pull URL without credentials.

ALTER TABLE backup_artifacts
    ADD COLUMN IF NOT EXISTS locality text NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS pull_url text NOT NULL DEFAULT '';
