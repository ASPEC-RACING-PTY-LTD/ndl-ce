package appdb

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

func (p *Postgres) ListFeatures(ctx context.Context, clusterID string) ([]Feature, error) {
	rows, err := p.DB.QueryContext(ctx, `
SELECT cluster_id::text, id, enabled, package_status, runtime_status, reason, updated_at
FROM features WHERE cluster_id=$1 ORDER BY id`, clusterID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Feature
	for rows.Next() {
		var f Feature
		if err := rows.Scan(&f.ClusterID, &f.ID, &f.Enabled, &f.PackageStatus, &f.RuntimeStatus, &f.Reason, &f.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

func (p *Postgres) GetFeature(ctx context.Context, clusterID, id string) (*Feature, error) {
	row := p.DB.QueryRowContext(ctx, `
SELECT cluster_id::text, id, enabled, package_status, runtime_status, reason, updated_at
FROM features WHERE cluster_id=$1 AND id=$2`, clusterID, id)
	var f Feature
	if err := row.Scan(&f.ClusterID, &f.ID, &f.Enabled, &f.PackageStatus, &f.RuntimeStatus, &f.Reason, &f.UpdatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &f, nil
}

func (p *Postgres) UpsertFeature(ctx context.Context, f Feature) error {
	if f.ClusterID == "" || f.ID == "" {
		return fmt.Errorf("feature identity is required")
	}
	if f.PackageStatus == "" {
		f.PackageStatus = FeatureNotConfigured
	}
	if f.RuntimeStatus == "" {
		f.RuntimeStatus = FeatureNotStarted
	}
	if f.UpdatedAt.IsZero() {
		f.UpdatedAt = time.Now().UTC()
	}
	_, err := p.DB.ExecContext(ctx, `
INSERT INTO features (cluster_id, id, enabled, package_status, runtime_status, reason, updated_at)
VALUES ($1,$2,$3,$4,$5,$6,$7)
ON CONFLICT (cluster_id, id) DO UPDATE SET
  enabled=EXCLUDED.enabled, package_status=EXCLUDED.package_status, runtime_status=EXCLUDED.runtime_status,
  reason=EXCLUDED.reason, updated_at=EXCLUDED.updated_at`,
		f.ClusterID, f.ID, f.Enabled, f.PackageStatus, f.RuntimeStatus, f.Reason, f.UpdatedAt)
	return err
}
