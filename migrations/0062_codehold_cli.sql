-- CodeHold CLI ("ch") in system containers: where it is downloaded from,
-- whether new containers get it, and the bulk install defaults. No secrets
-- are kept here.
CREATE TABLE IF NOT EXISTS codehold_cli_settings (
    cluster_id uuid PRIMARY KEY REFERENCES clusters (id) ON DELETE CASCADE,
    url text NOT NULL DEFAULT '',
    auto_install boolean NOT NULL DEFAULT true,
    concurrency integer NOT NULL DEFAULT 0,
    timeout_seconds integer NOT NULL DEFAULT 0,
    updated_by text NOT NULL DEFAULT '',
    updated_at timestamptz NOT NULL DEFAULT now()
);
