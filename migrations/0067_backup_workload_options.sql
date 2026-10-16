-- Per-workload backup options. keep_images keeps a docker-save copy of the
-- images a workload's containers run inside its backup, so it can be
-- restored without the registry or a rebuild.
CREATE TABLE IF NOT EXISTS backup_workload_options (
    cluster_id uuid NOT NULL REFERENCES clusters (id) ON DELETE CASCADE,
    workload_id text NOT NULL,
    keep_images boolean NOT NULL DEFAULT false,
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (cluster_id, workload_id)
);
