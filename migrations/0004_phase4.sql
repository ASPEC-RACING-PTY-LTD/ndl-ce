CREATE TABLE IF NOT EXISTS networks (
    id uuid PRIMARY KEY,
    cluster_id uuid NOT NULL REFERENCES clusters (id),
    node_id uuid NOT NULL REFERENCES nodes (id),
    name text NOT NULL,
    kind text NOT NULL,
    status text NOT NULL,
    reason text NOT NULL DEFAULT '',
    danger text NOT NULL DEFAULT 'safe',
    bridge_name text NOT NULL,
    uplink_ifname text NOT NULL DEFAULT '',
    ipv4_cidr text NOT NULL DEFAULT '',
    gateway text NOT NULL DEFAULT '',
    dhcp boolean NOT NULL DEFAULT false,
    dns boolean NOT NULL DEFAULT false,
    nat boolean NOT NULL DEFAULT false,
    persist_kind text NOT NULL DEFAULT 'systemd-networkd',
    warnings jsonb NOT NULL DEFAULT '[]'::jsonb,
    management_ifindex integer,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (cluster_id, name)
);

CREATE TABLE IF NOT EXISTS addresses (
    id uuid PRIMARY KEY,
    cluster_id uuid NOT NULL REFERENCES clusters (id),
    network_id uuid NOT NULL REFERENCES networks (id),
    family text NOT NULL,
    cidr text NOT NULL,
    role text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS dhcp_reservations (
    id uuid PRIMARY KEY,
    cluster_id uuid NOT NULL REFERENCES clusters (id),
    network_id uuid NOT NULL REFERENCES networks (id),
    mac text NOT NULL,
    ipv4 text NOT NULL,
    hostname text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (network_id, mac),
    UNIQUE (network_id, ipv4)
);

CREATE INDEX IF NOT EXISTS addresses_network ON addresses (network_id);
CREATE INDEX IF NOT EXISTS dhcp_reservations_network ON dhcp_reservations (network_id);
