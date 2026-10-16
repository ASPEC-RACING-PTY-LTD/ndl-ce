-- Platform backup: an encrypted copy of NDL-CE itself in a backup target,
-- recoverable with an Owner's password. state_json holds no secrets (the
-- Owner wraps it lists are published to the bucket anyway); the snapshot
-- key lives in the secrets schema.
CREATE TABLE IF NOT EXISTS platform_backup_state (
    cluster_id uuid PRIMARY KEY REFERENCES clusters (id) ON DELETE CASCADE,
    state_json text NOT NULL DEFAULT '{}',
    updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE IF NOT EXISTS secrets.platform_backup_keys (
    cluster_id uuid PRIMARY KEY REFERENCES clusters (id) ON DELETE CASCADE,
    keys_json text NOT NULL DEFAULT '{}',
    updated_at timestamptz NOT NULL DEFAULT now()
);
