ALTER TABLE nodes ADD COLUMN IF NOT EXISTS role text NOT NULL DEFAULT 'control';
ALTER TABLE nodes ADD COLUMN IF NOT EXISTS hostname text NOT NULL DEFAULT '';
ALTER TABLE nodes ADD COLUMN IF NOT EXISTS revoked_at timestamptz;

CREATE TABLE IF NOT EXISTS join_tokens (
    id uuid PRIMARY KEY,
    cluster_id uuid NOT NULL REFERENCES clusters (id),
    token_hash text NOT NULL UNIQUE,
    expires_at timestamptz NOT NULL,
    consumed_at timestamptz,
    consumed_node_id uuid REFERENCES nodes (id),
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS cluster_leases (
    cluster_id uuid PRIMARY KEY REFERENCES clusters (id),
    holder_id text NOT NULL,
    expires_at timestamptz NOT NULL
);
