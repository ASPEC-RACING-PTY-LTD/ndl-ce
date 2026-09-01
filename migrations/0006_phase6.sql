CREATE TABLE IF NOT EXISTS io_sessions (
    id uuid PRIMARY KEY,
    cluster_id uuid NOT NULL REFERENCES clusters (id),
    user_id uuid NOT NULL REFERENCES users (id),
    target_kind text NOT NULL,
    target_id uuid NOT NULL,
    kind text NOT NULL,
    cwd text NOT NULL DEFAULT '/',
    ticket_hash text NOT NULL,
    state text NOT NULL,
    reason text NOT NULL DEFAULT '',
    expires_at timestamptz NOT NULL,
    connected_at timestamptz,
    ended_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS io_sessions_cluster ON io_sessions (cluster_id, created_at DESC);
CREATE UNIQUE INDEX IF NOT EXISTS io_sessions_ticket ON io_sessions (ticket_hash);
