-- Saved JSON scripts (Docker bulk tasks and Uptime imports) and Uptime
-- monitors that check public URLs and restart the containers behind them.
CREATE TABLE IF NOT EXISTS saved_scripts (
    id uuid PRIMARY KEY,
    cluster_id uuid NOT NULL REFERENCES clusters (id),
    kind text NOT NULL,
    name text NOT NULL,
    body text NOT NULL,
    created_by text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (cluster_id, kind, name)
);

CREATE TABLE IF NOT EXISTS uptime_monitors (
    id uuid PRIMARY KEY,
    cluster_id uuid NOT NULL REFERENCES clusters (id),
    name text NOT NULL,
    target text NOT NULL,
    expect_status text NOT NULL DEFAULT '',
    keyword text NOT NULL DEFAULT '',
    containers jsonb NOT NULL DEFAULT '[]'::jsonb,
    auto_restart boolean NOT NULL DEFAULT true,
    enabled boolean NOT NULL DEFAULT true,
    state text NOT NULL DEFAULT 'pending',
    last_check_at timestamptz,
    last_ok_at timestamptz,
    last_change_at timestamptz,
    last_status integer NOT NULL DEFAULT 0,
    last_latency_ms integer NOT NULL DEFAULT 0,
    last_error text NOT NULL DEFAULT '',
    failures integer NOT NULL DEFAULT 0,
    last_restart_at timestamptz,
    next_check_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (cluster_id, name)
);

CREATE TABLE IF NOT EXISTS uptime_events (
    id bigserial PRIMARY KEY,
    monitor_id uuid NOT NULL REFERENCES uptime_monitors (id) ON DELETE CASCADE,
    at timestamptz NOT NULL DEFAULT now(),
    ok boolean NOT NULL,
    status_code integer NOT NULL DEFAULT 0,
    latency_ms integer NOT NULL DEFAULT 0,
    error text NOT NULL DEFAULT '',
    action text NOT NULL DEFAULT ''
);

CREATE INDEX IF NOT EXISTS uptime_events_monitor ON uptime_events (monitor_id, at DESC);
