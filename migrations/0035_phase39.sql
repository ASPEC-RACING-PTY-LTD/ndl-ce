-- Phase 39 optional distributed storage.
-- Consume an external Ceph/RBD cluster. OSD bring-up is a named sub-deliverable.
-- Cephx keys stay in secrets and are never list JSON. Enabling the feature does not start ceph-osd.

CREATE TABLE IF NOT EXISTS distributed_pools (
    pool_id uuid PRIMARY KEY REFERENCES storage_pools (id) ON DELETE CASCADE,
    cluster_id uuid NOT NULL REFERENCES clusters (id),
    locator text NOT NULL,
    ceph_pool text NOT NULL,
    ceph_user text NOT NULL DEFAULT 'admin',
    fsid text NOT NULL DEFAULT ''
);

CREATE TABLE IF NOT EXISTS secrets.distributed_credentials (
    pool_id uuid PRIMARY KEY REFERENCES storage_pools (id) ON DELETE CASCADE,
    cluster_id uuid NOT NULL REFERENCES clusters (id),
    cephx_key text NOT NULL DEFAULT '',
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS distributed_osds (
    id uuid PRIMARY KEY,
    cluster_id uuid NOT NULL REFERENCES clusters (id),
    node_id uuid NOT NULL REFERENCES nodes (id),
    pool_id uuid REFERENCES storage_pools (id) ON DELETE SET NULL,
    disk text NOT NULL,
    osd_id integer NOT NULL DEFAULT 0,
    status text NOT NULL DEFAULT 'not_started',
    reason text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);
