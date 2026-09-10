-- Additive identity columns for Management. Existing rows keep their
-- passwords, roles, sessions, and MFA secrets unchanged.

ALTER TABLE users ADD COLUMN IF NOT EXISTS display_name text NOT NULL DEFAULT '';
ALTER TABLE users ADD COLUMN IF NOT EXISTS disabled_at timestamptz;
ALTER TABLE users ADD COLUMN IF NOT EXISTS mfa_required boolean NOT NULL DEFAULT false;
ALTER TABLE users ADD COLUMN IF NOT EXISTS last_login_at timestamptz;

ALTER TABLE clusters ADD COLUMN IF NOT EXISTS mfa_required boolean NOT NULL DEFAULT false;

ALTER TABLE api_tokens ADD COLUMN IF NOT EXISTS last_used_at timestamptz;
