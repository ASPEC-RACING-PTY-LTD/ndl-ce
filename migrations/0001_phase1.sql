CREATE TABLE IF NOT EXISTS schema_migrations (
    name text PRIMARY KEY,
    applied_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE clusters (
    id uuid PRIMARY KEY,
    name text NOT NULL DEFAULT 'local',
    created_at timestamptz NOT NULL DEFAULT now(),
    setup_completed_at timestamptz
);

CREATE UNIQUE INDEX clusters_singleton ON clusters ((true));

CREATE TABLE nodes (
    id uuid PRIMARY KEY,
    cluster_id uuid NOT NULL REFERENCES clusters (id),
    name text NOT NULL DEFAULT 'local',
    host_platform jsonb NOT NULL DEFAULT '{}'::jsonb,
    enrolled_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (cluster_id, name)
);

CREATE TABLE users (
    id uuid PRIMARY KEY,
    cluster_id uuid NOT NULL REFERENCES clusters (id),
    username text NOT NULL,
    password_hash text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (cluster_id, username)
);

CREATE TABLE roles (
    id uuid PRIMARY KEY,
    cluster_id uuid NOT NULL REFERENCES clusters (id),
    name text NOT NULL,
    permissions text[] NOT NULL,
    UNIQUE (cluster_id, name)
);

CREATE TABLE role_bindings (
    id uuid PRIMARY KEY,
    cluster_id uuid NOT NULL REFERENCES clusters (id),
    user_id uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    role_id uuid NOT NULL REFERENCES roles (id),
    UNIQUE (user_id, role_id)
);

CREATE TABLE sessions (
    id uuid PRIMARY KEY,
    cluster_id uuid NOT NULL REFERENCES clusters (id),
    user_id uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    token_hash text NOT NULL UNIQUE,
    created_at timestamptz NOT NULL DEFAULT now(),
    expires_at timestamptz NOT NULL,
    revoked_at timestamptz,
    aal integer NOT NULL DEFAULT 1
);

CREATE TABLE api_tokens (
    id uuid PRIMARY KEY,
    cluster_id uuid NOT NULL REFERENCES clusters (id),
    user_id uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    name text NOT NULL,
    token_hash text NOT NULL UNIQUE,
    prefix text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    revoked_at timestamptz
);

CREATE TABLE operations (
    id uuid PRIMARY KEY,
    cluster_id uuid NOT NULL REFERENCES clusters (id),
    node_id uuid REFERENCES nodes (id),
    kind text NOT NULL,
    state text NOT NULL,
    idempotency_key text,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX operations_idempotency ON operations (cluster_id, idempotency_key)
WHERE
    idempotency_key IS NOT NULL;

CREATE TABLE events (
    id uuid PRIMARY KEY,
    cluster_id uuid NOT NULL REFERENCES clusters (id),
    node_id uuid REFERENCES nodes (id),
    type text NOT NULL,
    payload jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE audit_events (
    id uuid PRIMARY KEY,
    cluster_id uuid REFERENCES clusters (id),
    actor_user_id uuid,
    action text NOT NULL,
    result text NOT NULL,
    remote_addr text,
    detail jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE SCHEMA IF NOT EXISTS secrets;

CREATE TABLE secrets.setup_tokens (
    cluster_id uuid PRIMARY KEY REFERENCES clusters (id),
    token_hash text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    consumed_at timestamptz
);

CREATE TABLE capabilities (
    id text PRIMARY KEY,
    enabled boolean NOT NULL DEFAULT false
);
