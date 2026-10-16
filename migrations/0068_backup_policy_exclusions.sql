-- Workloads an "all workloads" backup policy leaves out, as a JSON array of
-- workload ids. Ignored for policies that select workloads.
ALTER TABLE backup_policies ADD COLUMN IF NOT EXISTS excluded_workload_ids text NOT NULL DEFAULT '[]';
