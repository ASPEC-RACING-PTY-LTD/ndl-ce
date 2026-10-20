-- Appearance and search settings, update impact notices, the rollback
-- window and profile fields.

-- One row per cluster. appearance holds the global theme, themes per role
-- and group, the default navigation and Instance layouts, and the design
-- profile limit. search holds indexing scope and custom filters.
CREATE TABLE IF NOT EXISTS ui_settings (
    cluster_id uuid PRIMARY KEY REFERENCES clusters (id),
    appearance jsonb NOT NULL DEFAULT '{}'::jsonb,
    search jsonb NOT NULL DEFAULT '{}'::jsonb,
    updated_by text NOT NULL DEFAULT '',
    updated_at timestamptz NOT NULL DEFAULT now()
);

-- Impact notices a release declared, once delivered to the people they
-- concern.
CREATE TABLE IF NOT EXISTS update_notices_delivered (
    cluster_id uuid NOT NULL REFERENCES clusters (id),
    notice_id text NOT NULL,
    delivered_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (cluster_id, notice_id)
);

-- The checkpoint an apply (or a rollback) took is wiped 3 days later, or
-- sooner by a SuperAdmin. After that it cannot be rolled back (or undone).
-- restored_db records that a rollback restored the database, so undoing it
-- restores the copy saved right before the rollback.
ALTER TABLE update_operations
    ADD COLUMN IF NOT EXISTS checkpoint_wiped_at timestamptz,
    ADD COLUMN IF NOT EXISTS checkpoint_wiped_by text NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS restored_db boolean NOT NULL DEFAULT false;

-- Social profile: shown to other signed-in people.
ALTER TABLE account_profiles
    ADD COLUMN IF NOT EXISTS bio text NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS avatar_url text NOT NULL DEFAULT '';

-- Global search. scope is 'primary' (names and basic details, always on)
-- or 'deep' (file names and other bulky sources, opt in). permission is the
-- grant a reader needs; link is where the result opens.
CREATE TABLE IF NOT EXISTS search_documents (
    cluster_id uuid NOT NULL REFERENCES clusters (id),
    kind text NOT NULL,
    ref_id text NOT NULL,
    scope text NOT NULL DEFAULT 'primary',
    title text NOT NULL,
    subtitle text NOT NULL DEFAULT '',
    body text NOT NULL DEFAULT '',
    link text NOT NULL,
    node_id text NOT NULL DEFAULT '',
    status text NOT NULL DEFAULT '',
    permission text NOT NULL DEFAULT '',
    updated_at timestamptz NOT NULL DEFAULT now(),
    tsv tsvector GENERATED ALWAYS AS (
        setweight(to_tsvector('simple', coalesce(title, '')), 'A') ||
        setweight(to_tsvector('simple', coalesce(subtitle, '')), 'B') ||
        setweight(to_tsvector('simple', coalesce(body, '')), 'C')) STORED,
    PRIMARY KEY (cluster_id, kind, ref_id)
);
CREATE INDEX IF NOT EXISTS search_documents_tsv_idx ON search_documents USING gin (tsv);
CREATE INDEX IF NOT EXISTS search_documents_title_idx ON search_documents (cluster_id, lower(title) text_pattern_ops);

-- Indexing runs. cursor is where a run carries on after a restart; it
-- re-indexes the last batch before the cursor to be safe.
CREATE TABLE IF NOT EXISTS search_jobs (
    id uuid PRIMARY KEY,
    cluster_id uuid NOT NULL REFERENCES clusters (id),
    scope text NOT NULL,
    status text NOT NULL,
    total bigint NOT NULL DEFAULT 0,
    done bigint NOT NULL DEFAULT 0,
    cursor jsonb NOT NULL DEFAULT '{}'::jsonb,
    error text NOT NULL DEFAULT '',
    started_by text NOT NULL DEFAULT '',
    started_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    finished_at timestamptz
);
CREATE INDEX IF NOT EXISTS search_jobs_cluster_idx ON search_jobs (cluster_id, started_at DESC);

-- Settings templates for instance types other than VMs (VM templates keep
-- their disk snapshot in vm_templates). spec holds the create-form fields.
CREATE TABLE IF NOT EXISTS instance_templates (
    id uuid PRIMARY KEY,
    cluster_id uuid NOT NULL REFERENCES clusters (id),
    kind text NOT NULL,
    name text NOT NULL,
    description text NOT NULL DEFAULT '',
    spec jsonb NOT NULL DEFAULT '{}'::jsonb,
    source_workload_id text NOT NULL DEFAULT '',
    created_by text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS instance_templates_cluster_idx ON instance_templates (cluster_id, kind, lower(name));
