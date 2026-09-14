CREATE TABLE update_operations (
    id uuid PRIMARY KEY,
    cluster_id uuid NOT NULL REFERENCES clusters (id),
    action text NOT NULL,
    status text NOT NULL,
    dry_run boolean NOT NULL DEFAULT false,
    error text NOT NULL DEFAULT '',
    version text NOT NULL DEFAULT '',
    packages text[] NOT NULL DEFAULT '{}',
    started_at timestamptz NOT NULL DEFAULT now(),
    finished_at timestamptz
);

CREATE INDEX update_operations_cluster ON update_operations (cluster_id, started_at DESC);
