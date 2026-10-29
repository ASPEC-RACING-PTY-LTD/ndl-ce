-- The built-in IAM Manager role sits between SuperAdmin and Admin. Each
-- cluster gets it at Admin's current rank, and Admin plus every role ranked
-- at or below Admin moves down one place, so custom orderings keep their
-- shape. Its permissions are filled in by EnsureRoles at start-up (a
-- built-in role that was never edited uses the built-in list). Safe to run
-- again: clusters that already have the role are skipped.
DO $$
DECLARE
    c record;
    admin_rank integer;
BEGIN
    FOR c IN SELECT id FROM clusters LOOP
        IF EXISTS (SELECT 1 FROM roles WHERE cluster_id = c.id AND name = 'iam-manager') THEN
            CONTINUE;
        END IF;
        SELECT rank INTO admin_rank FROM roles WHERE cluster_id = c.id AND name = 'admin';
        IF admin_rank IS NULL THEN
            CONTINUE;
        END IF;
        UPDATE roles SET rank = rank + 1 WHERE cluster_id = c.id AND rank >= admin_rank;
        INSERT INTO roles (id, cluster_id, name, permissions, title, summary, rank, builtin, login, customized)
        VALUES (md5(random()::text || clock_timestamp()::text)::uuid, c.id, 'iam-manager', '{}', 'IAM Manager', '', admin_rank, true, true, false);
    END LOOP;
END $$;
