-- Backup runs record included vs skipped disks and the copy method used.

ALTER TABLE backup_runs
    ADD COLUMN IF NOT EXISTS plan_json text NOT NULL DEFAULT '';
