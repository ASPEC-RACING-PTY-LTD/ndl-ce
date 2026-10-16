-- Per-user interface preferences (theme, sidebar order, table columns).
-- Presentation only; never read for authorization.
ALTER TABLE user_prefs
    ADD COLUMN IF NOT EXISTS ui_prefs jsonb NOT NULL DEFAULT '{}'::jsonb;
