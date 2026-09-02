-- Phase 21 OCI registries. Passwords live only in secrets.registry_secrets.

CREATE TABLE registries (
    id uuid PRIMARY KEY,
    cluster_id uuid NOT NULL REFERENCES clusters (id),
    name text NOT NULL,
    url text NOT NULL,
    insecure boolean NOT NULL DEFAULT false,
    has_credentials boolean NOT NULL DEFAULT false,
    status text NOT NULL DEFAULT 'configured',
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (cluster_id, name)
);

CREATE TABLE secrets.registry_secrets (
    registry_id uuid PRIMARY KEY REFERENCES registries (id) ON DELETE CASCADE,
    cluster_id uuid NOT NULL REFERENCES clusters (id),
    username text NOT NULL DEFAULT '',
    password text NOT NULL DEFAULT '',
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX registries_cluster ON registries (cluster_id, created_at DESC);
