-- Enterprise entitlement cache. Additive to the Phase 43 license surface.
-- Does not store workload state. Expiry never stops guests.

ALTER TABLE license_state ADD COLUMN IF NOT EXISTS entitlement_json jsonb;
ALTER TABLE license_state ADD COLUMN IF NOT EXISTS edition text NOT NULL DEFAULT 'ce';
ALTER TABLE license_state ADD COLUMN IF NOT EXISTS expires_at timestamptz;
ALTER TABLE license_state ADD COLUMN IF NOT EXISTS grace_until timestamptz;
ALTER TABLE license_state ADD COLUMN IF NOT EXISTS installation_id text NOT NULL DEFAULT '';
ALTER TABLE license_state ADD COLUMN IF NOT EXISTS organization text NOT NULL DEFAULT '';
