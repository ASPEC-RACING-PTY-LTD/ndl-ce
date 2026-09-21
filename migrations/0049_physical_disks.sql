-- Physical disk passthrough assignments. These are host devices owned by a
-- workload, not NDL storage volumes. Deleting a workload releases the row
-- and never wipes the disk.

CREATE TABLE IF NOT EXISTS physical_disk_assignments (
    id uuid PRIMARY KEY,
    cluster_id uuid NOT NULL REFERENCES clusters (id) ON DELETE CASCADE,
    workload_id uuid NOT NULL REFERENCES workloads (id) ON DELETE CASCADE,
    device_id text NOT NULL,
    by_id_path text NOT NULL,
    kernel_name text NOT NULL DEFAULT '',
    model text NOT NULL DEFAULT '',
    serial text NOT NULL DEFAULT '',
    size_bytes bigint NOT NULL DEFAULT 0,
    role text NOT NULL DEFAULT 'data',
    slot integer NOT NULL DEFAULT 0,
    bus text NOT NULL DEFAULT 'ahci',
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX IF NOT EXISTS physical_disk_assignments_device
    ON physical_disk_assignments (cluster_id, device_id);

CREATE INDEX IF NOT EXISTS physical_disk_assignments_workload
    ON physical_disk_assignments (cluster_id, workload_id);
