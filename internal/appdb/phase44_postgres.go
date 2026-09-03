package appdb

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
)

func (p *Postgres) CreateMigrationSource(ctx context.Context, src MigrationSource, token, username string, extra []byte) error {
	tx, err := p.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if src.ID == "" {
		src.ID = uuid.NewString()
	}
	if extra == nil {
		extra = []byte(`{}`)
	}
	if _, err := tx.ExecContext(ctx, `
INSERT INTO migration_sources (id, cluster_id, adapter, label, endpoint, insecure, created_at, updated_at)
VALUES ($1,$2,$3,$4,$5,$6,now(),now())`,
		src.ID, src.ClusterID, src.Adapter, src.Label, src.Endpoint, src.Insecure); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
INSERT INTO secrets.migration_source_credentials (source_id, token, username, extra, updated_at)
VALUES ($1,$2,$3,$4,now())`, src.ID, token, username, extra); err != nil {
		return err
	}
	return tx.Commit()
}

func (p *Postgres) ListMigrationSources(ctx context.Context, clusterID string) ([]MigrationSource, error) {
	rows, err := p.DB.QueryContext(ctx, `
SELECT id, cluster_id, adapter, label, endpoint, insecure, created_at, updated_at
FROM migration_sources WHERE cluster_id=$1 ORDER BY created_at`, clusterID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []MigrationSource
	for rows.Next() {
		var s MigrationSource
		if err := rows.Scan(&s.ID, &s.ClusterID, &s.Adapter, &s.Label, &s.Endpoint, &s.Insecure, &s.CreatedAt, &s.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func (p *Postgres) GetMigrationSource(ctx context.Context, clusterID, id string) (*MigrationSource, string, string, []byte, error) {
	row := p.DB.QueryRowContext(ctx, `
SELECT id, cluster_id, adapter, label, endpoint, insecure, created_at, updated_at
FROM migration_sources WHERE cluster_id=$1 AND id=$2`, clusterID, id)
	var s MigrationSource
	err := row.Scan(&s.ID, &s.ClusterID, &s.Adapter, &s.Label, &s.Endpoint, &s.Insecure, &s.CreatedAt, &s.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, "", "", nil, nil
	}
	if err != nil {
		return nil, "", "", nil, err
	}
	cred := p.DB.QueryRowContext(ctx, `SELECT token, username, extra FROM secrets.migration_source_credentials WHERE source_id=$1`, id)
	var token, username string
	var extra []byte
	if err := cred.Scan(&token, &username, &extra); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, "", "", nil, err
	}
	return &s, token, username, extra, nil
}

func (p *Postgres) DeleteMigrationSource(ctx context.Context, clusterID, id string) error {
	res, err := p.DB.ExecContext(ctx, `DELETE FROM migration_sources WHERE cluster_id=$1 AND id=$2`, clusterID, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return errors.New("migration source not found")
	}
	return nil
}

func (p *Postgres) CreateMigrationJob(ctx context.Context, j MigrationJob) error {
	if j.PlanJSON == nil {
		j.PlanJSON = json.RawMessage(`{}`)
	}
	if j.StatusJSON == nil {
		j.StatusJSON = json.RawMessage(`{}`)
	}
	var source any
	if j.SourceID != "" {
		source = j.SourceID
	}
	var op any
	if j.OperationID != "" {
		op = j.OperationID
	}
	_, err := p.DB.ExecContext(ctx, `
INSERT INTO migration_jobs (id, cluster_id, source_id, operation_id, adapter, direction, state, stage, plan_json, status_json, cancel_requested, created_at, updated_at)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,now(),now())`,
		j.ID, j.ClusterID, source, op, j.Adapter, j.Direction, j.State, j.Stage, j.PlanJSON, j.StatusJSON, j.CancelRequested)
	return err
}

func (p *Postgres) ListMigrationJobs(ctx context.Context, clusterID string, limit int) ([]MigrationJob, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := p.DB.QueryContext(ctx, `
SELECT id, cluster_id, COALESCE(source_id::text,''), COALESCE(operation_id::text,''), adapter, direction, state, stage, plan_json, status_json, cancel_requested, created_at, updated_at
FROM migration_jobs WHERE cluster_id=$1 ORDER BY created_at DESC LIMIT $2`, clusterID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []MigrationJob
	for rows.Next() {
		var j MigrationJob
		if err := rows.Scan(&j.ID, &j.ClusterID, &j.SourceID, &j.OperationID, &j.Adapter, &j.Direction, &j.State, &j.Stage, &j.PlanJSON, &j.StatusJSON, &j.CancelRequested, &j.CreatedAt, &j.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, j)
	}
	return out, rows.Err()
}

func (p *Postgres) GetMigrationJob(ctx context.Context, clusterID, id string) (*MigrationJob, error) {
	row := p.DB.QueryRowContext(ctx, `
SELECT id, cluster_id, COALESCE(source_id::text,''), COALESCE(operation_id::text,''), adapter, direction, state, stage, plan_json, status_json, cancel_requested, created_at, updated_at
FROM migration_jobs WHERE cluster_id=$1 AND id=$2`, clusterID, id)
	var j MigrationJob
	err := row.Scan(&j.ID, &j.ClusterID, &j.SourceID, &j.OperationID, &j.Adapter, &j.Direction, &j.State, &j.Stage, &j.PlanJSON, &j.StatusJSON, &j.CancelRequested, &j.CreatedAt, &j.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &j, nil
}

func (p *Postgres) UpdateMigrationJob(ctx context.Context, j MigrationJob) error {
	if j.PlanJSON == nil {
		j.PlanJSON = json.RawMessage(`{}`)
	}
	if j.StatusJSON == nil {
		j.StatusJSON = json.RawMessage(`{}`)
	}
	_, err := p.DB.ExecContext(ctx, `
UPDATE migration_jobs SET state=$3, stage=$4, plan_json=$5, status_json=$6, cancel_requested=$7, updated_at=$8
WHERE cluster_id=$1 AND id=$2`,
		j.ClusterID, j.ID, j.State, j.Stage, j.PlanJSON, j.StatusJSON, j.CancelRequested, time.Now().UTC())
	return err
}
