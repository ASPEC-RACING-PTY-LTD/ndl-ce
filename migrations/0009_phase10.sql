CREATE TABLE snapshots (
    id uuid PRIMARY KEY,
    cluster_id uuid NOT NULL REFERENCES clusters (id),
    workload_id uuid NOT NULL REFERENCES workloads (id) ON DELETE CASCADE,
    volume_id uuid NOT NULL REFERENCES volumes (id),
    name text NOT NULL,
    purpose_tag text NOT NULL,
    mechanism text NOT NULL,
    backend_ref text NOT NULL,
    parent_id uuid,
    chain_depth integer NOT NULL DEFAULT 0,
    status text NOT NULL DEFAULT 'available',
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX snapshots_workload ON snapshots (cluster_id, workload_id, created_at);
