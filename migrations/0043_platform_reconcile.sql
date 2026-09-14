ALTER TABLE volumes ADD COLUMN IF NOT EXISTS owner text NOT NULL DEFAULT '';
ALTER TABLE volumes ADD COLUMN IF NOT EXISTS owner_kind text NOT NULL DEFAULT '';
ALTER TABLE volumes ADD COLUMN IF NOT EXISTS owner_job_id text NOT NULL DEFAULT '';

CREATE INDEX IF NOT EXISTS volumes_owner_kind ON volumes (cluster_id, owner_kind);
