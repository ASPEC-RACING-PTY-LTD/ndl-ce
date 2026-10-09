-- Protected backups are never removed by retention.
CREATE TABLE IF NOT EXISTS backup_artifact_flags (
    artifact_id uuid PRIMARY KEY REFERENCES backup_artifacts (id) ON DELETE CASCADE,
    cluster_id uuid NOT NULL REFERENCES clusters (id) ON DELETE CASCADE,
    protected boolean NOT NULL DEFAULT false,
    updated_at timestamptz NOT NULL DEFAULT now()
);

-- Offsite retention: how many restore points keep their remote copy. Zero
-- for all three means the remote copy follows local retention.
CREATE TABLE IF NOT EXISTS backup_policy_offsite (
    policy_id uuid PRIMARY KEY REFERENCES backup_policies (id) ON DELETE CASCADE,
    cluster_id uuid NOT NULL REFERENCES clusters (id) ON DELETE CASCADE,
    keep_daily integer NOT NULL DEFAULT 0,
    keep_weekly integer NOT NULL DEFAULT 0,
    keep_monthly integer NOT NULL DEFAULT 0
);
