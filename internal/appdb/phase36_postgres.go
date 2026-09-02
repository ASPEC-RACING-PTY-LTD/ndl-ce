package appdb

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

func (p *Postgres) UpsertStorePackage(ctx context.Context, pkg StorePackage) error {
	if pkg.CreatedAt.IsZero() {
		pkg.CreatedAt = time.Now().UTC()
	}
	_, err := p.DB.ExecContext(ctx, `
INSERT INTO store_packages (id, cluster_id, name, version, class, title, summary, manifest_yaml, unsigned_warning, created_at)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
ON CONFLICT (id) DO UPDATE SET
  title=EXCLUDED.title, summary=EXCLUDED.summary, manifest_yaml=EXCLUDED.manifest_yaml,
  unsigned_warning=EXCLUDED.unsigned_warning`,
		pkg.ID, pkg.ClusterID, pkg.Name, pkg.Version, pkg.Class, pkg.Title, pkg.Summary, pkg.ManifestYAML, pkg.UnsignedWarning, pkg.CreatedAt)
	return err
}

func (p *Postgres) ListStorePackages(ctx context.Context, clusterID string) ([]StorePackage, error) {
	rows, err := p.DB.QueryContext(ctx, `
SELECT id::text, cluster_id::text, name, version, class, title, summary, manifest_yaml, unsigned_warning, created_at
FROM store_packages WHERE cluster_id=$1 ORDER BY name, version`, clusterID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []StorePackage
	for rows.Next() {
		var pkg StorePackage
		if err := rows.Scan(&pkg.ID, &pkg.ClusterID, &pkg.Name, &pkg.Version, &pkg.Class, &pkg.Title, &pkg.Summary, &pkg.ManifestYAML, &pkg.UnsignedWarning, &pkg.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, pkg)
	}
	return out, rows.Err()
}

func (p *Postgres) GetStorePackage(ctx context.Context, clusterID, id string) (*StorePackage, error) {
	row := p.DB.QueryRowContext(ctx, `
SELECT id::text, cluster_id::text, name, version, class, title, summary, manifest_yaml, unsigned_warning, created_at
FROM store_packages WHERE cluster_id=$1 AND id=$2`, clusterID, id)
	var pkg StorePackage
	if err := row.Scan(&pkg.ID, &pkg.ClusterID, &pkg.Name, &pkg.Version, &pkg.Class, &pkg.Title, &pkg.Summary, &pkg.ManifestYAML, &pkg.UnsignedWarning, &pkg.CreatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &pkg, nil
}

func (p *Postgres) GetStorePackageByName(ctx context.Context, clusterID, name, version string) (*StorePackage, error) {
	q := `
SELECT id::text, cluster_id::text, name, version, class, title, summary, manifest_yaml, unsigned_warning, created_at
FROM store_packages WHERE cluster_id=$1 AND name=$2`
	args := []any{clusterID, name}
	if version != "" {
		q += ` AND version=$3`
		args = append(args, version)
	}
	q += ` ORDER BY created_at DESC LIMIT 1`
	row := p.DB.QueryRowContext(ctx, q, args...)
	var pkg StorePackage
	if err := row.Scan(&pkg.ID, &pkg.ClusterID, &pkg.Name, &pkg.Version, &pkg.Class, &pkg.Title, &pkg.Summary, &pkg.ManifestYAML, &pkg.UnsignedWarning, &pkg.CreatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &pkg, nil
}

func (p *Postgres) CreateStoreInstallation(ctx context.Context, in StoreInstallation) error {
	if in.CreatedAt.IsZero() {
		in.CreatedAt = time.Now().UTC()
	}
	_, err := p.DB.ExecContext(ctx, `
INSERT INTO store_installations (id, cluster_id, package_id, status, reason, stack_id, workload_id, node_id, warning, created_at, finished_at)
VALUES ($1,$2,$3,$4,$5,NULLIF($6,'')::uuid,NULLIF($7,'')::uuid,NULLIF($8,'')::uuid,$9,$10,$11)`,
		in.ID, in.ClusterID, in.PackageID, in.Status, in.Reason, in.StackID, in.WorkloadID, in.NodeID, in.Warning, in.CreatedAt, in.FinishedAt)
	return err
}

func (p *Postgres) GetStoreInstallation(ctx context.Context, clusterID, id string) (*StoreInstallation, error) {
	row := p.DB.QueryRowContext(ctx, `
SELECT id::text, cluster_id::text, package_id::text, status, reason, COALESCE(stack_id::text,''), COALESCE(workload_id::text,''),
       COALESCE(node_id::text,''), warning, created_at, finished_at
FROM store_installations WHERE cluster_id=$1 AND id=$2`, clusterID, id)
	var in StoreInstallation
	var finished sql.NullTime
	if err := row.Scan(&in.ID, &in.ClusterID, &in.PackageID, &in.Status, &in.Reason, &in.StackID, &in.WorkloadID, &in.NodeID, &in.Warning, &in.CreatedAt, &finished); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	if finished.Valid {
		t := finished.Time.UTC()
		in.FinishedAt = &t
	}
	return &in, nil
}

func (p *Postgres) ListStoreInstallations(ctx context.Context, clusterID string) ([]StoreInstallation, error) {
	rows, err := p.DB.QueryContext(ctx, `
SELECT id::text, cluster_id::text, package_id::text, status, reason, COALESCE(stack_id::text,''), COALESCE(workload_id::text,''),
       COALESCE(node_id::text,''), warning, created_at, finished_at
FROM store_installations WHERE cluster_id=$1 ORDER BY created_at DESC`, clusterID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []StoreInstallation
	for rows.Next() {
		var in StoreInstallation
		var finished sql.NullTime
		if err := rows.Scan(&in.ID, &in.ClusterID, &in.PackageID, &in.Status, &in.Reason, &in.StackID, &in.WorkloadID, &in.NodeID, &in.Warning, &in.CreatedAt, &finished); err != nil {
			return nil, err
		}
		if finished.Valid {
			t := finished.Time.UTC()
			in.FinishedAt = &t
		}
		out = append(out, in)
	}
	return out, rows.Err()
}

func (p *Postgres) UpdateStoreInstallation(ctx context.Context, in StoreInstallation) error {
	res, err := p.DB.ExecContext(ctx, `
UPDATE store_installations SET status=$3, reason=$4, stack_id=NULLIF($5,'')::uuid, workload_id=NULLIF($6,'')::uuid,
  node_id=NULLIF($7,'')::uuid, warning=$8, finished_at=$9
WHERE cluster_id=$1 AND id=$2`,
		in.ClusterID, in.ID, in.Status, in.Reason, in.StackID, in.WorkloadID, in.NodeID, in.Warning, in.FinishedAt)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("installation not found")
	}
	return nil
}
