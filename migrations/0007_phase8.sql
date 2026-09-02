ALTER TABLE workloads
    ADD COLUMN IF NOT EXISTS spec_json jsonb NOT NULL DEFAULT '{}'::jsonb,
    ADD COLUMN IF NOT EXISTS applied_json jsonb NOT NULL DEFAULT '{}'::jsonb,
    ADD COLUMN IF NOT EXISTS autostart boolean NOT NULL DEFAULT false,
    ADD COLUMN IF NOT EXISTS pending_restart boolean NOT NULL DEFAULT false,
    ADD COLUMN IF NOT EXISTS firmware text NOT NULL DEFAULT 'bios';

ALTER TABLE workload_disks
    ADD COLUMN IF NOT EXISTS slot integer NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS bus_addr text NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS read_only boolean NOT NULL DEFAULT false,
    ADD COLUMN IF NOT EXISTS format text NOT NULL DEFAULT '';

ALTER TABLE workload_nics
    ADD COLUMN IF NOT EXISTS pci_addr text NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS model text NOT NULL DEFAULT 'virtio';

ALTER TABLE workload_disks DROP CONSTRAINT IF EXISTS workload_disks_workload_id_role_key;
ALTER TABLE workload_disks DROP CONSTRAINT IF EXISTS workload_disks_workload_role_slot;
ALTER TABLE workload_disks ADD CONSTRAINT workload_disks_workload_role_slot UNIQUE (workload_id, role, slot);

CREATE TABLE IF NOT EXISTS vm_cidata (
    workload_id uuid PRIMARY KEY REFERENCES workloads (id) ON DELETE CASCADE,
    cluster_id uuid NOT NULL REFERENCES clusters (id),
    user_data_sha text NOT NULL DEFAULT '',
    has_password boolean NOT NULL DEFAULT false,
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS vm_firmware (
    workload_id uuid PRIMARY KEY REFERENCES workloads (id) ON DELETE CASCADE,
    cluster_id uuid NOT NULL REFERENCES clusters (id),
    mode text NOT NULL DEFAULT 'bios',
    vars_ref text NOT NULL DEFAULT '',
    updated_at timestamptz NOT NULL DEFAULT now()
);
