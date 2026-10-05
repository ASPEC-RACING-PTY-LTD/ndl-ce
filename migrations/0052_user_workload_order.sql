ALTER TABLE user_prefs
    ADD COLUMN workload_sort text NOT NULL DEFAULT '',
    ADD COLUMN workload_order jsonb NOT NULL DEFAULT '[]'::jsonb;
