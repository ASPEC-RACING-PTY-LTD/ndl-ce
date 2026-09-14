CREATE TABLE IF NOT EXISTS datastores (
    pool_id uuid PRIMARY KEY REFERENCES storage_pools (id) ON DELETE CASCADE,
    kind text NOT NULL,
    locator text NOT NULL,
    portal text NOT NULL DEFAULT '',
    iqn text NOT NULL DEFAULT ''
);

CREATE TABLE IF NOT EXISTS secrets.datastore_credentials (
    pool_id uuid PRIMARY KEY REFERENCES storage_pools (id) ON DELETE CASCADE,
    cluster_id uuid NOT NULL REFERENCES clusters (id),
    username text NOT NULL DEFAULT '',
    password text NOT NULL DEFAULT '',
    updated_at timestamptz NOT NULL DEFAULT now()
);
