CREATE TABLE IF NOT EXISTS storage_pools (
    id uuid PRIMARY KEY,
    cluster_id uuid NOT NULL REFERENCES clusters (id),
    node_id uuid NOT NULL REFERENCES nodes (id),
    name text NOT NULL,
    backend_type text NOT NULL,
    status text NOT NULL,
    reason text NOT NULL DEFAULT '',
    root_path text NOT NULL,
    backing jsonb NOT NULL DEFAULT '{}'::jsonb,
    warnings jsonb NOT NULL DEFAULT '[]'::jsonb,
    warning_text jsonb NOT NULL DEFAULT '[]'::jsonb,
    capabilities jsonb NOT NULL DEFAULT '{}'::jsonb,
    usable_bytes bigint,
    allocated_bytes bigint,
    provisioned_bytes bigint,
    total_bytes bigint,
    adopted boolean NOT NULL DEFAULT false,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (cluster_id, name)
);

CREATE TABLE IF NOT EXISTS volumes (
    id uuid PRIMARY KEY,
    cluster_id uuid NOT NULL REFERENCES clusters (id),
    node_id uuid NOT NULL REFERENCES nodes (id),
    pool_id uuid NOT NULL REFERENCES storage_pools (id),
    class text NOT NULL,
    kind text NOT NULL,
    format text NOT NULL,
    size_bytes bigint NOT NULL,
    status text NOT NULL,
    backend_type text NOT NULL,
    backend_ref text NOT NULL,
    xattr_state text NOT NULL DEFAULT '',
    allocated_bytes bigint,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS library_items (
    id uuid PRIMARY KEY,
    cluster_id uuid NOT NULL REFERENCES clusters (id),
    node_id uuid NOT NULL REFERENCES nodes (id),
    pool_id uuid NOT NULL REFERENCES storage_pools (id),
    kind text NOT NULL,
    display_name text NOT NULL,
    backend_ref text NOT NULL,
    size_bytes bigint NOT NULL,
    checksum_sha256 text NOT NULL,
    status text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS volumes_pool ON volumes (pool_id);
CREATE INDEX IF NOT EXISTS library_items_pool ON library_items (pool_id);
CREATE UNIQUE INDEX IF NOT EXISTS library_items_checksum ON library_items (pool_id, checksum_sha256);
