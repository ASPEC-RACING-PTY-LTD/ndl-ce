-- Cloudflare Tunnel Manager. Cloudflare holds tunnels, routes and DNS; these
-- tables keep only what Cloudflare cannot: encrypted API tokens and what each
-- one can reach, routes switched off in No-dal (Cloudflare has no disabled
-- flag), which workload a route serves, and connectors No-dal deployed.

CREATE TABLE IF NOT EXISTS cloudflare_credentials (
    id uuid PRIMARY KEY,
    cluster_id uuid NOT NULL REFERENCES clusters (id),
    name text NOT NULL,
    account_hint text NOT NULL DEFAULT '',
    account_owned boolean NOT NULL DEFAULT false,
    token_last4 text NOT NULL DEFAULT '',
    status text NOT NULL DEFAULT 'unverified',
    status_reason text NOT NULL DEFAULT '',
    scope jsonb NOT NULL DEFAULT '[]'::jsonb,
    expires_at timestamptz,
    last_verified_at timestamptz,
    created_by text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (cluster_id, name)
);

-- The token is AES-256-GCM sealed with a key file only the control plane can
-- read, and bound to its credential ID.
CREATE TABLE IF NOT EXISTS secrets.cloudflare_tokens (
    credential_id uuid PRIMARY KEY REFERENCES cloudflare_credentials (id) ON DELETE CASCADE,
    cluster_id uuid NOT NULL,
    sealed text NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS cloudflare_routes (
    id uuid PRIMARY KEY,
    cluster_id uuid NOT NULL REFERENCES clusters (id),
    account_id text NOT NULL,
    tunnel_id text NOT NULL,
    hostname text NOT NULL,
    path text NOT NULL DEFAULT '',
    rule jsonb NOT NULL DEFAULT '{}'::jsonb,
    enabled boolean NOT NULL DEFAULT true,
    workload_id uuid REFERENCES workloads (id) ON DELETE SET NULL,
    origin_ip text NOT NULL DEFAULT '',
    updated_by text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (cluster_id, tunnel_id, hostname, path)
);

CREATE INDEX IF NOT EXISTS cloudflare_routes_workload ON cloudflare_routes (workload_id);

CREATE TABLE IF NOT EXISTS cloudflare_workload_prefs (
    workload_id uuid PRIMARY KEY REFERENCES workloads (id) ON DELETE CASCADE,
    cluster_id uuid NOT NULL REFERENCES clusters (id),
    account_id text NOT NULL DEFAULT '',
    tunnel_id text NOT NULL DEFAULT '',
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS cloudflare_connectors (
    id uuid PRIMARY KEY,
    cluster_id uuid NOT NULL REFERENCES clusters (id),
    account_id text NOT NULL,
    tunnel_id text NOT NULL,
    machine_id text NOT NULL,
    unit text NOT NULL,
    created_by text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (cluster_id, machine_id, unit)
);
