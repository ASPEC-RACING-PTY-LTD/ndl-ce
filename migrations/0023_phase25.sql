CREATE TABLE IF NOT EXISTS lvm_vgs (
    pool_id uuid PRIMARY KEY REFERENCES storage_pools (id) ON DELETE CASCADE,
    vg_uuid text NOT NULL,
    vg_name text NOT NULL,
    thin_pool text NOT NULL DEFAULT 'thinpool',
    UNIQUE (vg_uuid)
);

CREATE TABLE IF NOT EXISTS lvm_lvs (
    volume_id uuid PRIMARY KEY REFERENCES volumes (id) ON DELETE CASCADE,
    lv_uuid text NOT NULL DEFAULT '',
    lv_name text NOT NULL
);

CREATE INDEX lvm_vgs_uuid ON lvm_vgs (vg_uuid);
