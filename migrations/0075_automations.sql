-- Automations: flows of a trigger plus steps. Drafts are edited, published
-- versions are frozen, and runs execute a version step by step with their
-- state saved here so they resume after a restart.

CREATE TABLE IF NOT EXISTS automation_flows (
    id uuid PRIMARY KEY,
    cluster_id uuid NOT NULL REFERENCES clusters (id),
    name text NOT NULL,
    description text NOT NULL DEFAULT '',
    draft jsonb NOT NULL DEFAULT '{}',
    published_version integer NOT NULL DEFAULT 0,
    enabled boolean NOT NULL DEFAULT false,
    trigger_kind text NOT NULL DEFAULT '',
    event_kind text NOT NULL DEFAULT '',
    limits jsonb NOT NULL DEFAULT '{}',
    dry_run_hash text NOT NULL DEFAULT '',
    consecutive_failures integer NOT NULL DEFAULT 0,
    disabled_reason text NOT NULL DEFAULT '',
    legacy_policy_id uuid,
    created_by text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (cluster_id, name)
);

-- Event dispatch reads only the flows listening for one trigger kind.
CREATE INDEX IF NOT EXISTS automation_flows_trigger_idx
    ON automation_flows (cluster_id, trigger_kind, event_kind) WHERE enabled AND published_version > 0;
CREATE UNIQUE INDEX IF NOT EXISTS automation_flows_legacy_idx
    ON automation_flows (legacy_policy_id) WHERE legacy_policy_id IS NOT NULL;

CREATE TABLE IF NOT EXISTS automation_flow_versions (
    flow_id uuid NOT NULL REFERENCES automation_flows (id) ON DELETE CASCADE,
    cluster_id uuid NOT NULL REFERENCES clusters (id),
    version integer NOT NULL,
    definition jsonb NOT NULL,
    published_by text NOT NULL DEFAULT '',
    note text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (flow_id, version)
);

-- Saved credentials. sealed is encrypted with the control plane's secrets
-- key and is never returned by the API.
CREATE TABLE IF NOT EXISTS automation_connections (
    id uuid PRIMARY KEY,
    cluster_id uuid NOT NULL REFERENCES clusters (id),
    name text NOT NULL,
    kind text NOT NULL,
    config jsonb NOT NULL DEFAULT '{}',
    sealed text NOT NULL DEFAULT '',
    created_by text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (cluster_id, name)
);

-- Inbound webhook endpoints. Only SHA-256 hashes of the token and secret.
CREATE TABLE IF NOT EXISTS automation_webhook_endpoints (
    flow_id uuid PRIMARY KEY REFERENCES automation_flows (id) ON DELETE CASCADE,
    cluster_id uuid NOT NULL REFERENCES clusters (id),
    token_hash text NOT NULL UNIQUE,
    secret_hash text NOT NULL,
    rotated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS automation_webhook_deliveries (
    id uuid PRIMARY KEY,
    cluster_id uuid NOT NULL REFERENCES clusters (id),
    flow_id uuid NOT NULL REFERENCES automation_flows (id) ON DELETE CASCADE,
    status integer NOT NULL,
    run_id text NOT NULL DEFAULT '',
    error text NOT NULL DEFAULT '',
    payload jsonb NOT NULL DEFAULT '{}',
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS automation_webhook_deliveries_flow_idx ON automation_webhook_deliveries (flow_id, created_at DESC);

CREATE TABLE IF NOT EXISTS automation_schedules (
    flow_id uuid PRIMARY KEY REFERENCES automation_flows (id) ON DELETE CASCADE,
    cluster_id uuid NOT NULL REFERENCES clusters (id),
    preset text NOT NULL DEFAULT '',
    cron text NOT NULL DEFAULT '',
    timezone text NOT NULL DEFAULT '',
    next_run_at timestamptz,
    last_key text NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS automation_schedules_due_idx ON automation_schedules (next_run_at);

CREATE TABLE IF NOT EXISTS automation_runs (
    id uuid PRIMARY KEY,
    cluster_id uuid NOT NULL REFERENCES clusters (id),
    flow_id uuid NOT NULL REFERENCES automation_flows (id) ON DELETE CASCADE,
    version integer NOT NULL DEFAULT 0,
    mode text NOT NULL DEFAULT 'live',
    status text NOT NULL,
    trigger_kind text NOT NULL DEFAULT '',
    trigger_payload jsonb NOT NULL DEFAULT '{}',
    inputs jsonb NOT NULL DEFAULT '{}',
    definition jsonb NOT NULL DEFAULT '{}',
    state jsonb NOT NULL DEFAULT '{}',
    report jsonb NOT NULL DEFAULT '{}',
    idempotency_key text NOT NULL DEFAULT '',
    correlation_id text NOT NULL DEFAULT '',
    caused_by_run text NOT NULL DEFAULT '',
    chain_depth integer NOT NULL DEFAULT 0,
    retry_of text NOT NULL DEFAULT '',
    actor_id text NOT NULL DEFAULT '',
    started_by text NOT NULL DEFAULT '',
    error text NOT NULL DEFAULT '',
    wake_at timestamptz,
    -- A worker holds the lease while it steps the run.
    lease_owner text NOT NULL DEFAULT '',
    lease_until timestamptz,
    started_at timestamptz,
    finished_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS automation_runs_flow_idx ON automation_runs (flow_id, created_at DESC);
CREATE INDEX IF NOT EXISTS automation_runs_cluster_idx ON automation_runs (cluster_id, created_at DESC);
CREATE INDEX IF NOT EXISTS automation_runs_due_idx ON automation_runs (status, wake_at)
    WHERE status IN ('pending', 'running', 'waiting');
CREATE INDEX IF NOT EXISTS automation_runs_key_idx ON automation_runs (flow_id, idempotency_key) WHERE idempotency_key <> '';

CREATE TABLE IF NOT EXISTS automation_node_runs (
    id uuid PRIMARY KEY,
    cluster_id uuid NOT NULL REFERENCES clusters (id),
    run_id uuid NOT NULL REFERENCES automation_runs (id) ON DELETE CASCADE,
    node_id text NOT NULL,
    scope text NOT NULL DEFAULT '',
    type text NOT NULL DEFAULT '',
    status text NOT NULL,
    port text NOT NULL DEFAULT '',
    input jsonb NOT NULL DEFAULT '{}',
    output jsonb NOT NULL DEFAULT '{}',
    error text NOT NULL DEFAULT '',
    attempts integer NOT NULL DEFAULT 0,
    started_at timestamptz,
    finished_at timestamptz,
    UNIQUE (run_id, scope, node_id)
);

CREATE TABLE IF NOT EXISTS automation_node_attempts (
    id uuid PRIMARY KEY,
    cluster_id uuid NOT NULL REFERENCES clusters (id),
    run_id uuid NOT NULL REFERENCES automation_runs (id) ON DELETE CASCADE,
    node_run_id uuid NOT NULL,
    attempt integer NOT NULL,
    status text NOT NULL,
    error text NOT NULL DEFAULT '',
    started_at timestamptz NOT NULL DEFAULT now(),
    finished_at timestamptz
);
CREATE INDEX IF NOT EXISTS automation_node_attempts_run_idx ON automation_node_attempts (run_id);

CREATE TABLE IF NOT EXISTS automation_run_logs (
    id uuid PRIMARY KEY,
    cluster_id uuid NOT NULL REFERENCES clusters (id),
    run_id uuid NOT NULL REFERENCES automation_runs (id) ON DELETE CASCADE,
    node_id text NOT NULL DEFAULT '',
    level text NOT NULL DEFAULT 'info',
    message text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS automation_run_logs_run_idx ON automation_run_logs (run_id, created_at);

-- Destructive actions per target, across all flows, for the per-target limit.
CREATE TABLE IF NOT EXISTS automation_action_ledger (
    id uuid PRIMARY KEY,
    cluster_id uuid NOT NULL REFERENCES clusters (id),
    action text NOT NULL,
    target text NOT NULL,
    flow_id text NOT NULL DEFAULT '',
    run_id text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS automation_action_ledger_target_idx ON automation_action_ledger (cluster_id, action, target, created_at);
