CREATE TABLE IF NOT EXISTS workloads (
    id uuid PRIMARY KEY,
    cluster_id uuid NOT NULL REFERENCES clusters (id),
    node_id uuid NOT NULL REFERENCES nodes (id),
    owner_node_id uuid NOT NULL REFERENCES nodes (id),
    desired_node_id uuid NOT NULL REFERENCES nodes (id),
    name text NOT NULL,
    kind text NOT NULL,
    status text NOT NULL,
    reason text NOT NULL DEFAULT '',
    desired_power text NOT NULL DEFAULT 'running',
    image_pin text NOT NULL,
    image_verified boolean NOT NULL DEFAULT false,
    cpus integer NOT NULL DEFAULT 1,
    memory_bytes bigint NOT NULL DEFAULT 268435456,
    privileged boolean NOT NULL DEFAULT false,
    uid_map text NOT NULL DEFAULT 'u 0 100000 65536',
    gid_map text NOT NULL DEFAULT 'g 0 100000 65536',
    pid integer,
    unit_active boolean NOT NULL DEFAULT false,
    migrate_ready boolean NOT NULL DEFAULT false,
    migrate_blockers jsonb NOT NULL DEFAULT '[]'::jsonb,
    devices jsonb NOT NULL DEFAULT '[]'::jsonb,
    warnings jsonb NOT NULL DEFAULT '[]'::jsonb,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (cluster_id, name)
);

CREATE TABLE IF NOT EXISTS workload_disks (
    id uuid PRIMARY KEY,
    cluster_id uuid NOT NULL REFERENCES clusters (id),
    workload_id uuid NOT NULL REFERENCES workloads (id),
    volume_id uuid NOT NULL REFERENCES volumes (id),
    role text NOT NULL DEFAULT 'root',
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (workload_id, role)
);

CREATE TABLE IF NOT EXISTS workload_nics (
    id uuid PRIMARY KEY,
    cluster_id uuid NOT NULL REFERENCES clusters (id),
    workload_id uuid NOT NULL REFERENCES workloads (id),
    network_id uuid NOT NULL REFERENCES networks (id),
    mac text NOT NULL,
    ipv4 text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (workload_id, mac)
);

CREATE INDEX IF NOT EXISTS workloads_cluster ON workloads (cluster_id);
CREATE INDEX IF NOT EXISTS workload_disks_workload ON workload_disks (workload_id);
CREATE INDEX IF NOT EXISTS workload_nics_workload ON workload_nics (workload_id);
