-- A workload created after an "All workloads" backup policy waits here for
-- approval before such policies back it up. Every workload that exists now
-- is already approved.
CREATE TABLE IF NOT EXISTS backup_workload_approvals (
    cluster_id uuid NOT NULL,
    workload_id uuid NOT NULL,
    state text NOT NULL,
    decided_by text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    decided_at timestamptz,
    PRIMARY KEY (cluster_id, workload_id)
);

INSERT INTO backup_workload_approvals (cluster_id, workload_id, state, decided_at)
SELECT cluster_id, id, 'approved', now() FROM workloads
ON CONFLICT DO NOTHING;
