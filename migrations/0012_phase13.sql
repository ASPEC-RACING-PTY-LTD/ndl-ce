CREATE TABLE groups (
    id uuid PRIMARY KEY,
    cluster_id uuid NOT NULL REFERENCES clusters (id),
    name text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (cluster_id, name)
);

CREATE TABLE group_members (
    group_id uuid NOT NULL REFERENCES groups (id) ON DELETE CASCADE,
    user_id uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    PRIMARY KEY (group_id, user_id)
);

CREATE TABLE group_role_bindings (
    group_id uuid NOT NULL REFERENCES groups (id) ON DELETE CASCADE,
    role_id uuid NOT NULL REFERENCES roles (id),
    PRIMARY KEY (group_id, role_id)
);

CREATE TABLE mfa_methods (
    id uuid PRIMARY KEY,
    cluster_id uuid NOT NULL REFERENCES clusters (id),
    user_id uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    kind text NOT NULL,
    enabled boolean NOT NULL DEFAULT false,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (user_id)
);

CREATE TABLE secrets.mfa_secrets (
    method_id uuid PRIMARY KEY REFERENCES mfa_methods (id) ON DELETE CASCADE,
    totp_secret text NOT NULL DEFAULT '',
    recovery_hashes text[] NOT NULL DEFAULT '{}',
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE mfa_challenges (
    id uuid PRIMARY KEY,
    cluster_id uuid NOT NULL REFERENCES clusters (id),
    user_id uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    token_hash text NOT NULL UNIQUE,
    expires_at timestamptz NOT NULL,
    consumed_at timestamptz
);

CREATE TABLE service_principals (
    id uuid PRIMARY KEY,
    cluster_id uuid NOT NULL REFERENCES clusters (id),
    user_id uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    name text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (cluster_id, name)
);

ALTER TABLE users ADD COLUMN IF NOT EXISTS kind text NOT NULL DEFAULT 'person';

ALTER TABLE api_tokens ADD COLUMN IF NOT EXISTS permissions text[] NOT NULL DEFAULT '{}';

CREATE TABLE volume_encryption (
    volume_id uuid PRIMARY KEY REFERENCES volumes (id) ON DELETE CASCADE,
    cluster_id uuid NOT NULL REFERENCES clusters (id),
    encrypted boolean NOT NULL DEFAULT false,
    encryption_kind text NOT NULL DEFAULT 'none'
);

CREATE INDEX audit_events_cluster_created ON audit_events (cluster_id, created_at DESC);
