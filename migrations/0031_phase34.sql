-- Phase 34 HA foundations and rolling updates.
-- Single writer. Replica DSN is a secret. STONITH is not implemented.

ALTER TABLE cluster_leases ADD COLUMN IF NOT EXISTS fenced boolean NOT NULL DEFAULT false;

CREATE TABLE IF NOT EXISTS ha_state (
    cluster_id uuid PRIMARY KEY REFERENCES clusters (id),
    mode text NOT NULL DEFAULT 'single-writer',
    replica_status text NOT NULL DEFAULT 'not_configured',
    replica_endpoint text NOT NULL DEFAULT '',
    fencing_mode text NOT NULL DEFAULT 'operator',
    fenced_holder text NOT NULL DEFAULT '',
    fenced_at timestamptz,
    promoted_holder text NOT NULL DEFAULT '',
    promoted_at timestamptz,
    reason text NOT NULL DEFAULT '',
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS secrets.ha_credentials (
    cluster_id uuid PRIMARY KEY REFERENCES clusters (id) ON DELETE CASCADE,
    replica_dsn text NOT NULL DEFAULT '',
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS rolling_plans (
    id uuid PRIMARY KEY,
    cluster_id uuid NOT NULL REFERENCES clusters (id),
    status text NOT NULL,
    reason text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    finished_at timestamptz
);

CREATE TABLE IF NOT EXISTS rolling_steps (
    id uuid PRIMARY KEY,
    plan_id uuid NOT NULL REFERENCES rolling_plans (id) ON DELETE CASCADE,
    cluster_id uuid NOT NULL REFERENCES clusters (id),
    node_id uuid NOT NULL REFERENCES nodes (id),
    ordinal integer NOT NULL,
    action text NOT NULL,
    status text NOT NULL,
    reason text NOT NULL DEFAULT '',
    update_operation_id uuid,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS rolling_plans_cluster ON rolling_plans (cluster_id, created_at DESC);
CREATE INDEX IF NOT EXISTS rolling_steps_plan ON rolling_steps (plan_id, ordinal);
