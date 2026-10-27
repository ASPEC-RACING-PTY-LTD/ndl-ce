-- Edge forwarding: one settings document per cluster (edge nodes, the
-- tunnels between edge and origin nodes with their public keys, and the
-- forwarded services).
CREATE TABLE IF NOT EXISTS edge_config (
    cluster_id uuid PRIMARY KEY REFERENCES clusters (id) ON DELETE CASCADE,
    body jsonb NOT NULL DEFAULT '{}'::jsonb,
    updated_by text NOT NULL DEFAULT '',
    updated_at timestamptz NOT NULL DEFAULT now()
);
