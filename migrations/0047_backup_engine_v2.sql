-- Backup Engine V2: additive restore-point model, workspace bounds, and
-- policy capture modes. Existing artifacts and policies stay intact.

ALTER TABLE backup_policies
    ADD COLUMN IF NOT EXISTS capture_mode text NOT NULL DEFAULT 'smart',
    ADD COLUMN IF NOT EXISTS scope_json text NOT NULL DEFAULT '';

ALTER TABLE backup_artifacts
    ADD COLUMN IF NOT EXISTS engine_version text NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS backup_id text NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS namespace text NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS local_complete boolean NOT NULL DEFAULT false,
    ADD COLUMN IF NOT EXISTS remote_state text NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS logical_bytes bigint NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS physical_new_data bigint NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS chunks_new integer NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS chunks_reused integer NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS capture_duration_ns bigint NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS upload_duration_ns bigint NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS consistency text NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS capture_mode text NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS blueprint_json text NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS stats_json text NOT NULL DEFAULT '';

CREATE TABLE IF NOT EXISTS backup_workspace_settings (
    cluster_id uuid PRIMARY KEY REFERENCES clusters (id) ON DELETE CASCADE,
    max_local_bytes bigint NOT NULL DEFAULT 53687091200,
    min_host_free_bytes bigint NOT NULL DEFAULT 10737418240,
    capture_concurrency integer NOT NULL DEFAULT 1,
    upload_workers integer NOT NULL DEFAULT 4,
    bandwidth_limit_bps bigint NOT NULL DEFAULT 0,
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS backup_repositories (
    id uuid PRIMARY KEY,
    cluster_id uuid NOT NULL REFERENCES clusters (id) ON DELETE CASCADE,
    root_path text NOT NULL,
    size_bytes bigint NOT NULL DEFAULT 0,
    status text NOT NULL DEFAULT 'local',
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX IF NOT EXISTS backup_repositories_cluster ON backup_repositories (cluster_id);

CREATE TABLE IF NOT EXISTS backup_restore_points (
    id uuid PRIMARY KEY,
    cluster_id uuid NOT NULL REFERENCES clusters (id) ON DELETE CASCADE,
    artifact_id uuid REFERENCES backup_artifacts (id) ON DELETE CASCADE,
    run_id uuid REFERENCES backup_runs (id) ON DELETE SET NULL,
    workload_id uuid NOT NULL,
    backup_id text NOT NULL,
    namespace text NOT NULL,
    capture_mode text NOT NULL DEFAULT 'smart',
    local_complete boolean NOT NULL DEFAULT false,
    remote_state text NOT NULL DEFAULT 'local-only',
    logical_bytes bigint NOT NULL DEFAULT 0,
    physical_new_data bigint NOT NULL DEFAULT 0,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS backup_restore_points_cluster ON backup_restore_points (cluster_id, created_at DESC);
CREATE INDEX IF NOT EXISTS backup_restore_points_workload ON backup_restore_points (cluster_id, workload_id, created_at DESC);

CREATE TABLE IF NOT EXISTS backup_upload_jobs (
    id uuid PRIMARY KEY,
    cluster_id uuid NOT NULL REFERENCES clusters (id) ON DELETE CASCADE,
    artifact_id uuid,
    backup_id text NOT NULL DEFAULT '',
    pack_id text NOT NULL DEFAULT '',
    state text NOT NULL DEFAULT 'queued',
    attempts integer NOT NULL DEFAULT 0,
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS backup_upload_jobs_cluster ON backup_upload_jobs (cluster_id, updated_at DESC);
