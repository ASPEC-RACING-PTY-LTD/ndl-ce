CREATE TABLE IF NOT EXISTS node_groups (
    id uuid PRIMARY KEY,
    cluster_id uuid NOT NULL REFERENCES clusters (id),
    name text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (cluster_id, name)
);

CREATE TABLE IF NOT EXISTS node_group_members (
    group_id uuid NOT NULL REFERENCES node_groups (id) ON DELETE CASCADE,
    node_id uuid NOT NULL REFERENCES nodes (id) ON DELETE CASCADE,
    PRIMARY KEY (group_id, node_id)
);

CREATE TABLE IF NOT EXISTS node_maintenance (
    node_id uuid PRIMARY KEY REFERENCES nodes (id) ON DELETE CASCADE,
    cluster_id uuid NOT NULL REFERENCES clusters (id),
    since timestamptz NOT NULL DEFAULT now(),
    reason text NOT NULL DEFAULT ''
);

CREATE TABLE IF NOT EXISTS workload_placements (
    workload_id uuid PRIMARY KEY REFERENCES workloads (id) ON DELETE CASCADE,
    cluster_id uuid NOT NULL REFERENCES clusters (id),
    mode text NOT NULL DEFAULT 'automatic',
    node_group_id uuid REFERENCES node_groups (id),
    require_gpu boolean NOT NULL DEFAULT false,
    require_storage_class text NOT NULL DEFAULT '',
    affinity_workload_id uuid,
    anti_affinity_workload_id uuid,
    priority integer NOT NULL DEFAULT 0
);
