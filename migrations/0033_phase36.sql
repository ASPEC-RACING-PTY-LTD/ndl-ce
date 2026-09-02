-- Phase 36 No-dal Store. Manifests are declarative. No helper-script runner.

CREATE TABLE IF NOT EXISTS store_packages (
    id uuid PRIMARY KEY,
    cluster_id uuid NOT NULL REFERENCES clusters (id),
    name text NOT NULL,
    version text NOT NULL,
    class text NOT NULL,
    title text NOT NULL DEFAULT '',
    summary text NOT NULL DEFAULT '',
    manifest_yaml text NOT NULL,
    unsigned_warning boolean NOT NULL DEFAULT false,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (cluster_id, name, version)
);

CREATE TABLE IF NOT EXISTS store_installations (
    id uuid PRIMARY KEY,
    cluster_id uuid NOT NULL REFERENCES clusters (id),
    package_id uuid NOT NULL REFERENCES store_packages (id),
    status text NOT NULL,
    reason text NOT NULL DEFAULT '',
    stack_id uuid,
    workload_id uuid,
    node_id uuid,
    warning text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    finished_at timestamptz
);

CREATE INDEX IF NOT EXISTS store_packages_cluster ON store_packages (cluster_id, name);
CREATE INDEX IF NOT EXISTS store_installations_cluster ON store_installations (cluster_id, created_at DESC);
