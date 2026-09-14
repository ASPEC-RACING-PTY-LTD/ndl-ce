ALTER TABLE operations ADD COLUMN IF NOT EXISTS progress integer;
ALTER TABLE operations ADD COLUMN IF NOT EXISTS stage text;
ALTER TABLE operations ADD COLUMN IF NOT EXISTS message text;
ALTER TABLE operations ADD COLUMN IF NOT EXISTS updated_at timestamptz NOT NULL DEFAULT now();

CREATE TABLE IF NOT EXISTS hardware_inventory (
    node_id uuid PRIMARY KEY REFERENCES nodes (id),
    cluster_id uuid NOT NULL REFERENCES clusters (id),
    payload jsonb NOT NULL DEFAULT '{}'::jsonb,
    observed_at timestamptz NOT NULL,
    stale boolean NOT NULL DEFAULT false
);

CREATE TABLE IF NOT EXISTS node_observations (
    id uuid PRIMARY KEY,
    cluster_id uuid NOT NULL REFERENCES clusters (id),
    node_id uuid NOT NULL REFERENCES nodes (id),
    kind text NOT NULL,
    observed_at timestamptz NOT NULL,
    stale boolean NOT NULL DEFAULT false
);

CREATE INDEX IF NOT EXISTS events_cluster_created ON events (cluster_id, created_at DESC);
CREATE INDEX IF NOT EXISTS node_observations_node ON node_observations (node_id, observed_at DESC);
