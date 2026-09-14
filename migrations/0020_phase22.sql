-- Phase 22 application stacks. Compose import becomes inspectable stack objects.
-- Compose is not the runtime source of truth.

CREATE TABLE stacks (
    id uuid PRIMARY KEY,
    cluster_id uuid NOT NULL REFERENCES clusters (id),
    name text NOT NULL,
    status text NOT NULL DEFAULT 'draft',
    desired_json jsonb NOT NULL DEFAULT '{}'::jsonb,
    source_compose text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (cluster_id, name)
);

CREATE TABLE stack_members (
    id uuid PRIMARY KEY,
    cluster_id uuid NOT NULL REFERENCES clusters (id),
    stack_id uuid NOT NULL REFERENCES stacks (id) ON DELETE CASCADE,
    service_name text NOT NULL,
    workload_id uuid REFERENCES workloads (id) ON DELETE SET NULL,
    desired_json jsonb NOT NULL DEFAULT '{}'::jsonb,
    status text NOT NULL DEFAULT 'pending',
    sort_order integer NOT NULL DEFAULT 0,
    reason text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (stack_id, service_name)
);

CREATE INDEX stacks_cluster ON stacks (cluster_id, created_at DESC);
CREATE INDEX stack_members_stack ON stack_members (stack_id, sort_order ASC);
CREATE INDEX stack_members_workload ON stack_members (workload_id) WHERE workload_id IS NOT NULL;
