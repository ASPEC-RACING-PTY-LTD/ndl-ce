CREATE TABLE user_prefs (
    user_id uuid PRIMARY KEY REFERENCES users (id) ON DELETE CASCADE,
    cluster_id uuid NOT NULL REFERENCES clusters (id),
    ux_level text NOT NULL DEFAULT 'guided' CHECK (ux_level IN ('guided', 'advanced', 'expert')),
    expert_ack_at timestamptz,
    updated_at timestamptz NOT NULL DEFAULT now()
);
