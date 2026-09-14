CREATE TABLE vm_templates (
    id uuid PRIMARY KEY,
    cluster_id uuid NOT NULL REFERENCES clusters (id),
    name text NOT NULL,
    source_workload_id uuid REFERENCES workloads (id) ON DELETE SET NULL,
    snapshot_id uuid,
    spec_json jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE usb_attachments (
    id uuid PRIMARY KEY,
    cluster_id uuid NOT NULL REFERENCES clusters (id),
    workload_id uuid NOT NULL REFERENCES workloads (id) ON DELETE CASCADE,
    address text NOT NULL,
    vendor text NOT NULL,
    product text NOT NULL,
    exclusive boolean NOT NULL DEFAULT true,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (cluster_id, address)
);
