-- Saved choices for Docker Management: containers or Compose projects an
-- operator marked as ignored, so known-noisy health stops counting as an
-- issue. Keyed by name, which survives container recreation.
CREATE TABLE IF NOT EXISTS docker_prefs (
    cluster_id uuid NOT NULL REFERENCES clusters (id),
    machine_id text NOT NULL,
    scope text NOT NULL,
    name text NOT NULL,
    ignored boolean NOT NULL DEFAULT false,
    note text NOT NULL DEFAULT '',
    updated_by text NOT NULL DEFAULT '',
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (cluster_id, machine_id, scope, name)
);
