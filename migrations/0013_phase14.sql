CREATE TABLE gpu_assignments (
    id uuid PRIMARY KEY,
    cluster_id uuid NOT NULL REFERENCES clusters (id),
    gpu_id text NOT NULL,
    workload_id uuid REFERENCES workloads (id) ON DELETE SET NULL,
    mode text NOT NULL,
    exclusive boolean NOT NULL DEFAULT false,
    iommu_group text NOT NULL DEFAULT '',
    pci_devices text[] NOT NULL DEFAULT '{}',
    device_nodes text[] NOT NULL DEFAULT '{}',
    status text NOT NULL,
    reason text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (cluster_id, gpu_id, workload_id)
);

CREATE INDEX gpu_assignments_cluster_gpu ON gpu_assignments (cluster_id, gpu_id);
