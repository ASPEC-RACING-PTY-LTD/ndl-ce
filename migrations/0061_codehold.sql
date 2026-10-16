-- CodeHold runners. No-dal acts as a CodeHold host controller: CodeHold keeps
-- each runner's desired state and No-dal runs it as a system container.
-- These tables keep the connection, the controller's sealed credential and
-- which container serves which runner.

CREATE TABLE IF NOT EXISTS codehold_controllers (
    cluster_id uuid PRIMARY KEY REFERENCES clusters (id) ON DELETE CASCADE,
    url text NOT NULL,
    runner_url text NOT NULL DEFAULT '',
    controller_id text NOT NULL DEFAULT '',
    name text NOT NULL DEFAULT '',
    status text NOT NULL DEFAULT 'disconnected',
    status_reason text NOT NULL DEFAULT '',
    settings jsonb NOT NULL DEFAULT '{}'::jsonb,
    last_sync_at timestamptz,
    credential_expires_at timestamptz,
    created_by text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

-- The controller credential (and its predecessor while a rotation is
-- confirmed) and the Cloudflare Access service token, AES-256-GCM sealed with
-- a key file only the control plane can read.
CREATE TABLE IF NOT EXISTS secrets.codehold_secrets (
    cluster_id uuid NOT NULL,
    name text NOT NULL,
    sealed text NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (cluster_id, name)
);

CREATE TABLE IF NOT EXISTS codehold_runners (
    cluster_id uuid NOT NULL REFERENCES clusters (id) ON DELETE CASCADE,
    runner_id text NOT NULL,
    name text NOT NULL DEFAULT '',
    workload_id text NOT NULL DEFAULT '',
    desired text NOT NULL DEFAULT '',
    state text NOT NULL DEFAULT 'provisioning',
    error text NOT NULL DEFAULT '',
    busy integer NOT NULL DEFAULT 0,
    spec text NOT NULL DEFAULT '',
    applied_spec text NOT NULL DEFAULT '',
    token_hash text NOT NULL DEFAULT '',
    limits jsonb NOT NULL DEFAULT '{}'::jsonb,
    -- Set when the integration was turned off: the container is kept but no
    -- longer synced, or is deleted once delete_after passes.
    detached boolean NOT NULL DEFAULT false,
    delete_after timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (cluster_id, runner_id)
);
