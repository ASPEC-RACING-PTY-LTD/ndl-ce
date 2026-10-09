ALTER TABLE update_operations
    ADD COLUMN checkpoint_id text NOT NULL DEFAULT '',
    ADD COLUMN schema_version text NOT NULL DEFAULT '';
