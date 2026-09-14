-- Phase 23 object-storage backup destinations.

ALTER TABLE backup_targets
    ADD COLUMN IF NOT EXISTS endpoint text NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS region text NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS bucket text NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS prefix text NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS no_check_bucket boolean NOT NULL DEFAULT false;

ALTER TABLE secrets.backup_credentials
    ADD COLUMN IF NOT EXISTS encryption_key text NOT NULL DEFAULT '';

ALTER TABLE backup_runs
    ADD COLUMN IF NOT EXISTS transferred_bytes bigint NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS incremental boolean NOT NULL DEFAULT false;

ALTER TABLE backup_artifacts
    ADD COLUMN IF NOT EXISTS encrypted boolean NOT NULL DEFAULT false,
    ADD COLUMN IF NOT EXISTS transferred_bytes bigint NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS parent_artifact_id uuid,
    ADD COLUMN IF NOT EXISTS object_key text NOT NULL DEFAULT '';
