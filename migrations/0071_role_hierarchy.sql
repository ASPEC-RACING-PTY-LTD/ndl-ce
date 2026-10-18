-- Roles become editable and ranked. rank 0 is the highest; a role can only
-- act on roles below it. customized marks a built-in role whose permissions
-- were edited, so its stored list is used instead of the built-in one.
ALTER TABLE roles ADD COLUMN IF NOT EXISTS title text NOT NULL DEFAULT '';
ALTER TABLE roles ADD COLUMN IF NOT EXISTS summary text NOT NULL DEFAULT '';
ALTER TABLE roles ADD COLUMN IF NOT EXISTS rank integer NOT NULL DEFAULT 100;
ALTER TABLE roles ADD COLUMN IF NOT EXISTS builtin boolean NOT NULL DEFAULT false;
ALTER TABLE roles ADD COLUMN IF NOT EXISTS login boolean NOT NULL DEFAULT true;
ALTER TABLE roles ADD COLUMN IF NOT EXISTS customized boolean NOT NULL DEFAULT false;

UPDATE roles SET builtin = true, rank = 0, title = 'Owner', login = true WHERE name = 'admin';
UPDATE roles SET builtin = true, rank = 1, title = 'Admin', login = true WHERE name = 'operator';
UPDATE roles SET builtin = true, rank = 2, title = 'Automation', login = false WHERE name = 'automation';
UPDATE roles SET builtin = true, rank = 3, title = 'User', login = true WHERE name = 'viewer';

ALTER TABLE groups ADD COLUMN IF NOT EXISTS description text NOT NULL DEFAULT '';
