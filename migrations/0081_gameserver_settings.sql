-- Game server settings shared by the cluster. The CurseForge API key is
-- sealed with the control node's key and only handed to installs and
-- catalogue requests.
CREATE TABLE IF NOT EXISTS gameserver_settings (
    cluster_id uuid PRIMARY KEY REFERENCES clusters (id) ON DELETE CASCADE,
    curseforge_key_sealed text NOT NULL DEFAULT '',
    updated_by text NOT NULL DEFAULT '',
    updated_at timestamptz NOT NULL DEFAULT now()
);
