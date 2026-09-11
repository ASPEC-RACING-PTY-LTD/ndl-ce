-- Backup policies may cover the whole eligible fleet or a selected subset.
-- Existing rows stay selected and keep their original workload.

ALTER TABLE backup_policies
    ALTER COLUMN workload_id DROP NOT NULL;

ALTER TABLE backup_policies
    DROP CONSTRAINT backup_policies_workload_id_fkey;

ALTER TABLE backup_policies
    ADD CONSTRAINT backup_policies_workload_id_fkey
    FOREIGN KEY (workload_id) REFERENCES workloads (id) ON DELETE SET NULL;

ALTER TABLE backup_policies
    ADD COLUMN IF NOT EXISTS scope text NOT NULL DEFAULT 'selected';

CREATE TABLE IF NOT EXISTS backup_policy_workloads (
    policy_id uuid NOT NULL REFERENCES backup_policies (id) ON DELETE CASCADE,
    workload_id uuid NOT NULL REFERENCES workloads (id) ON DELETE CASCADE,
    PRIMARY KEY (policy_id, workload_id)
);

INSERT INTO backup_policy_workloads (policy_id, workload_id)
SELECT id, workload_id
FROM backup_policies
WHERE workload_id IS NOT NULL
ON CONFLICT DO NOTHING;
