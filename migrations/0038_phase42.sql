-- Phase 42 AI Plan, Operate, Automate.
-- Reviewable existing-API plans. Never a shell. actor_type is ai.

CREATE TABLE IF NOT EXISTS ai_plans (
    id uuid PRIMARY KEY,
    cluster_id uuid NOT NULL REFERENCES clusters (id),
    profile_id uuid,
    prompt text NOT NULL,
    status text NOT NULL,
    actor_type text NOT NULL DEFAULT 'ai',
    reason text NOT NULL DEFAULT '',
    created_by uuid NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS ai_plan_steps (
    id uuid PRIMARY KEY,
    cluster_id uuid NOT NULL REFERENCES clusters (id),
    plan_id uuid NOT NULL REFERENCES ai_plans (id) ON DELETE CASCADE,
    ordinal integer NOT NULL,
    action text NOT NULL,
    permission text NOT NULL,
    method text NOT NULL,
    path text NOT NULL,
    title text NOT NULL DEFAULT '',
    body_json text NOT NULL DEFAULT '{}',
    status text NOT NULL DEFAULT 'preview',
    reason text NOT NULL DEFAULT '',
    operation_id text NOT NULL DEFAULT ''
);

CREATE INDEX IF NOT EXISTS ai_plans_cluster ON ai_plans (cluster_id, created_at DESC);
CREATE INDEX IF NOT EXISTS ai_plan_steps_plan ON ai_plan_steps (plan_id, ordinal);
