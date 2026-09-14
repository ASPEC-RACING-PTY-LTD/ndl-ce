package appdb

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

func (p *Postgres) CreateIOSession(ctx context.Context, s IOSession) error {
	if s.CreatedAt.IsZero() {
		s.CreatedAt = time.Now().UTC()
	}
	if s.CWD == "" {
		s.CWD = "/"
	}
	if s.State == "" {
		s.State = IOStatePending
	}
	_, err := p.DB.ExecContext(ctx, `
INSERT INTO io_sessions (
  id, cluster_id, user_id, target_kind, target_id, kind, cwd, ticket_hash, state, reason,
  expires_at, connected_at, ended_at, created_at
) VALUES (
  $1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14
)`,
		s.ID, s.ClusterID, s.UserID, s.TargetKind, s.TargetID, s.Kind, s.CWD, s.TicketHash, s.State, s.Reason,
		s.ExpiresAt, s.ConnectedAt, s.EndedAt, s.CreatedAt)
	return err
}

func (p *Postgres) GetIOSession(ctx context.Context, clusterID, id string) (*IOSession, error) {
	row := p.DB.QueryRowContext(ctx, ioSessionSelect+` WHERE cluster_id=$1 AND id=$2`, clusterID, id)
	return scanIOSession(row)
}

func (p *Postgres) GetIOSessionByTicketHash(ctx context.Context, ticketHash string) (*IOSession, error) {
	if ticketHash == "" {
		return nil, nil
	}
	row := p.DB.QueryRowContext(ctx, ioSessionSelect+` WHERE ticket_hash=$1`, ticketHash)
	return scanIOSession(row)
}

func (p *Postgres) ListIOSessions(ctx context.Context, clusterID, userID string) ([]IOSession, error) {
	rows, err := p.DB.QueryContext(ctx, ioSessionSelect+`
WHERE cluster_id=$1 AND user_id=$2
ORDER BY created_at DESC
LIMIT 100`, clusterID, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []IOSession
	for rows.Next() {
		s, err := scanIOSession(rows)
		if err != nil {
			return nil, err
		}
		if s != nil {
			out = append(out, *s)
		}
	}
	return out, rows.Err()
}

func (p *Postgres) UpdateIOSession(ctx context.Context, s IOSession) error {
	_, err := p.DB.ExecContext(ctx, `
UPDATE io_sessions
SET state=$2, reason=$3, cwd=$4, connected_at=$5, ended_at=$6, expires_at=$7
WHERE id=$1`, s.ID, s.State, s.Reason, s.CWD, s.ConnectedAt, s.EndedAt, s.ExpiresAt)
	return err
}

const ioSessionSelect = `
SELECT id::text, cluster_id::text, user_id::text, target_kind, target_id::text, kind, cwd, ticket_hash,
       state, reason, expires_at, connected_at, ended_at, created_at
FROM io_sessions`

func scanIOSession(row interface {
	Scan(dest ...any) error
}) (*IOSession, error) {
	var s IOSession
	var connected, ended sql.NullTime
	if err := row.Scan(&s.ID, &s.ClusterID, &s.UserID, &s.TargetKind, &s.TargetID, &s.Kind, &s.CWD, &s.TicketHash,
		&s.State, &s.Reason, &s.ExpiresAt, &connected, &ended, &s.CreatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	if connected.Valid {
		s.ConnectedAt = &connected.Time
	}
	if ended.Valid {
		s.EndedAt = &ended.Time
	}
	return &s, nil
}
