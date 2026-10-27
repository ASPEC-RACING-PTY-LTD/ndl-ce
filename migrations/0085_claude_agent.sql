-- Claude Agent: one settings document per cluster (the agent's container,
-- its Remote Control settings, the scoped API token hash and the grants:
-- which game server folders it may read or write, and whether it may read
-- logs, run console commands or restart each server).
CREATE TABLE IF NOT EXISTS claude_agent_config (
    cluster_id uuid PRIMARY KEY REFERENCES clusters (id) ON DELETE CASCADE,
    body jsonb NOT NULL DEFAULT '{}'::jsonb,
    updated_by text NOT NULL DEFAULT '',
    updated_at timestamptz NOT NULL DEFAULT now()
);
