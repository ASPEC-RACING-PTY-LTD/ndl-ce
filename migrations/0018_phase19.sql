CREATE TABLE guest_observations (
    workload_id uuid PRIMARY KEY REFERENCES workloads (id) ON DELETE CASCADE,
    cluster_id uuid NOT NULL REFERENCES clusters (id),
    qemu_ga_state text NOT NULL DEFAULT 'unavailable',
    nodal_ga_state text NOT NULL DEFAULT 'not_installed',
    nodal_ga_version text,
    guest_os text,
    guest_arch text,
    guest_ipv4 text,
    observed_at timestamptz NOT NULL DEFAULT now(),
    stale boolean NOT NULL DEFAULT false
);
