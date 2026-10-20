-- Run now on a paused backup policy queues one run, made when the policy
-- is resumed. Clear queue drops it.
ALTER TABLE backup_policies ADD COLUMN IF NOT EXISTS run_queued boolean NOT NULL DEFAULT false;
