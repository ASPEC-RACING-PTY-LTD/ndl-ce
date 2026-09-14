CREATE TABLE IF NOT EXISTS zfs_pools (
    pool_id uuid PRIMARY KEY REFERENCES storage_pools (id) ON DELETE CASCADE,
    zpool_guid text NOT NULL,
    zpool_name text NOT NULL,
    UNIQUE (zpool_guid)
);

CREATE TABLE IF NOT EXISTS zfs_datasets (
    volume_id uuid PRIMARY KEY REFERENCES volumes (id) ON DELETE CASCADE,
    dataset_guid text NOT NULL DEFAULT '',
    dataset_name text NOT NULL
);

CREATE INDEX zfs_pools_guid ON zfs_pools (zpool_guid);
