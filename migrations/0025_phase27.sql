CREATE TABLE IF NOT EXISTS network_vlans (
    id uuid PRIMARY KEY,
    cluster_id uuid NOT NULL REFERENCES clusters (id),
    network_id uuid REFERENCES networks (id) ON DELETE CASCADE,
    name text NOT NULL DEFAULT '',
    vid integer NOT NULL,
    parent_ifname text NOT NULL DEFAULT '',
    access_ifname text NOT NULL DEFAULT '',
    mode text NOT NULL DEFAULT 'access',
    locator text NOT NULL DEFAULT '',
    status text NOT NULL DEFAULT 'unavailable',
    reason text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS network_bonds (
    id uuid PRIMARY KEY,
    cluster_id uuid NOT NULL REFERENCES clusters (id),
    name text NOT NULL,
    mode text NOT NULL DEFAULT 'active-backup',
    members jsonb NOT NULL DEFAULT '[]'::jsonb,
    locator text NOT NULL DEFAULT '',
    status text NOT NULL DEFAULT 'unavailable',
    reason text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS network_policies (
    id uuid PRIMARY KEY,
    cluster_id uuid NOT NULL REFERENCES clusters (id),
    name text NOT NULL,
    action text NOT NULL DEFAULT 'deny',
    src_workload_id uuid,
    dst_workload_id uuid,
    src_mac text NOT NULL DEFAULT '',
    dst_mac text NOT NULL DEFAULT '',
    status text NOT NULL DEFAULT 'unavailable',
    reason text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS network_overlays (
    id uuid PRIMARY KEY,
    cluster_id uuid NOT NULL REFERENCES clusters (id),
    name text NOT NULL DEFAULT '',
    vni integer NOT NULL,
    locator text NOT NULL DEFAULT '',
    status text NOT NULL DEFAULT 'unavailable',
    reason text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now()
);
