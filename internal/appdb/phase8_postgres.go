package appdb

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

func (p *Postgres) DeleteWorkload(ctx context.Context, clusterID, id string) error {
	tx, err := p.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `DELETE FROM vm_cidata WHERE workload_id=$1`, id); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM vm_firmware WHERE workload_id=$1`, id); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM workload_disks WHERE workload_id=$1`, id); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM workload_nics WHERE workload_id=$1`, id); err != nil {
		return err
	}
	res, err := tx.ExecContext(ctx, `DELETE FROM workloads WHERE cluster_id=$1 AND id=$2`, clusterID, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return errors.New("workload not found")
	}
	return tx.Commit()
}

func (p *Postgres) UpsertVMCidata(ctx context.Context, row VMCidata) error {
	if row.UpdatedAt.IsZero() {
		row.UpdatedAt = time.Now().UTC()
	}
	_, err := p.DB.ExecContext(ctx, `
INSERT INTO vm_cidata (workload_id, cluster_id, user_data_sha, has_password, updated_at)
VALUES ($1,$2,$3,$4,$5)
ON CONFLICT (workload_id) DO UPDATE SET user_data_sha=EXCLUDED.user_data_sha, has_password=EXCLUDED.has_password, updated_at=EXCLUDED.updated_at`,
		row.WorkloadID, row.ClusterID, row.UserDataSHA, row.HasPassword, row.UpdatedAt)
	return err
}

func (p *Postgres) GetVMCidata(ctx context.Context, clusterID, workloadID string) (*VMCidata, error) {
	row := p.DB.QueryRowContext(ctx, `
SELECT workload_id::text, cluster_id::text, user_data_sha, has_password, updated_at
FROM vm_cidata WHERE cluster_id=$1 AND workload_id=$2`, clusterID, workloadID)
	var out VMCidata
	if err := row.Scan(&out.WorkloadID, &out.ClusterID, &out.UserDataSHA, &out.HasPassword, &out.UpdatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &out, nil
}

func (p *Postgres) UpsertVMFirmware(ctx context.Context, row VMFirmware) error {
	if row.UpdatedAt.IsZero() {
		row.UpdatedAt = time.Now().UTC()
	}
	_, err := p.DB.ExecContext(ctx, `
INSERT INTO vm_firmware (workload_id, cluster_id, mode, vars_ref, updated_at)
VALUES ($1,$2,$3,$4,$5)
ON CONFLICT (workload_id) DO UPDATE SET mode=EXCLUDED.mode, vars_ref=EXCLUDED.vars_ref, updated_at=EXCLUDED.updated_at`,
		row.WorkloadID, row.ClusterID, row.Mode, row.VarsRef, row.UpdatedAt)
	return err
}

func (p *Postgres) GetVMFirmware(ctx context.Context, clusterID, workloadID string) (*VMFirmware, error) {
	row := p.DB.QueryRowContext(ctx, `
SELECT workload_id::text, cluster_id::text, mode, vars_ref, updated_at
FROM vm_firmware WHERE cluster_id=$1 AND workload_id=$2`, clusterID, workloadID)
	var out VMFirmware
	if err := row.Scan(&out.WorkloadID, &out.ClusterID, &out.Mode, &out.VarsRef, &out.UpdatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &out, nil
}
