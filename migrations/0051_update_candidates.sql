ALTER TABLE update_operations
    ADD COLUMN candidates jsonb NOT NULL DEFAULT '[]'::jsonb;