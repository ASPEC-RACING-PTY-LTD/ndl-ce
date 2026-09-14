CREATE TABLE IF NOT EXISTS game_servers (
    id uuid PRIMARY KEY,
    cluster_id uuid NOT NULL REFERENCES clusters (id) ON DELETE CASCADE,
    workload_id text NOT NULL DEFAULT '',
    node_id text NOT NULL DEFAULT '',
    owner_user_id text NOT NULL DEFAULT '',
    name text NOT NULL,
    notes text NOT NULL DEFAULT '',
    template_id text NOT NULL,
    template_name text NOT NULL DEFAULT '',
    game text NOT NULL DEFAULT '',
    implementation text NOT NULL DEFAULT '',
    family text NOT NULL DEFAULT '',
    status text NOT NULL DEFAULT 'pending',
    desired_power text NOT NULL DEFAULT 'stopped',
    image text NOT NULL DEFAULT '',
    image_label text NOT NULL DEFAULT '',
    startup text NOT NULL DEFAULT '',
    env_json text NOT NULL DEFAULT '{}',
    ports_json text NOT NULL DEFAULT '[]',
    cpus integer NOT NULL DEFAULT 1,
    memory_bytes bigint NOT NULL DEFAULT 0,
    disk_bytes bigint NOT NULL DEFAULT 0,
    capabilities text NOT NULL DEFAULT '[]',
    container_id text NOT NULL DEFAULT '',
    data_dir text NOT NULL DEFAULT '',
    install_phase text NOT NULL DEFAULT '',
    install_log text NOT NULL DEFAULT '',
    error_human text NOT NULL DEFAULT '',
    error_raw text NOT NULL DEFAULT '',
    pinned boolean NOT NULL DEFAULT false,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS game_servers_cluster ON game_servers (cluster_id, name);

CREATE TABLE IF NOT EXISTS game_templates (
    id text NOT NULL,
    cluster_id uuid NOT NULL REFERENCES clusters (id) ON DELETE CASCADE,
    body text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (cluster_id, id)
);

CREATE TABLE IF NOT EXISTS game_sources (
    id text NOT NULL,
    cluster_id uuid NOT NULL REFERENCES clusters (id) ON DELETE CASCADE,
    name text NOT NULL,
    url text NOT NULL,
    kind text NOT NULL DEFAULT 'url',
    enabled boolean NOT NULL DEFAULT true,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (cluster_id, id)
);

CREATE TABLE IF NOT EXISTS game_acl (
    server_id uuid NOT NULL REFERENCES game_servers (id) ON DELETE CASCADE,
    user_id text NOT NULL,
    grants text NOT NULL DEFAULT '[]',
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (server_id, user_id)
);

CREATE TABLE IF NOT EXISTS game_events (
    id uuid PRIMARY KEY,
    server_id text NOT NULL,
    cluster_id uuid NOT NULL REFERENCES clusters (id) ON DELETE CASCADE,
    user_id text NOT NULL DEFAULT '',
    kind text NOT NULL,
    summary text NOT NULL,
    detail text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS game_events_server ON game_events (cluster_id, server_id, created_at DESC);

CREATE TABLE IF NOT EXISTS game_schedules (
    id uuid PRIMARY KEY,
    server_id uuid NOT NULL REFERENCES game_servers (id) ON DELETE CASCADE,
    name text NOT NULL,
    action text NOT NULL,
    cron text NOT NULL,
    payload text NOT NULL DEFAULT '',
    enabled boolean NOT NULL DEFAULT true,
    last_run timestamptz,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS game_backups (
    id uuid PRIMARY KEY,
    server_id uuid NOT NULL REFERENCES game_servers (id) ON DELETE CASCADE,
    name text NOT NULL,
    reason text NOT NULL DEFAULT '',
    path text NOT NULL,
    bytes bigint NOT NULL DEFAULT 0,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS game_content (
    id text PRIMARY KEY,
    server_id uuid NOT NULL REFERENCES game_servers (id) ON DELETE CASCADE,
    body text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS game_content_profiles (
    id uuid PRIMARY KEY,
    server_id uuid NOT NULL REFERENCES game_servers (id) ON DELETE CASCADE,
    name text NOT NULL,
    items text NOT NULL DEFAULT '[]',
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS game_console_favs (
    id uuid PRIMARY KEY,
    server_id uuid NOT NULL REFERENCES game_servers (id) ON DELETE CASCADE,
    name text NOT NULL,
    command text NOT NULL
);

CREATE TABLE IF NOT EXISTS game_console_hist (
    id bigserial PRIMARY KEY,
    server_id text NOT NULL,
    command text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS game_console_hist_server ON game_console_hist (server_id, id DESC);

CREATE TABLE IF NOT EXISTS game_user_prefs (
    cluster_id uuid NOT NULL REFERENCES clusters (id) ON DELETE CASCADE,
    user_id text NOT NULL,
    key text NOT NULL,
    value text NOT NULL DEFAULT '',
    PRIMARY KEY (cluster_id, user_id, key)
);
