-- Phase 24 backup verification and file restore.

ALTER TABLE backup_artifacts
    ADD COLUMN IF NOT EXISTS verify_status text NOT NULL DEFAULT 'unverified',
    ADD COLUMN IF NOT EXISTS verify_error text NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS last_tested_at timestamptz,
    ADD COLUMN IF NOT EXISTS throwaway_workload_id uuid;
