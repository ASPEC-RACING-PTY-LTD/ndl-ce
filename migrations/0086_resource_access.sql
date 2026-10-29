-- Resource access: grants of permissions on one item (a workload, game
-- server, Docker container or node) to a user or group, so people can be
-- given exactly that item without a role. One document per cluster.
CREATE TABLE IF NOT EXISTS resource_access (
    cluster_id uuid PRIMARY KEY REFERENCES clusters (id) ON DELETE CASCADE,
    body jsonb NOT NULL DEFAULT '{}'::jsonb,
    updated_by text NOT NULL DEFAULT '',
    updated_at timestamptz NOT NULL DEFAULT now()
);
