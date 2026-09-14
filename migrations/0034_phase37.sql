-- Phase 37 Store trust pipeline.
-- Signatures, scan results, and install policy. Private key material stays in secrets.

CREATE TABLE IF NOT EXISTS store_signing_keys (
    id uuid PRIMARY KEY,
    cluster_id uuid NOT NULL REFERENCES clusters (id),
    name text NOT NULL,
    class text NOT NULL,
    public_key text NOT NULL,
    status text NOT NULL DEFAULT 'active',
    created_at timestamptz NOT NULL DEFAULT now(),
    revoked_at timestamptz,
    UNIQUE (cluster_id, name)
);

CREATE TABLE IF NOT EXISTS secrets.store_signing_private (
    key_id uuid PRIMARY KEY REFERENCES store_signing_keys (id) ON DELETE CASCADE,
    cluster_id uuid NOT NULL,
    private_key text NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS store_package_signatures (
    id uuid PRIMARY KEY,
    cluster_id uuid NOT NULL REFERENCES clusters (id),
    package_id uuid NOT NULL REFERENCES store_packages (id) ON DELETE CASCADE,
    key_id uuid NOT NULL REFERENCES store_signing_keys (id),
    algorithm text NOT NULL,
    signature_b64 text NOT NULL,
    payload_sha256 text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS store_verifications (
    id uuid PRIMARY KEY,
    cluster_id uuid NOT NULL REFERENCES clusters (id),
    package_id uuid NOT NULL REFERENCES store_packages (id) ON DELETE CASCADE,
    status text NOT NULL,
    reason text NOT NULL DEFAULT '',
    trust_class text NOT NULL DEFAULT 'community',
    key_id uuid,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS store_scan_results (
    id uuid PRIMARY KEY,
    cluster_id uuid NOT NULL REFERENCES clusters (id),
    package_id uuid NOT NULL REFERENCES store_packages (id) ON DELETE CASCADE,
    verification_id uuid NOT NULL REFERENCES store_verifications (id) ON DELETE CASCADE,
    kind text NOT NULL,
    status text NOT NULL,
    detail text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS store_policies (
    cluster_id uuid PRIMARY KEY REFERENCES clusters (id),
    install_policy text NOT NULL DEFAULT 'community-allowed',
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS store_signing_keys_cluster ON store_signing_keys (cluster_id, status);
CREATE INDEX IF NOT EXISTS store_package_signatures_pkg ON store_package_signatures (cluster_id, package_id, created_at DESC);
CREATE INDEX IF NOT EXISTS store_verifications_pkg ON store_verifications (cluster_id, package_id, created_at DESC);
CREATE INDEX IF NOT EXISTS store_scan_results_verify ON store_scan_results (verification_id);
