-- Daily pruning of unused Docker images and build cache in every machine
-- with Docker. Images used by any container, running or stopped, and all
-- volumes are kept.
CREATE TABLE IF NOT EXISTS docker_prune_settings (
    cluster_id uuid PRIMARY KEY REFERENCES clusters (id) ON DELETE CASCADE,
    enabled boolean NOT NULL DEFAULT true,
    run_at text NOT NULL DEFAULT '06:00',
    timezone text NOT NULL DEFAULT 'Australia/Melbourne',
    last_run_at timestamptz,
    last_result text NOT NULL DEFAULT '',
    updated_by text NOT NULL DEFAULT '',
    updated_at timestamptz NOT NULL DEFAULT now()
);
