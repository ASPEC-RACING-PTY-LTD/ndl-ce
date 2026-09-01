package appdb

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"

	"github.com/google/uuid"
)

// Postgres implements Store on PostgreSQL 16.
type Postgres struct {
	DB *sql.DB
}

func (p *Postgres) GetCluster(ctx context.Context) (*Cluster, error) {
	row := p.DB.QueryRowContext(ctx, `SELECT id::text, name, setup_completed_at FROM clusters LIMIT 1`)
	var c Cluster
	var completed sql.NullTime
	if err := row.Scan(&c.ID, &c.Name, &completed); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	if completed.Valid {
		c.SetupCompletedAt = &completed.Time
	}
	return &c, nil
}

func (p *Postgres) CreateCluster(ctx context.Context, c Cluster) error {
	_, err := p.DB.ExecContext(ctx, `INSERT INTO clusters (id, name) VALUES ($1, $2)`, c.ID, c.Name)
	return err
}

func (p *Postgres) CompleteSetup(ctx context.Context, clusterID string) error {
	_, err := p.DB.ExecContext(ctx, `UPDATE clusters SET setup_completed_at = now() WHERE id = $1`, clusterID)
	return err
}

func (p *Postgres) GetSetup(ctx context.Context) (*SetupToken, error) {
	row := p.DB.QueryRowContext(ctx, `SELECT cluster_id::text, token_hash, consumed_at FROM secrets.setup_tokens LIMIT 1`)
	var s SetupToken
	var consumed sql.NullTime
	if err := row.Scan(&s.ClusterID, &s.TokenHash, &consumed); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	if consumed.Valid {
		s.ConsumedAt = &consumed.Time
	}
	return &s, nil
}

func (p *Postgres) PutSetup(ctx context.Context, clusterID, tokenHash string) error {
	_, err := p.DB.ExecContext(ctx, `
INSERT INTO secrets.setup_tokens (cluster_id, token_hash)
VALUES ($1, $2)
ON CONFLICT (cluster_id) DO NOTHING`, clusterID, tokenHash)
	return err
}

func (p *Postgres) ConsumeSetup(ctx context.Context, clusterID string) error {
	res, err := p.DB.ExecContext(ctx, `
UPDATE secrets.setup_tokens
SET consumed_at = now()
WHERE cluster_id = $1 AND consumed_at IS NULL`, clusterID)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return errors.New("setup already claimed")
	}
	return nil
}

func (p *Postgres) CreateUser(ctx context.Context, u User) error {
	if u.Kind == "" {
		u.Kind = UserKindPerson
	}
	_, err := p.DB.ExecContext(ctx, `INSERT INTO users (id, cluster_id, username, password_hash, kind) VALUES ($1,$2,$3,$4,$5)`,
		u.ID, u.ClusterID, u.Username, u.PasswordHash, u.Kind)
	return err
}

func (p *Postgres) GetUserByName(ctx context.Context, clusterID, username string) (*User, error) {
	row := p.DB.QueryRowContext(ctx, `SELECT id::text, cluster_id::text, username, password_hash, COALESCE(kind, 'person') FROM users WHERE cluster_id=$1 AND username=$2`, clusterID, username)
	return scanUser(row)
}

func (p *Postgres) GetUser(ctx context.Context, id string) (*User, error) {
	row := p.DB.QueryRowContext(ctx, `SELECT id::text, cluster_id::text, username, password_hash, COALESCE(kind, 'person') FROM users WHERE id=$1`, id)
	return scanUser(row)
}

func scanUser(row *sql.Row) (*User, error) {
	var u User
	if err := row.Scan(&u.ID, &u.ClusterID, &u.Username, &u.PasswordHash, &u.Kind); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	if u.Kind == "" {
		u.Kind = UserKindPerson
	}
	return &u, nil
}

func (p *Postgres) UpdatePassword(ctx context.Context, userID, passwordHash string) error {
	_, err := p.DB.ExecContext(ctx, `UPDATE users SET password_hash=$2 WHERE id=$1`, userID, passwordHash)
	return err
}

func (p *Postgres) CountAdmins(ctx context.Context, clusterID string) (int, error) {
	row := p.DB.QueryRowContext(ctx, `
SELECT count(*)
FROM role_bindings b
JOIN roles r ON r.id = b.role_id
WHERE b.cluster_id = $1 AND r.name = 'admin'`, clusterID)
	var n int
	if err := row.Scan(&n); err != nil {
		return 0, err
	}
	return n, nil
}

func (p *Postgres) EnsureRoles(ctx context.Context, clusterID string, roles map[string][]string) error {
	for name, perms := range roles {
		_, err := p.DB.ExecContext(ctx, `
INSERT INTO roles (id, cluster_id, name, permissions)
VALUES ($1, $2, $3, string_to_array($4, ','))
ON CONFLICT (cluster_id, name) DO UPDATE SET permissions = EXCLUDED.permissions`,
			uuid.NewString(), clusterID, name, strings.Join(perms, ","))
		if err != nil {
			return err
		}
	}
	return nil
}

func (p *Postgres) BindRole(ctx context.Context, clusterID, userID, roleName string) error {
	_, err := p.DB.ExecContext(ctx, `
INSERT INTO role_bindings (id, cluster_id, user_id, role_id)
SELECT $4, $1, $2, r.id
FROM roles r
WHERE r.cluster_id = $1 AND r.name = $3
ON CONFLICT (user_id, role_id) DO NOTHING`, clusterID, userID, roleName, uuid.NewString())
	return err
}

func (p *Postgres) UserRoles(ctx context.Context, userID string) ([]string, error) {
	rows, err := p.DB.QueryContext(ctx, `
SELECT r.name
FROM role_bindings b
JOIN roles r ON r.id = b.role_id
WHERE b.user_id = $1
UNION
SELECT r.name
FROM group_members m
JOIN group_role_bindings gb ON gb.group_id = m.group_id
JOIN roles r ON r.id = gb.role_id
WHERE m.user_id = $1`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		out = append(out, name)
	}
	return out, rows.Err()
}

func (p *Postgres) CreateSession(ctx context.Context, s Session) error {
	if s.AAL <= 0 {
		s.AAL = 1
	}
	_, err := p.DB.ExecContext(ctx, `INSERT INTO sessions (id, cluster_id, user_id, token_hash, expires_at, aal) VALUES ($1,$2,$3,$4,$5,$6)`,
		s.ID, s.ClusterID, s.UserID, s.TokenHash, s.ExpiresAt, s.AAL)
	return err
}

func (p *Postgres) GetSessionByHash(ctx context.Context, hash string) (*Session, error) {
	row := p.DB.QueryRowContext(ctx, `SELECT id::text, cluster_id::text, user_id::text, token_hash, expires_at, revoked_at, aal FROM sessions WHERE token_hash=$1`, hash)
	var s Session
	var revoked sql.NullTime
	if err := row.Scan(&s.ID, &s.ClusterID, &s.UserID, &s.TokenHash, &s.ExpiresAt, &revoked, &s.AAL); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	if revoked.Valid {
		s.RevokedAt = &revoked.Time
	}
	if s.AAL <= 0 {
		s.AAL = 1
	}
	return &s, nil
}

func (p *Postgres) RevokeSession(ctx context.Context, id string) error {
	_, err := p.DB.ExecContext(ctx, `UPDATE sessions SET revoked_at=now() WHERE id=$1`, id)
	return err
}

func (p *Postgres) RevokeUserSessions(ctx context.Context, userID string) error {
	_, err := p.DB.ExecContext(ctx, `UPDATE sessions SET revoked_at=now() WHERE user_id=$1 AND revoked_at IS NULL`, userID)
	return err
}

func (p *Postgres) CreateToken(ctx context.Context, t APIToken) error {
	_, err := p.DB.ExecContext(ctx, `INSERT INTO api_tokens (id, cluster_id, user_id, name, token_hash, prefix, permissions) VALUES ($1,$2,$3,$4,$5,$6,COALESCE(string_to_array(NULLIF($7, ''), ','), '{}'))`,
		t.ID, t.ClusterID, t.UserID, t.Name, t.TokenHash, t.Prefix, strings.Join(t.Permissions, ","))
	return err
}

func (p *Postgres) GetTokenByHash(ctx context.Context, hash string) (*APIToken, error) {
	row := p.DB.QueryRowContext(ctx, `SELECT id::text, cluster_id::text, user_id::text, name, token_hash, prefix, revoked_at, COALESCE(array_to_string(permissions, ','), '') FROM api_tokens WHERE token_hash=$1`, hash)
	var t APIToken
	var revoked sql.NullTime
	var permCSV string
	if err := row.Scan(&t.ID, &t.ClusterID, &t.UserID, &t.Name, &t.TokenHash, &t.Prefix, &revoked, &permCSV); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	if revoked.Valid {
		t.RevokedAt = &revoked.Time
	}
	if permCSV != "" {
		t.Permissions = strings.Split(permCSV, ",")
	}
	return &t, nil
}

func (p *Postgres) RevokeToken(ctx context.Context, id, userID string) error {
	res, err := p.DB.ExecContext(ctx, `UPDATE api_tokens SET revoked_at=now() WHERE id=$1 AND user_id=$2 AND revoked_at IS NULL`, id, userID)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return errors.New("token not found")
	}
	return nil
}

func (p *Postgres) UpsertNode(ctx context.Context, n Node) error {
	_, err := p.DB.ExecContext(ctx, `
INSERT INTO nodes (id, cluster_id, name, host_platform)
VALUES ($1,$2,$3,$4)
ON CONFLICT (id) DO UPDATE SET host_platform = EXCLUDED.host_platform`,
		n.ID, n.ClusterID, n.Name, n.HostPlatform)
	return err
}

func (p *Postgres) GetNode(ctx context.Context, clusterID string) (*Node, error) {
	row := p.DB.QueryRowContext(ctx, `SELECT id::text, cluster_id::text, name, host_platform FROM nodes WHERE cluster_id=$1 LIMIT 1`, clusterID)
	var n Node
	if err := row.Scan(&n.ID, &n.ClusterID, &n.Name, &n.HostPlatform); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &n, nil
}

func (p *Postgres) InsertAudit(ctx context.Context, e AuditEvent) error {
	if len(e.Detail) == 0 {
		e.Detail = json.RawMessage(`{}`)
	}
	actor := any(nil)
	if e.ActorUserID != "" {
		actor = e.ActorUserID
	}
	cluster := any(nil)
	if e.ClusterID != "" {
		cluster = e.ClusterID
	}
	_, err := p.DB.ExecContext(ctx, `
INSERT INTO audit_events (id, cluster_id, actor_user_id, action, result, remote_addr, detail)
VALUES ($1,$2,$3,$4,$5,$6,$7)`,
		e.ID, cluster, actor, e.Action, e.Result, nullIfEmpty(e.RemoteAddr), e.Detail)
	return err
}

func nullIfEmpty(s string) any {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	return s
}
