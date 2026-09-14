-- Phase 43 CE 1.0 license-activation surface.
-- Empty by default. CE does not require a key. Workloads never stop.

CREATE TABLE IF NOT EXISTS license_state (
    cluster_id uuid PRIMARY KEY REFERENCES clusters (id),
    status text NOT NULL DEFAULT 'absent',
    reason text NOT NULL DEFAULT '',
    last_checked timestamptz,
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS secrets.license_keys (
    cluster_id uuid PRIMARY KEY REFERENCES clusters (id),
    license_key text NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT now()
);
