-- Helix integration: one settings document per cluster (credential hashes,
-- machine grants, approvals, the kill switch) and the event feed Helix
-- reads with a cursor.
CREATE TABLE IF NOT EXISTS helix_config (
    cluster_id uuid PRIMARY KEY REFERENCES clusters (id) ON DELETE CASCADE,
    body jsonb NOT NULL DEFAULT '{}'::jsonb,
    updated_by text NOT NULL DEFAULT '',
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS helix_events (
    seq bigserial PRIMARY KEY,
    cluster_id uuid NOT NULL REFERENCES clusters (id) ON DELETE CASCADE,
    id uuid NOT NULL,
    type text NOT NULL,
    severity text NOT NULL DEFAULT 'info',
    machine_id text NOT NULL DEFAULT '',
    body jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS helix_events_cluster_seq ON helix_events (cluster_id, seq);
