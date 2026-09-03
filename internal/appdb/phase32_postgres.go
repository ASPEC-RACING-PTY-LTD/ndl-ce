package appdb

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

func (p *Postgres) SetWorkloadDesiredNode(ctx context.Context, clusterID, workloadID, destNodeID string) error {
	_, err := p.DB.ExecContext(ctx, `UPDATE workloads SET desired_node_id=$3, updated_at=now() WHERE cluster_id=$1 AND id=$2`,
		clusterID, workloadID, destNodeID)
	return err
}

func (p *Postgres) TransferWorkloadOwnership(ctx context.Context, clusterID, workloadID, destNodeID string, expectedEpoch int) (int, error) {
	row := p.DB.QueryRowContext(ctx, `
UPDATE workloads
SET node_id=$3, owner_node_id=$3, desired_node_id=$3, ownership_epoch=ownership_epoch+1, updated_at=now()
WHERE cluster_id=$1 AND id=$2 AND ownership_epoch=$4
RETURNING ownership_epoch`, clusterID, workloadID, destNodeID, expectedEpoch)
	var epoch int
	if err := row.Scan(&epoch); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, errors.New("ownership epoch mismatch")
		}
		return 0, err
	}
	return epoch, nil
}

func (p *Postgres) CreateMigrateJob(ctx context.Context, j MigrateJob) error {
	if j.CreatedAt.IsZero() {
		j.CreatedAt = time.Now().UTC()
	}
	j.UpdatedAt = j.CreatedAt
	_, err := p.DB.ExecContext(ctx, `
INSERT INTO migrate_jobs (
  id, cluster_id, workload_id, operation_id, source_node_id, dest_node_id, mode, state,
  epoch_at_start, source_running, dest_running, reason, created_at, updated_at
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)`,
		j.ID, j.ClusterID, j.WorkloadID, nullIfEmpty(j.OperationID), j.SourceNodeID, j.DestNodeID, j.Mode, j.State,
		j.EpochAtStart, j.SourceRunning, j.DestRunning, j.Reason, j.CreatedAt, j.UpdatedAt)
	return err
}

func (p *Postgres) GetMigrateJob(ctx context.Context, clusterID, id string) (*MigrateJob, error) {
	row := p.DB.QueryRowContext(ctx, migrateJobSelect+` WHERE cluster_id=$1 AND id=$2`, clusterID, id)
	j, err := scanMigrateJob(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &j, nil
}

func (p *Postgres) UpdateMigrateJob(ctx context.Context, j MigrateJob) error {
	res, err := p.DB.ExecContext(ctx, `
UPDATE migrate_jobs SET state=$2, source_running=$3, dest_running=$4, reason=$5, updated_at=now()
WHERE id=$1`, j.ID, j.State, j.SourceRunning, j.DestRunning, j.Reason)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return errors.New("migrate job not found")
	}
	return nil
}

func (p *Postgres) ListMigrateJobs(ctx context.Context, clusterID string, limit int) ([]MigrateJob, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := p.DB.QueryContext(ctx, migrateJobSelect+` WHERE cluster_id=$1 ORDER BY created_at DESC LIMIT $2`, clusterID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []MigrateJob
	for rows.Next() {
		j, err := scanMigrateJob(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, j)
	}
	return out, rows.Err()
}

const migrateJobSelect = `
SELECT id::text, cluster_id::text, workload_id::text, COALESCE(operation_id::text, ''),
       source_node_id::text, dest_node_id::text, mode, state, epoch_at_start,
       source_running, dest_running, reason, created_at, updated_at
FROM migrate_jobs`

func scanMigrateJob(row rowScanner) (MigrateJob, error) {
	var j MigrateJob
	err := row.Scan(&j.ID, &j.ClusterID, &j.WorkloadID, &j.OperationID, &j.SourceNodeID, &j.DestNodeID,
		&j.Mode, &j.State, &j.EpochAtStart, &j.SourceRunning, &j.DestRunning, &j.Reason, &j.CreatedAt, &j.UpdatedAt)
	return j, err
}
