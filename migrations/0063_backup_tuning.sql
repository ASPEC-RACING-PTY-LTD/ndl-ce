-- Backup tuning: the zstd level for new chunks (0 = default, -1 = off) and a
-- pause switch for scheduled policy runs.
ALTER TABLE backup_workspace_settings ADD COLUMN IF NOT EXISTS compression_level integer NOT NULL DEFAULT 0;
ALTER TABLE backup_policies ADD COLUMN IF NOT EXISTS paused boolean NOT NULL DEFAULT false;
