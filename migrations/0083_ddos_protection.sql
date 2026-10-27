-- DDoS protection: one settings document per cluster (mode, trusted and
-- blocked networks, protected targets, hand-added bans) and a history of
-- detected attacks.
CREATE TABLE IF NOT EXISTS ddos_config (
    cluster_id uuid PRIMARY KEY REFERENCES clusters (id) ON DELETE CASCADE,
    body jsonb NOT NULL DEFAULT '{}'::jsonb,
    updated_by text NOT NULL DEFAULT '',
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS ddos_events (
    id uuid PRIMARY KEY,
    cluster_id uuid NOT NULL REFERENCES clusters (id) ON DELETE CASCADE,
    rule_id text NOT NULL,
    target text NOT NULL DEFAULT '',
    started_at timestamptz NOT NULL,
    ended_at timestamptz,
    peak_pps bigint NOT NULL DEFAULT 0,
    dropped_packets bigint NOT NULL DEFAULT 0,
    dropped_bytes bigint NOT NULL DEFAULT 0,
    top_reason text NOT NULL DEFAULT '',
    sources integer NOT NULL DEFAULT 0
);

CREATE INDEX IF NOT EXISTS ddos_events_cluster_started ON ddos_events (cluster_id, started_at DESC);
