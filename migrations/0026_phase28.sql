CREATE TABLE IF NOT EXISTS wg_peers (
    id uuid PRIMARY KEY,
    cluster_id uuid NOT NULL REFERENCES clusters (id),
    node_id uuid,
    name text NOT NULL DEFAULT '',
    role text NOT NULL DEFAULT 'local',
    public_key text NOT NULL DEFAULT '',
    listen_port integer NOT NULL DEFAULT 51820,
    address_cidr text NOT NULL DEFAULT '',
    endpoint text NOT NULL DEFAULT '',
    allowed_ips text NOT NULL DEFAULT '',
    persistent_keepalive integer NOT NULL DEFAULT 25,
    iface_name text NOT NULL DEFAULT '',
    private_key_path text NOT NULL DEFAULT '',
    pairing_token_hash text NOT NULL DEFAULT '',
    last_handshake_unix bigint NOT NULL DEFAULT 0,
    status text NOT NULL DEFAULT 'unavailable',
    reason text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS remote_nodes (
    id uuid PRIMARY KEY,
    cluster_id uuid NOT NULL REFERENCES clusters (id),
    wg_peer_id uuid REFERENCES wg_peers (id) ON DELETE SET NULL,
    name text NOT NULL DEFAULT '',
    listen_addr text NOT NULL DEFAULT '',
    wg_public_key text NOT NULL DEFAULT '',
    status text NOT NULL DEFAULT 'NotReady',
    reason text NOT NULL DEFAULT '',
    last_seen_at timestamptz,
    last_handshake_unix bigint NOT NULL DEFAULT 0,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS remote_sessions (
    id uuid PRIMARY KEY,
    cluster_id uuid NOT NULL REFERENCES clusters (id),
    node_id uuid NOT NULL REFERENCES remote_nodes (id) ON DELETE CASCADE,
    listen_addr text NOT NULL DEFAULT '',
    wg_public_key text NOT NULL DEFAULT '',
    last_seen_at timestamptz NOT NULL DEFAULT now(),
    created_at timestamptz NOT NULL DEFAULT now()
);
