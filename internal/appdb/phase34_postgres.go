package appdb

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/google/uuid"
)

func (p *Postgres) FenceLease(ctx context.Context, clusterID string, at time.Time) error {
	if at.IsZero() {
		at = time.Now().UTC().Add(-time.Second)
	}
	_, err := p.DB.ExecContext(ctx, `
UPDATE cluster_leases SET expires_at=$2, fenced=true WHERE cluster_id=$1`, clusterID, at)
	return err
}

func (p *Postgres) GetHAState(ctx context.Context, clusterID string) (*HAState, error) {
	row := p.DB.QueryRowContext(ctx, `
SELECT cluster_id::text, mode, replica_status, replica_endpoint, fencing_mode, fenced_holder,
       fenced_at, promoted_holder, promoted_at, reason, updated_at
FROM ha_state WHERE cluster_id=$1`, clusterID)
	var h HAState
	var fencedAt, promotedAt sql.NullTime
	if err := row.Scan(&h.ClusterID, &h.Mode, &h.ReplicaStatus, &h.ReplicaEndpoint, &h.FencingMode, &h.FencedHolder,
		&fencedAt, &h.PromotedHolder, &promotedAt, &h.Reason, &h.UpdatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	if fencedAt.Valid {
		t := fencedAt.Time.UTC()
		h.FencedAt = &t
	}
	if promotedAt.Valid {
		t := promotedAt.Time.UTC()
		h.PromotedAt = &t
	}
	return &h, nil
}

func (p *Postgres) UpsertHAState(ctx context.Context, h HAState) error {
	if h.Mode == "" {
		h.Mode = HAModeSingleWriter
	}
	if h.ReplicaStatus == "" {
		h.ReplicaStatus = HAReplicaNotConfigured
	}
	if h.FencingMode == "" {
		h.FencingMode = HAFencingOperator
	}
	if h.UpdatedAt.IsZero() {
		h.UpdatedAt = time.Now().UTC()
	}
	_, err := p.DB.ExecContext(ctx, `
INSERT INTO ha_state (cluster_id, mode, replica_status, replica_endpoint, fencing_mode, fenced_holder, fenced_at, promoted_holder, promoted_at, reason, updated_at)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)
ON CONFLICT (cluster_id) DO UPDATE SET
  mode=EXCLUDED.mode, replica_status=EXCLUDED.replica_status, replica_endpoint=EXCLUDED.replica_endpoint,
  fencing_mode=EXCLUDED.fencing_mode, fenced_holder=EXCLUDED.fenced_holder, fenced_at=EXCLUDED.fenced_at,
  promoted_holder=EXCLUDED.promoted_holder, promoted_at=EXCLUDED.promoted_at, reason=EXCLUDED.reason, updated_at=EXCLUDED.updated_at`,
		h.ClusterID, h.Mode, h.ReplicaStatus, h.ReplicaEndpoint, h.FencingMode, h.FencedHolder, h.FencedAt,
		h.PromotedHolder, h.PromotedAt, h.Reason, h.UpdatedAt)
	return err
}

func (p *Postgres) SetHAReplicaDSN(ctx context.Context, clusterID, dsn string) error {
	_, err := p.DB.ExecContext(ctx, `
INSERT INTO secrets.ha_credentials (cluster_id, replica_dsn, updated_at)
VALUES ($1,$2,now())
ON CONFLICT (cluster_id) DO UPDATE SET replica_dsn=EXCLUDED.replica_dsn, updated_at=now()`, clusterID, dsn)
	return err
}

func (p *Postgres) GetHAReplicaDSN(ctx context.Context, clusterID string) (string, error) {
	row := p.DB.QueryRowContext(ctx, `SELECT replica_dsn FROM secrets.ha_credentials WHERE cluster_id=$1`, clusterID)
	var dsn string
	if err := row.Scan(&dsn); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", nil
		}
		return "", err
	}
	return dsn, nil
}

func (p *Postgres) CreateRollingPlan(ctx context.Context, plan RollingPlan) error {
	if plan.ID == "" {
		plan.ID = uuid.NewString()
	}
	if plan.CreatedAt.IsZero() {
		plan.CreatedAt = time.Now().UTC()
	}
	_, err := p.DB.ExecContext(ctx, `
INSERT INTO rolling_plans (id, cluster_id, status, reason, created_at, finished_at)
VALUES ($1,$2,$3,$4,$5,$6)`, plan.ID, plan.ClusterID, plan.Status, plan.Reason, plan.CreatedAt, plan.FinishedAt)
	return err
}

func (p *Postgres) GetRollingPlan(ctx context.Context, clusterID, id string) (*RollingPlan, error) {
	row := p.DB.QueryRowContext(ctx, `
SELECT id::text, cluster_id::text, status, reason, created_at, finished_at
FROM rolling_plans WHERE cluster_id=$1 AND id=$2`, clusterID, id)
	return scanRollingPlan(row)
}

func (p *Postgres) LatestRollingPlan(ctx context.Context, clusterID string) (*RollingPlan, error) {
	row := p.DB.QueryRowContext(ctx, `
SELECT id::text, cluster_id::text, status, reason, created_at, finished_at
FROM rolling_plans WHERE cluster_id=$1 ORDER BY created_at DESC LIMIT 1`, clusterID)
	return scanRollingPlan(row)
}

func (p *Postgres) UpdateRollingPlan(ctx context.Context, plan RollingPlan) error {
	res, err := p.DB.ExecContext(ctx, `
UPDATE rolling_plans SET status=$3, reason=$4, finished_at=$5 WHERE cluster_id=$1 AND id=$2`,
		plan.ClusterID, plan.ID, plan.Status, plan.Reason, plan.FinishedAt)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return errors.New("rolling plan not found")
	}
	return nil
}

func (p *Postgres) CreateRollingStep(ctx context.Context, s RollingStep) error {
	if s.ID == "" {
		s.ID = uuid.NewString()
	}
	if s.CreatedAt.IsZero() {
		s.CreatedAt = time.Now().UTC()
	}
	_, err := p.DB.ExecContext(ctx, `
INSERT INTO rolling_steps (id, plan_id, cluster_id, node_id, ordinal, action, status, reason, update_operation_id, created_at)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`,
		s.ID, s.PlanID, s.ClusterID, s.NodeID, s.Ordinal, s.Action, s.Status, s.Reason, nullIfEmpty(s.UpdateOperationID), s.CreatedAt)
	return err
}

func (p *Postgres) ListRollingSteps(ctx context.Context, clusterID, planID string) ([]RollingStep, error) {
	rows, err := p.DB.QueryContext(ctx, `
SELECT id::text, plan_id::text, cluster_id::text, node_id::text, ordinal, action, status, reason, COALESCE(update_operation_id::text, ''), created_at
FROM rolling_steps WHERE cluster_id=$1 AND plan_id=$2 ORDER BY ordinal ASC`, clusterID, planID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []RollingStep
	for rows.Next() {
		var s RollingStep
		if err := rows.Scan(&s.ID, &s.PlanID, &s.ClusterID, &s.NodeID, &s.Ordinal, &s.Action, &s.Status, &s.Reason, &s.UpdateOperationID, &s.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func (p *Postgres) UpdateRollingStep(ctx context.Context, s RollingStep) error {
	_, err := p.DB.ExecContext(ctx, `
UPDATE rolling_steps SET status=$3, reason=$4, update_operation_id=$5 WHERE cluster_id=$1 AND id=$2`,
		s.ClusterID, s.ID, s.Status, s.Reason, nullIfEmpty(s.UpdateOperationID))
	return err
}

func scanRollingPlan(row *sql.Row) (*RollingPlan, error) {
	var p RollingPlan
	var finished sql.NullTime
	if err := row.Scan(&p.ID, &p.ClusterID, &p.Status, &p.Reason, &p.CreatedAt, &finished); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	if finished.Valid {
		t := finished.Time.UTC()
		p.FinishedAt = &t
	}
	return &p, nil
}
