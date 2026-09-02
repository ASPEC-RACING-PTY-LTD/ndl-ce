package appdb

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

func scanNode(row interface{ Scan(dest ...any) error }) (*Node, error) {
	var n Node
	var revoked sql.NullTime
	var plat []byte
	if err := row.Scan(&n.ID, &n.ClusterID, &n.Name, &plat, &n.Role, &n.Hostname, &revoked); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	n.HostPlatform = json.RawMessage(plat)
	if revoked.Valid {
		t := revoked.Time.UTC()
		n.RevokedAt = &t
	}
	return &n, nil
}

func (p *Postgres) GetNodeByID(ctx context.Context, clusterID, id string) (*Node, error) {
	row := p.DB.QueryRowContext(ctx, `
SELECT id::text, cluster_id::text, name, host_platform, COALESCE(role, 'control'), COALESCE(hostname, ''), revoked_at
FROM nodes WHERE cluster_id=$1 AND id=$2`, clusterID, id)
	return scanNode(row)
}

func (p *Postgres) ListClusterNodes(ctx context.Context, clusterID string) ([]Node, error) {
	rows, err := p.DB.QueryContext(ctx, `
SELECT id::text, cluster_id::text, name, host_platform, COALESCE(role, 'control'), COALESCE(hostname, ''), revoked_at
FROM nodes WHERE cluster_id=$1 ORDER BY enrolled_at ASC`, clusterID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Node
	for rows.Next() {
		n, err := scanNode(rows)
		if err != nil {
			return nil, err
		}
		if n != nil {
			out = append(out, *n)
		}
	}
	return out, rows.Err()
}

func (p *Postgres) RevokeNode(ctx context.Context, clusterID, id string, at time.Time) error {
	if at.IsZero() {
		at = time.Now().UTC()
	}
	res, err := p.DB.ExecContext(ctx, `UPDATE nodes SET revoked_at=$3 WHERE cluster_id=$1 AND id=$2 AND revoked_at IS NULL`, clusterID, id, at)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNodeNotFound
	}
	return nil
}

func (p *Postgres) CreateJoinToken(ctx context.Context, t JoinToken) error {
	_, err := p.DB.ExecContext(ctx, `
INSERT INTO join_tokens (id, cluster_id, token_hash, expires_at, created_at)
VALUES ($1,$2,$3,$4,$5)`,
		t.ID, t.ClusterID, t.TokenHash, t.ExpiresAt, t.CreatedAt)
	return err
}

func (p *Postgres) GetJoinTokenByHash(ctx context.Context, tokenHash string) (*JoinToken, error) {
	row := p.DB.QueryRowContext(ctx, `
SELECT id::text, cluster_id::text, token_hash, expires_at, consumed_at, COALESCE(consumed_node_id::text, ''), created_at
FROM join_tokens WHERE token_hash=$1`, tokenHash)
	return scanJoinToken(row)
}

func (p *Postgres) ConsumeJoinToken(ctx context.Context, tokenHash, nodeID string, at time.Time) (*JoinToken, error) {
	if at.IsZero() {
		at = time.Now().UTC()
	}
	row := p.DB.QueryRowContext(ctx, `
UPDATE join_tokens
SET consumed_at=$2, consumed_node_id=$3
WHERE token_hash=$1 AND consumed_at IS NULL AND expires_at > $2
RETURNING id::text, cluster_id::text, token_hash, expires_at, consumed_at, COALESCE(consumed_node_id::text, ''), created_at`,
		tokenHash, at, nodeID)
	tok, err := scanJoinToken(row)
	if err != nil {
		return nil, err
	}
	if tok != nil {
		return tok, nil
	}
	existing, err := p.GetJoinTokenByHash(ctx, tokenHash)
	if err != nil {
		return nil, err
	}
	if existing == nil {
		return nil, ErrJoinTokenInvalid
	}
	if existing.ConsumedAt != nil {
		return nil, ErrJoinTokenUsed
	}
	return nil, ErrJoinTokenInvalid
}

func scanJoinToken(row interface{ Scan(dest ...any) error }) (*JoinToken, error) {
	var t JoinToken
	var consumed sql.NullTime
	if err := row.Scan(&t.ID, &t.ClusterID, &t.TokenHash, &t.ExpiresAt, &consumed, &t.ConsumedNodeID, &t.CreatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	if consumed.Valid {
		c := consumed.Time.UTC()
		t.ConsumedAt = &c
	}
	return &t, nil
}

func (p *Postgres) AcquireLease(ctx context.Context, clusterID, holderID string, expiresAt time.Time) error {
	res, err := p.DB.ExecContext(ctx, `
INSERT INTO cluster_leases (cluster_id, holder_id, expires_at)
VALUES ($1,$2,$3)
ON CONFLICT (cluster_id) DO UPDATE SET
  holder_id = EXCLUDED.holder_id,
  expires_at = EXCLUDED.expires_at,
  fenced = false
WHERE cluster_leases.holder_id = EXCLUDED.holder_id
   OR cluster_leases.expires_at < now()
   OR cluster_leases.fenced = true`,
		clusterID, holderID, expiresAt)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrLeaseHeld
	}
	return nil
}

func (p *Postgres) GetClusterLease(ctx context.Context, clusterID string) (*ClusterLease, error) {
	row := p.DB.QueryRowContext(ctx, `SELECT cluster_id::text, holder_id, expires_at, COALESCE(fenced, false) FROM cluster_leases WHERE cluster_id=$1`, clusterID)
	var l ClusterLease
	if err := row.Scan(&l.ClusterID, &l.HolderID, &l.ExpiresAt, &l.Fenced); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &l, nil
}
