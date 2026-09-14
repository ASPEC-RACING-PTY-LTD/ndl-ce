-- Phase 44 Import / Export / Migration. Copy-first. Source destruction is not stored.

CREATE TABLE IF NOT EXISTS migration_sources (
    id uuid PRIMARY KEY,
    cluster_id uuid NOT NULL REFERENCES clusters (id),
    adapter text NOT NULL,
    label text NOT NULL DEFAULT '',
    endpoint text NOT NULL DEFAULT '',
    insecure boolean NOT NULL DEFAULT false,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS migration_sources_cluster_idx ON migration_sources (cluster_id);

CREATE TABLE IF NOT EXISTS secrets.migration_source_credentials (
    source_id uuid PRIMARY KEY REFERENCES migration_sources (id) ON DELETE CASCADE,
    token text NOT NULL DEFAULT '',
    username text NOT NULL DEFAULT '',
    extra jsonb NOT NULL DEFAULT '{}'::jsonb,
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS migration_jobs (
    id uuid PRIMARY KEY,
    cluster_id uuid NOT NULL REFERENCES clusters (id),
    source_id uuid REFERENCES migration_sources (id),
    operation_id uuid,
    adapter text NOT NULL,
    direction text NOT NULL,
    state text NOT NULL,
    stage text NOT NULL DEFAULT '',
    plan_json jsonb NOT NULL DEFAULT '{}'::jsonb,
    status_json jsonb NOT NULL DEFAULT '{}'::jsonb,
    cancel_requested boolean NOT NULL DEFAULT false,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS migration_jobs_cluster_idx ON migration_jobs (cluster_id, created_at DESC);
