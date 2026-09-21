-- Backup Engine V2 hardening: cache retention is honored by the agent and
-- Full Machine is the default capture scope for new policies.

ALTER TABLE backup_workspace_settings
    ADD COLUMN IF NOT EXISTS cache_retention_hours integer NOT NULL DEFAULT 0;

ALTER TABLE backup_policies
    ALTER COLUMN capture_mode SET DEFAULT 'full';
