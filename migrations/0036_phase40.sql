-- Phase 40 automation engine.
-- Deterministic policies. Not an LLM loop. Policies cannot Host.Exec.

CREATE TABLE IF NOT EXISTS policies (
    id uuid PRIMARY KEY,
    cluster_id uuid NOT NULL REFERENCES clusters (id),
    name text NOT NULL,
    kind text NOT NULL,
    action text NOT NULL,
    threshold_percent integer NOT NULL DEFAULT 85,
    require_approval boolean NOT NULL DEFAULT false,
    enabled boolean NOT NULL DEFAULT true,
    spec_yaml text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (cluster_id, name)
);

CREATE TABLE IF NOT EXISTS policy_runs (
    id uuid PRIMARY KEY,
    cluster_id uuid NOT NULL REFERENCES clusters (id),
    policy_id uuid NOT NULL REFERENCES policies (id) ON DELETE CASCADE,
    actor_id uuid NOT NULL,
    status text NOT NULL,
    reason text NOT NULL DEFAULT '',
    operation_ids text[] NOT NULL DEFAULT '{}',
    created_at timestamptz NOT NULL DEFAULT now()
);
