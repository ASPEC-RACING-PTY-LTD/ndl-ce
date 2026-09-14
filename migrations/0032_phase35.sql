-- Phase 35 optional feature modules.
-- Default disabled. Core VM and CT stay with the nodal metapackage.
-- Enabling records intent and may install an allowlisted feature package.
-- Kubernetes runtime is Phase 38; this table does not start kubelet.

CREATE TABLE IF NOT EXISTS features (
    cluster_id uuid NOT NULL REFERENCES clusters (id),
    id text NOT NULL,
    enabled boolean NOT NULL DEFAULT false,
    package_status text NOT NULL DEFAULT 'not_configured',
    runtime_status text NOT NULL DEFAULT 'not_started',
    reason text NOT NULL DEFAULT '',
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (cluster_id, id)
);

CREATE INDEX IF NOT EXISTS features_cluster ON features (cluster_id);
