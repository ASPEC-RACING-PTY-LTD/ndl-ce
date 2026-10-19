-- Account profile: contact emails and the account-level IP allowlist. The
-- username stays the identity; users.display_name is only a UI label.
CREATE TABLE IF NOT EXISTS account_profiles (
    user_id uuid PRIMARY KEY REFERENCES users (id) ON DELETE CASCADE,
    cluster_id uuid NOT NULL REFERENCES clusters (id),
    email text NOT NULL DEFAULT '',
    email_secondary text NOT NULL DEFAULT '',
    email_verified_at timestamptz,
    ip_allowlist_on boolean NOT NULL DEFAULT false,
    ip_allowlist jsonb NOT NULL DEFAULT '[]'::jsonb,
    updated_at timestamptz NOT NULL DEFAULT now()
);

-- Profile pictures, kept small (the API caps uploads at 2 MB).
CREATE TABLE IF NOT EXISTS user_avatars (
    user_id uuid PRIMARY KEY REFERENCES users (id) ON DELETE CASCADE,
    cluster_id uuid NOT NULL REFERENCES clusters (id),
    content_type text NOT NULL,
    data bytea NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT now()
);

-- Proxies whose client-address headers are believed. Detected proxies are
-- worked out at runtime; a detected row only records that it was turned off.
CREATE TABLE IF NOT EXISTS trusted_proxies (
    id text NOT NULL,
    cluster_id uuid NOT NULL REFERENCES clusters (id),
    cidr text NOT NULL DEFAULT '',
    kind text NOT NULL DEFAULT 'generic',
    note text NOT NULL DEFAULT '',
    detected boolean NOT NULL DEFAULT false,
    disabled boolean NOT NULL DEFAULT false,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (cluster_id, id)
);
