-- Role ids match the names the UI shows. Built-in roles are renamed in
-- place, so ids, bindings (user and group bindings point at roles.id),
-- permissions and timestamps all stay:
--   admin (shown as Owner) -> superadmin (SuperAdmin)
--   operator (shown as Admin) -> admin (Admin)
--   viewer (shown as User) -> user (User)
--   automation stays automation.
-- "admin" is reused, so the order matters. Custom roles get the id of their
-- name where that is safe; a custom role that already holds one of the new
-- ids (or "owner", which old API clients may still send) moves aside to
-- "<id>-custom". Every change is written to the audit log. Audit history
-- itself is left as it was written.
--
-- One DO block: it either completes or rolls back entirely. It is safe to
-- run again: a cluster that already has the built-in superadmin role is
-- skipped. The checks at the end fail the migration rather than leave an old
-- id behind.
DO $$
DECLARE
    c record;
    r record;
    taken text;
    wanted text;
    n integer;
BEGIN
    FOR c IN SELECT id FROM clusters LOOP
        IF EXISTS (SELECT 1 FROM roles WHERE cluster_id = c.id AND name = 'superadmin' AND builtin) THEN
            CONTINUE;
        END IF;

        -- Custom roles in the way of the new ids, or named like an old one.
        FOR r IN
            SELECT id, name, title FROM roles
            WHERE cluster_id = c.id AND NOT builtin AND name IN ('superadmin', 'user', 'owner')
        LOOP
            taken := r.name || '-custom';
            n := 1;
            WHILE EXISTS (SELECT 1 FROM roles WHERE cluster_id = c.id AND name = taken) LOOP
                n := n + 1;
                taken := r.name || '-custom-' || n;
            END LOOP;
            UPDATE roles SET name = taken, title = CASE WHEN title = '' THEN r.name ELSE title END WHERE id = r.id;
            INSERT INTO audit_events (id, cluster_id, actor_user_id, action, result, remote_addr, detail, created_at)
            VALUES (md5(random()::text || r.id::text)::uuid, c.id, NULL, 'role.rename', 'ok', 'internal',
                    jsonb_build_object('detail', r.name || ' -> ' || taken,
                                       'note', 'custom role moved aside: its id is now used by a built-in role'), now());
        END LOOP;

        -- Clusters created after 0071 were seeded without the built-in
        -- flags (rank 100, no title); those get the built-in order here.
        UPDATE roles SET name = 'superadmin', title = 'SuperAdmin', builtin = true, login = true,
               rank = CASE WHEN builtin THEN rank ELSE 0 END,
               summary = CASE WHEN summary ILIKE '%owner%' THEN '' ELSE summary END
        WHERE cluster_id = c.id AND name = 'admin';
        UPDATE roles SET name = 'admin', title = 'Admin', builtin = true, login = true,
               rank = CASE WHEN builtin THEN rank ELSE 1 END
        WHERE cluster_id = c.id AND name = 'operator';
        UPDATE roles SET title = 'Automation', builtin = true, login = false,
               rank = CASE WHEN builtin THEN rank ELSE 2 END
        WHERE cluster_id = c.id AND name = 'automation';
        UPDATE roles SET name = 'user', title = 'User', builtin = true, login = true,
               rank = CASE WHEN builtin THEN rank ELSE 3 END
        WHERE cluster_id = c.id AND name = 'viewer';

        -- Custom roles whose id differs from their name take the name's id
        -- when it is a valid, free id. Bindings follow (they hold roles.id).
        FOR r IN SELECT id, name, title FROM roles WHERE cluster_id = c.id AND NOT builtin LOOP
            IF r.title = '' THEN
                UPDATE roles SET title = r.name WHERE id = r.id;
                CONTINUE;
            END IF;
            wanted := left(trim(BOTH '-' FROM lower(regexp_replace(trim(r.title), '[^A-Za-z0-9]+', '-', 'g'))), 40);
            IF wanted = r.name THEN
                CONTINUE;
            END IF;
            IF wanted ~ '^[a-z][a-z0-9-]{1,39}$'
               AND wanted NOT IN ('superadmin', 'admin', 'user', 'automation', 'owner', 'operator', 'viewer')
               AND NOT EXISTS (SELECT 1 FROM roles WHERE cluster_id = c.id AND name = wanted) THEN
                UPDATE roles SET name = wanted WHERE id = r.id;
                INSERT INTO audit_events (id, cluster_id, actor_user_id, action, result, remote_addr, detail, created_at)
                VALUES (md5(random()::text || r.id::text)::uuid, c.id, NULL, 'role.rename', 'ok', 'internal',
                        jsonb_build_object('detail', r.name || ' -> ' || wanted, 'note', 'role id now matches its name'), now());
            ELSE
                INSERT INTO audit_events (id, cluster_id, actor_user_id, action, result, remote_addr, detail, created_at)
                VALUES (md5(random()::text || r.id::text)::uuid, c.id, NULL, 'role.rename', 'skipped', 'internal',
                        jsonb_build_object('detail', r.name,
                                           'note', 'role id could not match its name "' || r.title || '"; rename the role'), now());
            END IF;
        END LOOP;
    END LOOP;

    SELECT count(*) INTO n FROM roles WHERE name IN ('operator', 'viewer', 'owner');
    IF n > 0 THEN
        RAISE EXCEPTION 'role rename left % role(s) with an old id', n;
    END IF;
    SELECT count(*) INTO n FROM roles WHERE name = 'admin' AND title <> 'Admin';
    IF n > 0 THEN
        RAISE EXCEPTION 'role rename left % admin role(s) with the old meaning', n;
    END IF;
    SELECT count(*) INTO n FROM clusters cl
    WHERE EXISTS (SELECT 1 FROM roles WHERE cluster_id = cl.id)
      AND NOT EXISTS (SELECT 1 FROM roles WHERE cluster_id = cl.id AND name = 'superadmin');
    IF n > 0 THEN
        RAISE EXCEPTION 'role rename left % cluster(s) without a superadmin role', n;
    END IF;
END $$;
