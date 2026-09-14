package appdb

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/google/uuid"
)

func (p *Postgres) CreateSnapshot(ctx context.Context, s Snapshot) error {
	if s.ID == "" {
		s.ID = uuid.NewString()
	}
	if s.CreatedAt.IsZero() {
		s.CreatedAt = time.Now().UTC()
	}
	var parent any
	if s.ParentID != "" {
		parent = s.ParentID
	}
	_, err := p.DB.ExecContext(ctx, `
INSERT INTO snapshots (id, cluster_id, workload_id, volume_id, name, purpose_tag, mechanism, backend_ref, parent_id, chain_depth, status, created_at)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)`,
		s.ID, s.ClusterID, s.WorkloadID, s.VolumeID, s.Name, s.PurposeTag, s.Mechanism, s.BackendRef, parent, s.ChainDepth, s.Status, s.CreatedAt)
	return err
}

func (p *Postgres) ListSnapshots(ctx context.Context, clusterID, workloadID string) ([]Snapshot, error) {
	rows, err := p.DB.QueryContext(ctx, `
SELECT id::text, cluster_id::text, workload_id::text, volume_id::text, name, purpose_tag, mechanism, backend_ref,
       COALESCE(parent_id::text, ''), chain_depth, status, created_at
FROM snapshots WHERE cluster_id=$1 AND workload_id=$2 ORDER BY created_at ASC`, clusterID, workloadID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Snapshot
	for rows.Next() {
		s, err := scanSnapshot(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func (p *Postgres) GetSnapshot(ctx context.Context, clusterID, id string) (*Snapshot, error) {
	row := p.DB.QueryRowContext(ctx, `
SELECT id::text, cluster_id::text, workload_id::text, volume_id::text, name, purpose_tag, mechanism, backend_ref,
       COALESCE(parent_id::text, ''), chain_depth, status, created_at
FROM snapshots WHERE cluster_id=$1 AND id=$2`, clusterID, id)
	s, err := scanSnapshot(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &s, nil
}

func (p *Postgres) UpdateVolumeLocator(ctx context.Context, clusterID, id, backendRef string) error {
	res, err := p.DB.ExecContext(ctx, `UPDATE volumes SET backend_ref=$3, updated_at=now() WHERE cluster_id=$1 AND id=$2`, clusterID, id, backendRef)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func scanSnapshot(row rowScanner) (Snapshot, error) {
	var s Snapshot
	err := row.Scan(&s.ID, &s.ClusterID, &s.WorkloadID, &s.VolumeID, &s.Name, &s.PurposeTag, &s.Mechanism, &s.BackendRef,
		&s.ParentID, &s.ChainDepth, &s.Status, &s.CreatedAt)
	return s, err
}
