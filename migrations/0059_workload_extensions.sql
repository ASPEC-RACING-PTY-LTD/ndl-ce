-- Optional extras (Docker, Compose, git, ...) per system container: what was
-- asked for, what the guest reports as installed, and the last error, so an
-- install that failed or was cut short is visible and can be retried.
CREATE TABLE IF NOT EXISTS workload_extensions (
    workload_id uuid NOT NULL REFERENCES workloads (id) ON DELETE CASCADE,
    cluster_id uuid NOT NULL REFERENCES clusters (id),
    extra_id text NOT NULL,
    requested boolean NOT NULL DEFAULT false,
    state text NOT NULL DEFAULT 'unknown',
    version text NOT NULL DEFAULT '',
    package text NOT NULL DEFAULT '',
    detail text NOT NULL DEFAULT '',
    message text NOT NULL DEFAULT '',
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (workload_id, extra_id)
);
