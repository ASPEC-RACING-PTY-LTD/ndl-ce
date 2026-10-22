-- How new nodes reach this control node to join: the URL they post to and
-- an optional Cloudflare Access service token. The token secret is sealed
-- with the control node's key; only the client id is stored in the clear.
CREATE TABLE IF NOT EXISTS cluster_join_settings (
    cluster_id uuid PRIMARY KEY REFERENCES clusters (id) ON DELETE CASCADE,
    join_url text NOT NULL DEFAULT '',
    access_client_id text NOT NULL DEFAULT '',
    access_secret_sealed text NOT NULL DEFAULT '',
    updated_by text NOT NULL DEFAULT '',
    updated_at timestamptz NOT NULL DEFAULT now()
);
