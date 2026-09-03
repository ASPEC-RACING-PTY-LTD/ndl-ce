package appdb

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
)

func (p *Postgres) CreateUpdateOperation(ctx context.Context, op UpdateOperation) error {
	if op.ID == "" {
		op.ID = uuid.NewString()
	}
	if op.StartedAt.IsZero() {
		op.StartedAt = time.Now().UTC()
	}
	_, err := p.DB.ExecContext(ctx, `
INSERT INTO update_operations (id, cluster_id, action, status, dry_run, error, version, packages, started_at, finished_at)
VALUES ($1,$2,$3,$4,$5,$6,$7,COALESCE(string_to_array(NULLIF($8, ''), ','), '{}'),$9,$10)`,
		op.ID, op.ClusterID, op.Action, op.Status, op.DryRun, op.Error, op.Version, strings.Join(op.Packages, ","), op.StartedAt, nullTime(op.FinishedAt))
	return err
}

func (p *Postgres) ListUpdateOperations(ctx context.Context, clusterID string, limit int) ([]UpdateOperation, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := p.DB.QueryContext(ctx, `
SELECT id::text, cluster_id::text, action, status, dry_run, error, version, COALESCE(array_to_string(packages, ','), ''), started_at, finished_at
FROM update_operations WHERE cluster_id=$1 ORDER BY started_at DESC LIMIT $2`, clusterID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []UpdateOperation
	for rows.Next() {
		op, err := scanUpdateOperation(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, op)
	}
	return out, rows.Err()
}

func (p *Postgres) GetLatestUpdateOperation(ctx context.Context, clusterID string) (*UpdateOperation, error) {
	row := p.DB.QueryRowContext(ctx, `
SELECT id::text, cluster_id::text, action, status, dry_run, error, version, COALESCE(array_to_string(packages, ','), ''), started_at, finished_at
FROM update_operations WHERE cluster_id=$1 ORDER BY started_at DESC LIMIT 1`, clusterID)
	op, err := scanUpdateOperation(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &op, nil
}

func (p *Postgres) GetLatestCheckUpdateOperation(ctx context.Context, clusterID string) (*UpdateOperation, error) {
	row := p.DB.QueryRowContext(ctx, `
SELECT id::text, cluster_id::text, action, status, dry_run, error, version, COALESCE(array_to_string(packages, ','), ''), started_at, finished_at
FROM update_operations WHERE cluster_id=$1 AND action='check' AND version <> '' ORDER BY started_at DESC, id ASC LIMIT 1`, clusterID)
	op, err := scanUpdateOperation(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &op, nil
}

func (p *Postgres) UpdateUpdateOperation(ctx context.Context, op UpdateOperation) error {
	_, err := p.DB.ExecContext(ctx, `
UPDATE update_operations
SET action=$3, status=$4, dry_run=$5, error=$6, version=$7,
    packages=COALESCE(string_to_array(NULLIF($8, ''), ','), '{}'), finished_at=$9
WHERE cluster_id=$1 AND id=$2`,
		op.ClusterID, op.ID, op.Action, op.Status, op.DryRun, op.Error, op.Version, strings.Join(op.Packages, ","), nullTime(op.FinishedAt))
	return err
}

func scanUpdateOperation(s rowScanner) (UpdateOperation, error) {
	var op UpdateOperation
	var finished sql.NullTime
	var pkgCSV string
	if err := s.Scan(&op.ID, &op.ClusterID, &op.Action, &op.Status, &op.DryRun, &op.Error, &op.Version, &pkgCSV, &op.StartedAt, &finished); err != nil {
		return UpdateOperation{}, err
	}
	if pkgCSV != "" {
		op.Packages = strings.Split(pkgCSV, ",")
	}
	if finished.Valid {
		t := finished.Time.UTC()
		op.FinishedAt = &t
	}
	return op, nil
}

func nullTime(t *time.Time) any {
	if t == nil {
		return nil
	}
	return *t
}
