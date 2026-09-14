CREATE TABLE backup_targets (
    id uuid PRIMARY KEY,
    cluster_id uuid NOT NULL REFERENCES clusters (id),
    name text NOT NULL,
    kind text NOT NULL,
    locator text NOT NULL,
    status text NOT NULL DEFAULT 'not_configured',
    username text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE secrets.backup_credentials (
    target_id uuid PRIMARY KEY REFERENCES backup_targets (id) ON DELETE CASCADE,
    cluster_id uuid NOT NULL REFERENCES clusters (id),
    password text NOT NULL DEFAULT '',
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE backup_policies (
    id uuid PRIMARY KEY,
    cluster_id uuid NOT NULL REFERENCES clusters (id),
    name text NOT NULL,
    workload_id uuid NOT NULL REFERENCES workloads (id) ON DELETE CASCADE,
    target_id uuid NOT NULL REFERENCES backup_targets (id),
    schedule text NOT NULL DEFAULT 'nightly',
    keep_daily integer NOT NULL DEFAULT 7,
    keep_weekly integer NOT NULL DEFAULT 4,
    keep_monthly integer NOT NULL DEFAULT 3,
    last_run_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE backup_runs (
    id uuid PRIMARY KEY,
    cluster_id uuid NOT NULL REFERENCES clusters (id),
    policy_id uuid,
    target_id uuid NOT NULL REFERENCES backup_targets (id),
    workload_id uuid NOT NULL,
    snapshot_id uuid,
    status text NOT NULL,
    error text NOT NULL DEFAULT '',
    restored_workload_id text NOT NULL DEFAULT '',
    started_at timestamptz NOT NULL DEFAULT now(),
    finished_at timestamptz
);

CREATE TABLE backup_artifacts (
    id uuid PRIMARY KEY,
    cluster_id uuid NOT NULL REFERENCES clusters (id),
    run_id uuid NOT NULL REFERENCES backup_runs (id) ON DELETE CASCADE,
    workload_id uuid NOT NULL,
    checksum_sha256 text NOT NULL,
    size_bytes bigint NOT NULL,
    locator text NOT NULL,
    format text NOT NULL DEFAULT 'qcow2',
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX backup_runs_cluster ON backup_runs (cluster_id, started_at DESC);
CREATE INDEX backup_artifacts_cluster ON backup_artifacts (cluster_id, created_at DESC);
