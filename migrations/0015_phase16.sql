CREATE TABLE alert_rules (
    id uuid PRIMARY KEY,
    cluster_id uuid NOT NULL REFERENCES clusters (id),
    name text NOT NULL,
    metric text NOT NULL,
    op text NOT NULL,
    threshold double precision NOT NULL,
    for_minutes integer NOT NULL DEFAULT 1,
    enabled boolean NOT NULL DEFAULT true,
    last_fired_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE notification_channels (
    id uuid PRIMARY KEY,
    cluster_id uuid NOT NULL REFERENCES clusters (id),
    name text NOT NULL,
    kind text NOT NULL,
    smtp_host text NOT NULL DEFAULT '',
    smtp_port integer NOT NULL DEFAULT 0,
    smtp_from text NOT NULL DEFAULT '',
    smtp_username text NOT NULL DEFAULT '',
    status text NOT NULL DEFAULT 'not_configured',
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE secrets.notification_secrets (
    channel_id uuid PRIMARY KEY REFERENCES notification_channels (id) ON DELETE CASCADE,
    cluster_id uuid NOT NULL REFERENCES clusters (id),
    webhook_url text NOT NULL DEFAULT '',
    smtp_password text NOT NULL DEFAULT '',
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX alert_rules_cluster ON alert_rules (cluster_id, created_at DESC);
CREATE INDEX notification_channels_cluster ON notification_channels (cluster_id, created_at DESC);
