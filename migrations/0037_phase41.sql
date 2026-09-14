-- Phase 41 AI Ask.
-- Provider-neutral read-only assistant. API keys stay in secrets. Not an LLM automation loop.

CREATE TABLE IF NOT EXISTS ai_providers (
    id uuid PRIMARY KEY,
    cluster_id uuid NOT NULL REFERENCES clusters (id),
    name text NOT NULL,
    kind text NOT NULL,
    endpoint text NOT NULL DEFAULT '',
    model text NOT NULL DEFAULT '',
    enabled boolean NOT NULL DEFAULT true,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (cluster_id, name)
);

CREATE TABLE IF NOT EXISTS secrets.ai_credentials (
    provider_id uuid PRIMARY KEY REFERENCES ai_providers (id) ON DELETE CASCADE,
    cluster_id uuid NOT NULL,
    api_key text NOT NULL DEFAULT '',
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS ai_profiles (
    id uuid PRIMARY KEY,
    cluster_id uuid NOT NULL REFERENCES clusters (id),
    name text NOT NULL,
    provider_id uuid REFERENCES ai_providers (id) ON DELETE SET NULL,
    mode text NOT NULL DEFAULT 'ask',
    grants text[] NOT NULL DEFAULT '{}',
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (cluster_id, name)
);

CREATE INDEX IF NOT EXISTS ai_providers_cluster ON ai_providers (cluster_id, created_at);
CREATE INDEX IF NOT EXISTS ai_profiles_cluster ON ai_profiles (cluster_id, created_at);
