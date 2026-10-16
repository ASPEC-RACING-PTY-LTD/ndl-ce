-- Uncommitted Git work: by default Smart backups ignore it. When the Owner
-- turns that off, a workload whose repositories have uncommitted or unpushed
-- changes is held (not backed up) until the work is committed and pushed,
-- the Owner lets the agent do it, or ignoring is turned back on.
ALTER TABLE backup_workspace_settings ADD COLUMN IF NOT EXISTS hold_uncommitted_git boolean NOT NULL DEFAULT false;
CREATE TABLE IF NOT EXISTS backup_git_holds (
    cluster_id uuid NOT NULL REFERENCES clusters (id) ON DELETE CASCADE,
    workload_id text NOT NULL,
    policy_id text NOT NULL DEFAULT '',
    target_id text NOT NULL DEFAULT '',
    repos_json text NOT NULL DEFAULT '[]',
    fingerprint text NOT NULL DEFAULT '',
    last_action text NOT NULL DEFAULT '',
    last_error text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (cluster_id, workload_id)
);
