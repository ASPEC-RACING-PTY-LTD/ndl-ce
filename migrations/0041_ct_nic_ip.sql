-- Independent IPv4/IPv6 policy for system-container NICs.
-- ipv4 remains the observed address from the guest.

ALTER TABLE workload_nics
    ADD COLUMN IF NOT EXISTS ipv4_mode text NOT NULL DEFAULT 'dhcp',
    ADD COLUMN IF NOT EXISTS ipv4_address text NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS ipv4_gateway text NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS ipv6_mode text NOT NULL DEFAULT 'disabled',
    ADD COLUMN IF NOT EXISTS ipv6_address text NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS ipv6_gateway text NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS dns text NOT NULL DEFAULT '';
