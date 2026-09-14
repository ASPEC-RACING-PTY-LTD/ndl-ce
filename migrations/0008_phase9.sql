CREATE TABLE certificates (
    id uuid PRIMARY KEY,
    cluster_id uuid NOT NULL REFERENCES clusters (id),
    mode text NOT NULL,
    enabled boolean NOT NULL DEFAULT false,
    common_name text NOT NULL DEFAULT '',
    sans jsonb NOT NULL DEFAULT '[]'::jsonb,
    fingerprint text NOT NULL DEFAULT '',
    not_before timestamptz,
    not_after timestamptz,
    cert_path text NOT NULL DEFAULT '',
    key_path text NOT NULL DEFAULT '',
    acme_directory text NOT NULL DEFAULT '',
    acme_email text NOT NULL DEFAULT '',
    acme_domain text NOT NULL DEFAULT '',
    acme_status text NOT NULL DEFAULT 'not_configured',
    next_renewal_at timestamptz,
    last_good_fingerprint text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (cluster_id)
);

CREATE INDEX certificates_cluster ON certificates (cluster_id);
