-- API tokens are hard-deleted when deleted or expired. Their history lives
-- in the audit log. api_token_usage records each source IP that used a token
-- while it exists; it goes with the token.
CREATE TABLE IF NOT EXISTS api_token_usage (
    token_id uuid NOT NULL REFERENCES api_tokens (id) ON DELETE CASCADE,
    ip text NOT NULL,
    first_at timestamptz NOT NULL,
    last_at timestamptz NOT NULL,
    count bigint NOT NULL DEFAULT 1,
    PRIMARY KEY (token_id, ip)
);

-- Tokens that were revoked or expired before this change: record each in the
-- audit log, then delete it.
INSERT INTO audit_events (id, cluster_id, actor_user_id, action, result, remote_addr, detail, created_at)
SELECT md5(random()::text || t.id::text)::uuid, t.cluster_id, NULL,
       CASE WHEN t.revoked_at IS NOT NULL THEN 'token.delete' ELSE 'token.expire' END,
       'ok', 'internal',
       jsonb_build_object(
           'detail', t.id::text,
           'name', t.name,
           'owner', t.user_id::text,
           'created_at', t.created_at,
           'last_used_at', t.last_used_at,
           'ended_at', COALESCE(t.revoked_at, t.expires_at),
           'note', 'removed when revoked and expired tokens became hard deletes'),
       now()
FROM api_tokens t
WHERE t.revoked_at IS NOT NULL OR (t.expires_at IS NOT NULL AND t.expires_at <= now());

DELETE FROM api_tokens WHERE revoked_at IS NOT NULL OR (expires_at IS NOT NULL AND expires_at <= now());
