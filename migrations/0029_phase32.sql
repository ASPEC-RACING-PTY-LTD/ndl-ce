CREATE TABLE IF NOT EXISTS migrate_jobs (
    id uuid PRIMARY KEY,
    cluster_id uuid NOT NULL REFERENCES clusters (id),
    workload_id uuid NOT NULL REFERENCES workloads (id) ON DELETE CASCADE,
    operation_id uuid,
    source_node_id uuid NOT NULL REFERENCES nodes (id),
    dest_node_id uuid NOT NULL REFERENCES nodes (id),
    mode text NOT NULL,
    state text NOT NULL,
    epoch_at_start integer NOT NULL DEFAULT 0,
    source_running boolean NOT NULL DEFAULT true,
    dest_running boolean NOT NULL DEFAULT false,
    reason text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

ALTER TABLE workloads ADD COLUMN IF NOT EXISTS ownership_epoch integer NOT NULL DEFAULT 0;

CREATE INDEX IF NOT EXISTS migrate_jobs_cluster ON migrate_jobs (cluster_id, created_at DESC);
