-- Docker Compose project deployments: per-project update commands, Git
-- update detection and deployment history. config and state are JSON; the
-- project's environment values and webhook secret live in the secrets
-- schema. Logs are files on the node, not rows.
CREATE TABLE IF NOT EXISTS deploy_projects (
    id uuid PRIMARY KEY,
    cluster_id uuid NOT NULL REFERENCES clusters (id) ON DELETE CASCADE,
    workload_id uuid NOT NULL,
    name text NOT NULL,
    working_dir text NOT NULL,
    config text NOT NULL DEFAULT '{}',
    state text NOT NULL DEFAULT '{}',
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (cluster_id, workload_id, working_dir)
);
CREATE TABLE IF NOT EXISTS deployments (
    id uuid PRIMARY KEY,
    cluster_id uuid NOT NULL REFERENCES clusters (id) ON DELETE CASCADE,
    project_id uuid NOT NULL REFERENCES deploy_projects (id) ON DELETE CASCADE,
    workload_id uuid NOT NULL,
    batch_id text NOT NULL DEFAULT '',
    status text NOT NULL,
    trigger text NOT NULL,
    actor text NOT NULL DEFAULT '',
    queued_at timestamptz NOT NULL,
    started_at timestamptz,
    finished_at timestamptz,
    detail text NOT NULL DEFAULT '{}'
);
CREATE INDEX IF NOT EXISTS deployments_project_idx ON deployments (cluster_id, project_id, queued_at DESC);
CREATE INDEX IF NOT EXISTS deployments_status_idx ON deployments (cluster_id, status);
CREATE TABLE IF NOT EXISTS deploy_settings (
    cluster_id uuid PRIMARY KEY REFERENCES clusters (id) ON DELETE CASCADE,
    body text NOT NULL DEFAULT '{}',
    updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE IF NOT EXISTS secrets.deploy_project_secrets (
    project_id uuid PRIMARY KEY REFERENCES deploy_projects (id) ON DELETE CASCADE,
    cluster_id uuid NOT NULL,
    env text NOT NULL DEFAULT '{}',
    webhook_secret text NOT NULL DEFAULT '',
    updated_at timestamptz NOT NULL DEFAULT now()
);
