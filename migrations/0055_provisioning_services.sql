CREATE TABLE IF NOT EXISTS provisioning_services (
    cluster_id uuid NOT NULL REFERENCES clusters (id),
    external_id text NOT NULL,
    kind text NOT NULL,
    resource_id text NOT NULL DEFAULT '',
    template text NOT NULL DEFAULT '',
    desired_power text NOT NULL DEFAULT 'running',
    suspended boolean NOT NULL DEFAULT false,
    labels jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (cluster_id, external_id)
);
