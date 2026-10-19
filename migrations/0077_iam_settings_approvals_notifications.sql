-- Cluster-wide identity settings, per-user notifications and approval of
-- automation flow versions that hold protected steps.

-- root_user_id is the account that set the appliance up. Only it may manage
-- itself, and it is changed only from the host (nodalctl set-root).
-- automation_creators says who may create and edit flows; admins may change
-- it until a SuperAdmin saves it (creators_locked), unless that SuperAdmin
-- let them (creators_admins_may_edit). approvals_access gives roles the
-- approvals.view / approvals.decide permissions; only SuperAdmins edit it.
CREATE TABLE IF NOT EXISTS iam_settings (
    cluster_id uuid PRIMARY KEY REFERENCES clusters (id),
    root_user_id uuid REFERENCES users (id) ON DELETE SET NULL,
    automation_creators jsonb NOT NULL DEFAULT '{"min_role":"admin"}'::jsonb,
    creators_locked boolean NOT NULL DEFAULT false,
    creators_admins_may_edit boolean NOT NULL DEFAULT false,
    approvals_access jsonb NOT NULL DEFAULT '{}'::jsonb,
    updated_by text NOT NULL DEFAULT '',
    updated_at timestamptz NOT NULL DEFAULT now()
);

-- Backfill: the root account is the oldest person holding SuperAdmin.
INSERT INTO iam_settings (cluster_id, root_user_id)
SELECT c.id, (
    SELECT u.id FROM users u
    JOIN role_bindings b ON b.user_id = u.id
    JOIN roles r ON r.id = b.role_id
    WHERE u.cluster_id = c.id AND r.name = 'superadmin' AND u.kind = 'person'
    ORDER BY u.created_at, u.id
    LIMIT 1)
FROM clusters c
ON CONFLICT (cluster_id) DO UPDATE SET root_user_id = COALESCE(iam_settings.root_user_id, EXCLUDED.root_user_id);

CREATE TABLE IF NOT EXISTS notifications (
    id uuid PRIMARY KEY,
    cluster_id uuid NOT NULL REFERENCES clusters (id),
    user_id uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    kind text NOT NULL,
    title text NOT NULL,
    body text NOT NULL DEFAULT '',
    link text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    read_at timestamptz
);
CREATE INDEX IF NOT EXISTS notifications_user_idx ON notifications (user_id, created_at DESC);
CREATE INDEX IF NOT EXISTS notifications_unread_idx ON notifications (user_id) WHERE read_at IS NULL;

-- A flow version is approved, waiting for approval, denied, or superseded
-- by a newer request. Only approved versions ever run. hash is the hash of
-- the definition the decision covers. exempt marks a version published by a
-- SuperAdmin from a draft only SuperAdmins edited (no approval needed).
-- Versions published before approvals existed stay approved.
ALTER TABLE automation_flow_versions
    ADD COLUMN IF NOT EXISTS status text NOT NULL DEFAULT 'approved',
    ADD COLUMN IF NOT EXISTS definition_hash text NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS needs_approval boolean NOT NULL DEFAULT false,
    ADD COLUMN IF NOT EXISTS protected_steps jsonb NOT NULL DEFAULT '[]'::jsonb,
    ADD COLUMN IF NOT EXISTS exempt boolean NOT NULL DEFAULT false,
    ADD COLUMN IF NOT EXISTS requested_by_id text NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS decided_by text NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS decided_at timestamptz,
    ADD COLUMN IF NOT EXISTS decision_note text NOT NULL DEFAULT '';
CREATE INDEX IF NOT EXISTS automation_flow_versions_status_idx
    ON automation_flow_versions (cluster_id, status, created_at DESC) WHERE needs_approval;

-- pending_version is the version waiting for approval (0 if none).
-- draft_tainted is set when someone below SuperAdmin created or edited the
-- draft since its last publish: it then loses the SuperAdmin exemption.
ALTER TABLE automation_flows
    ADD COLUMN IF NOT EXISTS pending_version integer NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS draft_tainted boolean NOT NULL DEFAULT false;

-- Existing flows keep running as they are; the next publish of a flow that
-- a SuperAdmin did not create goes through approval if it holds protected
-- steps.
UPDATE automation_flows f SET draft_tainted = NOT EXISTS (
    SELECT 1 FROM users u
    JOIN role_bindings b ON b.user_id = u.id
    JOIN roles r ON r.id = b.role_id
    WHERE u.cluster_id = f.cluster_id AND u.username = f.created_by AND r.name = 'superadmin');
