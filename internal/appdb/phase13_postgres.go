package appdb

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

func (p *Postgres) DeleteUserMFA(ctx context.Context, userID string) error {
	_, err := p.DB.ExecContext(ctx, `DELETE FROM mfa_methods WHERE user_id=$1`, userID)
	return err
}

func (p *Postgres) ListAuditEvents(ctx context.Context, clusterID string, limit int) ([]AuditEvent, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := p.DB.QueryContext(ctx, `
SELECT id::text, COALESCE(cluster_id::text, ''), COALESCE(actor_user_id::text, ''), action, result, COALESCE(remote_addr, ''), detail, created_at
FROM audit_events WHERE cluster_id=$1 OR ($1='' AND cluster_id IS NULL)
ORDER BY created_at DESC LIMIT $2`, clusterID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AuditEvent
	for rows.Next() {
		var e AuditEvent
		if err := rows.Scan(&e.ID, &e.ClusterID, &e.ActorUserID, &e.Action, &e.Result, &e.RemoteAddr, &e.Detail, &e.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func (p *Postgres) CreateGroup(ctx context.Context, g Group) error {
	if g.ID == "" {
		g.ID = uuid.NewString()
	}
	if g.CreatedAt.IsZero() {
		g.CreatedAt = time.Now().UTC()
	}
	_, err := p.DB.ExecContext(ctx, `INSERT INTO groups (id, cluster_id, name, created_at) VALUES ($1,$2,$3,$4)`,
		g.ID, g.ClusterID, g.Name, g.CreatedAt)
	return err
}

func (p *Postgres) ListGroups(ctx context.Context, clusterID string) ([]Group, error) {
	rows, err := p.DB.QueryContext(ctx, `SELECT id::text, cluster_id::text, name, created_at FROM groups WHERE cluster_id=$1 ORDER BY name`, clusterID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Group
	for rows.Next() {
		var g Group
		if err := rows.Scan(&g.ID, &g.ClusterID, &g.Name, &g.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

func (p *Postgres) GetGroup(ctx context.Context, clusterID, id string) (*Group, error) {
	row := p.DB.QueryRowContext(ctx, `SELECT id::text, cluster_id::text, name, created_at FROM groups WHERE cluster_id=$1 AND id=$2`, clusterID, id)
	var g Group
	if err := row.Scan(&g.ID, &g.ClusterID, &g.Name, &g.CreatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &g, nil
}

func (p *Postgres) AddGroupMember(ctx context.Context, clusterID, groupID, userID string) error {
	g, err := p.GetGroup(ctx, clusterID, groupID)
	if err != nil {
		return err
	}
	if g == nil {
		return fmt.Errorf("group not found")
	}
	_, err = p.DB.ExecContext(ctx, `
INSERT INTO group_members (group_id, user_id) VALUES ($1,$2)
ON CONFLICT DO NOTHING`, groupID, userID)
	if err != nil {
		return err
	}
	members, err := p.ListGroupMembers(ctx, clusterID, groupID)
	if err != nil {
		return err
	}
	for _, id := range members {
		if id == userID {
			return nil
		}
	}
	return fmt.Errorf("group member not found")
}

func (p *Postgres) ListGroupMembers(ctx context.Context, clusterID, groupID string) ([]string, error) {
	rows, err := p.DB.QueryContext(ctx, `
SELECT m.user_id::text FROM group_members m
JOIN groups g ON g.id = m.group_id
WHERE g.cluster_id=$1 AND m.group_id=$2`, clusterID, groupID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

func (p *Postgres) BindGroupRole(ctx context.Context, clusterID, groupID, roleName string) error {
	if roleName == "admin" {
		return fmt.Errorf("groups cannot grant admin")
	}
	g, err := p.GetGroup(ctx, clusterID, groupID)
	if err != nil {
		return err
	}
	if g == nil {
		return fmt.Errorf("group not found")
	}
	_, err = p.DB.ExecContext(ctx, `
INSERT INTO group_role_bindings (group_id, role_id)
SELECT $1, r.id FROM roles r
WHERE r.cluster_id=$2 AND r.name=$3
ON CONFLICT DO NOTHING`, groupID, clusterID, roleName)
	if err != nil {
		return err
	}
	roles, err := p.ListGroupRoles(ctx, clusterID, groupID)
	if err != nil {
		return err
	}
	for _, name := range roles {
		if name == roleName {
			return nil
		}
	}
	return fmt.Errorf("group role not found")
}

func (p *Postgres) ListGroupRoles(ctx context.Context, clusterID, groupID string) ([]string, error) {
	g, err := p.GetGroup(ctx, clusterID, groupID)
	if err != nil {
		return nil, err
	}
	if g == nil {
		return nil, fmt.Errorf("group not found")
	}
	rows, err := p.DB.QueryContext(ctx, `
SELECT r.name
FROM group_role_bindings gb
JOIN roles r ON r.id = gb.role_id
WHERE gb.group_id=$1 AND r.cluster_id=$2
ORDER BY r.name`, groupID, clusterID)
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

func (p *Postgres) UpsertMFAMethod(ctx context.Context, method MFAMethod, totpSecret string, recoveryHashes []string) error {
	if method.ID == "" {
		method.ID = uuid.NewString()
	}
	if method.CreatedAt.IsZero() {
		method.CreatedAt = time.Now().UTC()
	}
	tx, err := p.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	_, err = tx.ExecContext(ctx, `DELETE FROM mfa_methods WHERE user_id=$1`, method.UserID)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO mfa_methods (id, cluster_id, user_id, kind, enabled, created_at) VALUES ($1,$2,$3,$4,$5,$6)`,
		method.ID, method.ClusterID, method.UserID, method.Kind, method.Enabled, method.CreatedAt)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `
INSERT INTO secrets.mfa_secrets (method_id, totp_secret, recovery_hashes, updated_at)
VALUES ($1,$2,COALESCE(string_to_array(NULLIF($3, ''), ','), '{}'),$4)`,
		method.ID, totpSecret, strings.Join(recoveryHashes, ","), method.CreatedAt)
	if err != nil {
		return err
	}
	return tx.Commit()
}

func (p *Postgres) GetMFAMethod(ctx context.Context, userID string) (*MFAMethod, string, []string, error) {
	row := p.DB.QueryRowContext(ctx, `
SELECT m.id::text, m.cluster_id::text, m.user_id::text, m.kind, m.enabled, m.created_at, s.totp_secret, COALESCE(array_to_string(s.recovery_hashes, ','), '')
FROM mfa_methods m
JOIN secrets.mfa_secrets s ON s.method_id = m.id
WHERE m.user_id=$1`, userID)
	var method MFAMethod
	var secret, hashCSV string
	if err := row.Scan(&method.ID, &method.ClusterID, &method.UserID, &method.Kind, &method.Enabled, &method.CreatedAt, &secret, &hashCSV); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, "", nil, nil
		}
		return nil, "", nil, err
	}
	var hashes []string
	if hashCSV != "" {
		hashes = strings.Split(hashCSV, ",")
	}
	return &method, secret, hashes, nil
}

func (p *Postgres) EnableMFAMethod(ctx context.Context, userID string) error {
	res, err := p.DB.ExecContext(ctx, `UPDATE mfa_methods SET enabled=true WHERE user_id=$1`, userID)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return errors.New("mfa method not found")
	}
	return nil
}

func (p *Postgres) ConsumeRecoveryHash(ctx context.Context, userID, hash string) error {
	res, err := p.DB.ExecContext(ctx, `
UPDATE secrets.mfa_secrets s
SET recovery_hashes = array_remove(s.recovery_hashes, $2), updated_at=now()
FROM mfa_methods m
WHERE s.method_id=m.id AND m.user_id=$1 AND $2 = ANY(s.recovery_hashes)`, userID, hash)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return errors.New("recovery code is invalid")
	}
	return nil
}

func (p *Postgres) CreateMFAChallenge(ctx context.Context, c MFAChallenge) error {
	if c.ID == "" {
		c.ID = uuid.NewString()
	}
	_, err := p.DB.ExecContext(ctx, `INSERT INTO mfa_challenges (id, cluster_id, user_id, token_hash, expires_at) VALUES ($1,$2,$3,$4,$5)`,
		c.ID, c.ClusterID, c.UserID, c.TokenHash, c.ExpiresAt)
	return err
}

func (p *Postgres) GetMFAChallengeByHash(ctx context.Context, hash string) (*MFAChallenge, error) {
	row := p.DB.QueryRowContext(ctx, `SELECT id::text, cluster_id::text, user_id::text, token_hash, expires_at, consumed_at FROM mfa_challenges WHERE token_hash=$1`, hash)
	var c MFAChallenge
	var consumed sql.NullTime
	if err := row.Scan(&c.ID, &c.ClusterID, &c.UserID, &c.TokenHash, &c.ExpiresAt, &consumed); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	if consumed.Valid {
		t := consumed.Time.UTC()
		c.ConsumedAt = &t
	}
	return &c, nil
}

func (p *Postgres) ConsumeMFAChallenge(ctx context.Context, id string) error {
	res, err := p.DB.ExecContext(ctx, `UPDATE mfa_challenges SET consumed_at=now() WHERE id=$1 AND consumed_at IS NULL`, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return errors.New("mfa challenge is invalid")
	}
	return nil
}

func (p *Postgres) CreateServicePrincipal(ctx context.Context, sp ServicePrincipal) error {
	if sp.ID == "" {
		sp.ID = uuid.NewString()
	}
	if sp.CreatedAt.IsZero() {
		sp.CreatedAt = time.Now().UTC()
	}
	_, err := p.DB.ExecContext(ctx, `INSERT INTO service_principals (id, cluster_id, user_id, name, created_at) VALUES ($1,$2,$3,$4,$5)`,
		sp.ID, sp.ClusterID, sp.UserID, sp.Name, sp.CreatedAt)
	return err
}

func (p *Postgres) ListServicePrincipals(ctx context.Context, clusterID string) ([]ServicePrincipal, error) {
	rows, err := p.DB.QueryContext(ctx, `SELECT id::text, cluster_id::text, user_id::text, name, created_at FROM service_principals WHERE cluster_id=$1 ORDER BY created_at, id`, clusterID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ServicePrincipal
	for rows.Next() {
		var sp ServicePrincipal
		if err := rows.Scan(&sp.ID, &sp.ClusterID, &sp.UserID, &sp.Name, &sp.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, sp)
	}
	return out, rows.Err()
}

func (p *Postgres) GetVolumeEncryption(ctx context.Context, clusterID, volumeID string) (*VolumeEncryption, error) {
	row := p.DB.QueryRowContext(ctx, `SELECT volume_id::text, cluster_id::text, encrypted, encryption_kind FROM volume_encryption WHERE cluster_id=$1 AND volume_id=$2`, clusterID, volumeID)
	var e VolumeEncryption
	if err := row.Scan(&e.VolumeID, &e.ClusterID, &e.Encrypted, &e.EncryptionKind); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return &VolumeEncryption{VolumeID: volumeID, ClusterID: clusterID, EncryptionKind: EncryptionNone}, nil
		}
		return nil, err
	}
	return &e, nil
}

func (p *Postgres) UpsertVolumeEncryption(ctx context.Context, e VolumeEncryption) error {
	_, err := p.DB.ExecContext(ctx, `
INSERT INTO volume_encryption (volume_id, cluster_id, encrypted, encryption_kind)
VALUES ($1,$2,$3,$4)
ON CONFLICT (volume_id) DO UPDATE SET encrypted=EXCLUDED.encrypted, encryption_kind=EXCLUDED.encryption_kind`,
		e.VolumeID, e.ClusterID, e.Encrypted, e.EncryptionKind)
	return err
}
