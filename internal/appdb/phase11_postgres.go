package appdb

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/google/uuid"
)

func (p *Postgres) CreateBackupTarget(ctx context.Context, t BackupTarget, password string) error {
	if t.ID == "" {
		t.ID = uuid.NewString()
	}
	if t.CreatedAt.IsZero() {
		t.CreatedAt = time.Now().UTC()
	}
	tx, err := p.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	_, err = tx.ExecContext(ctx, `
INSERT INTO backup_targets (id, cluster_id, name, kind, locator, status, username, created_at, updated_at)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$8)`,
		t.ID, t.ClusterID, t.Name, t.Kind, t.Locator, t.Status, t.Username, t.CreatedAt)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `
INSERT INTO secrets.backup_credentials (target_id, cluster_id, password, updated_at)
VALUES ($1,$2,$3,$4)`, t.ID, t.ClusterID, password, t.CreatedAt)
	if err != nil {
		return err
	}
	return tx.Commit()
}

func (p *Postgres) ListBackupTargets(ctx context.Context, clusterID string) ([]BackupTarget, error) {
	rows, err := p.DB.QueryContext(ctx, `
SELECT id::text, cluster_id::text, name, kind, locator, status, username, created_at, updated_at
FROM backup_targets WHERE cluster_id=$1 ORDER BY created_at ASC`, clusterID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []BackupTarget
	for rows.Next() {
		t, err := scanBackupTarget(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (p *Postgres) GetBackupTarget(ctx context.Context, clusterID, id string) (*BackupTarget, error) {
	row := p.DB.QueryRowContext(ctx, `
SELECT id::text, cluster_id::text, name, kind, locator, status, username, created_at, updated_at
FROM backup_targets WHERE cluster_id=$1 AND id=$2`, clusterID, id)
	t, err := scanBackupTarget(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &t, nil
}

func (p *Postgres) UpdateBackupTargetStatus(ctx context.Context, clusterID, id, status string) error {
	res, err := p.DB.ExecContext(ctx, `UPDATE backup_targets SET status=$3, updated_at=now() WHERE cluster_id=$1 AND id=$2`, clusterID, id, status)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (p *Postgres) CreateBackupPolicy(ctx context.Context, pol BackupPolicy) error {
	if pol.ID == "" {
		pol.ID = uuid.NewString()
	}
	if pol.CreatedAt.IsZero() {
		pol.CreatedAt = time.Now().UTC()
	}
	_, err := p.DB.ExecContext(ctx, `
INSERT INTO backup_policies (id, cluster_id, name, workload_id, target_id, schedule, keep_daily, keep_weekly, keep_monthly, last_run_at, created_at)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`,
		pol.ID, pol.ClusterID, pol.Name, pol.WorkloadID, pol.TargetID, pol.Schedule, pol.KeepDaily, pol.KeepWeekly, pol.KeepMonthly, pol.LastRunAt, pol.CreatedAt)
	return err
}

func (p *Postgres) ListBackupPolicies(ctx context.Context, clusterID string) ([]BackupPolicy, error) {
	rows, err := p.DB.QueryContext(ctx, `
SELECT id::text, cluster_id::text, name, workload_id::text, target_id::text, schedule, keep_daily, keep_weekly, keep_monthly, last_run_at, created_at
FROM backup_policies WHERE cluster_id=$1 ORDER BY created_at ASC`, clusterID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []BackupPolicy
	for rows.Next() {
		pol, err := scanBackupPolicy(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, pol)
	}
	return out, rows.Err()
}

func (p *Postgres) GetBackupPolicy(ctx context.Context, clusterID, id string) (*BackupPolicy, error) {
	row := p.DB.QueryRowContext(ctx, `
SELECT id::text, cluster_id::text, name, workload_id::text, target_id::text, schedule, keep_daily, keep_weekly, keep_monthly, last_run_at, created_at
FROM backup_policies WHERE cluster_id=$1 AND id=$2`, clusterID, id)
	pol, err := scanBackupPolicy(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &pol, nil
}

func (p *Postgres) UpdateBackupPolicyLastRun(ctx context.Context, clusterID, id string, at time.Time) error {
	res, err := p.DB.ExecContext(ctx, `UPDATE backup_policies SET last_run_at=$3 WHERE cluster_id=$1 AND id=$2`, clusterID, id, at)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (p *Postgres) CreateBackupRun(ctx context.Context, r BackupRun) error {
	if r.ID == "" {
		r.ID = uuid.NewString()
	}
	if r.StartedAt.IsZero() {
		r.StartedAt = time.Now().UTC()
	}
	var policy any
	if r.PolicyID != "" {
		policy = r.PolicyID
	}
	var snap any
	if r.SnapshotID != "" {
		snap = r.SnapshotID
	}
	_, err := p.DB.ExecContext(ctx, `
INSERT INTO backup_runs (id, cluster_id, policy_id, target_id, workload_id, snapshot_id, status, error, restored_workload_id, started_at, finished_at)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`,
		r.ID, r.ClusterID, policy, r.TargetID, r.WorkloadID, snap, r.Status, r.Error, r.RestoredWorkloadID, r.StartedAt, r.FinishedAt)
	return err
}

func (p *Postgres) ListBackupRuns(ctx context.Context, clusterID string) ([]BackupRun, error) {
	rows, err := p.DB.QueryContext(ctx, `
SELECT id::text, cluster_id::text, COALESCE(policy_id::text, ''), target_id::text, workload_id::text,
       COALESCE(snapshot_id::text, ''), status, error, restored_workload_id, started_at, finished_at
FROM backup_runs WHERE cluster_id=$1 ORDER BY started_at DESC`, clusterID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []BackupRun
	for rows.Next() {
		r, err := scanBackupRun(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (p *Postgres) GetBackupRun(ctx context.Context, clusterID, id string) (*BackupRun, error) {
	row := p.DB.QueryRowContext(ctx, `
SELECT id::text, cluster_id::text, COALESCE(policy_id::text, ''), target_id::text, workload_id::text,
       COALESCE(snapshot_id::text, ''), status, error, restored_workload_id, started_at, finished_at
FROM backup_runs WHERE cluster_id=$1 AND id=$2`, clusterID, id)
	r, err := scanBackupRun(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &r, nil
}

func (p *Postgres) UpdateBackupRun(ctx context.Context, r BackupRun) error {
	var policy any
	if r.PolicyID != "" {
		policy = r.PolicyID
	}
	var snap any
	if r.SnapshotID != "" {
		snap = r.SnapshotID
	}
	res, err := p.DB.ExecContext(ctx, `
UPDATE backup_runs SET policy_id=$3, snapshot_id=$4, status=$5, error=$6, restored_workload_id=$7, finished_at=$8
WHERE cluster_id=$1 AND id=$2`,
		r.ClusterID, r.ID, policy, snap, r.Status, r.Error, r.RestoredWorkloadID, r.FinishedAt)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (p *Postgres) CreateBackupArtifact(ctx context.Context, a BackupArtifact) error {
	if a.ID == "" {
		a.ID = uuid.NewString()
	}
	if a.CreatedAt.IsZero() {
		a.CreatedAt = time.Now().UTC()
	}
	_, err := p.DB.ExecContext(ctx, `
INSERT INTO backup_artifacts (id, cluster_id, run_id, workload_id, checksum_sha256, size_bytes, locator, format, created_at)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`,
		a.ID, a.ClusterID, a.RunID, a.WorkloadID, a.ChecksumSHA256, a.SizeBytes, a.Locator, a.Format, a.CreatedAt)
	return err
}

func (p *Postgres) ListBackupArtifacts(ctx context.Context, clusterID string) ([]BackupArtifact, error) {
	rows, err := p.DB.QueryContext(ctx, `
SELECT id::text, cluster_id::text, run_id::text, workload_id::text, checksum_sha256, size_bytes, locator, format, created_at
FROM backup_artifacts WHERE cluster_id=$1 ORDER BY created_at DESC`, clusterID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []BackupArtifact
	for rows.Next() {
		a, err := scanBackupArtifact(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (p *Postgres) ListBackupArtifactsForWorkload(ctx context.Context, clusterID, workloadID, targetID string) ([]BackupArtifact, error) {
	q := `
SELECT a.id::text, a.cluster_id::text, a.run_id::text, a.workload_id::text, a.checksum_sha256, a.size_bytes, a.locator, a.format, a.created_at
FROM backup_artifacts a
JOIN backup_runs r ON r.id = a.run_id
WHERE a.cluster_id=$1 AND a.workload_id=$2`
	args := []any{clusterID, workloadID}
	if targetID != "" {
		q += ` AND r.target_id=$3`
		args = append(args, targetID)
	}
	q += ` ORDER BY a.created_at DESC`
	rows, err := p.DB.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []BackupArtifact
	for rows.Next() {
		a, err := scanBackupArtifact(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (p *Postgres) GetBackupArtifact(ctx context.Context, clusterID, id string) (*BackupArtifact, error) {
	row := p.DB.QueryRowContext(ctx, `
SELECT id::text, cluster_id::text, run_id::text, workload_id::text, checksum_sha256, size_bytes, locator, format, created_at
FROM backup_artifacts WHERE cluster_id=$1 AND id=$2`, clusterID, id)
	a, err := scanBackupArtifact(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &a, nil
}

func (p *Postgres) DeleteBackupArtifact(ctx context.Context, clusterID, id string) error {
	res, err := p.DB.ExecContext(ctx, `DELETE FROM backup_artifacts WHERE cluster_id=$1 AND id=$2`, clusterID, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func scanBackupTarget(row rowScanner) (BackupTarget, error) {
	var t BackupTarget
	err := row.Scan(&t.ID, &t.ClusterID, &t.Name, &t.Kind, &t.Locator, &t.Status, &t.Username, &t.CreatedAt, &t.UpdatedAt)
	return t, err
}

func scanBackupPolicy(row rowScanner) (BackupPolicy, error) {
	var pol BackupPolicy
	var last sql.NullTime
	err := row.Scan(&pol.ID, &pol.ClusterID, &pol.Name, &pol.WorkloadID, &pol.TargetID, &pol.Schedule, &pol.KeepDaily, &pol.KeepWeekly, &pol.KeepMonthly, &last, &pol.CreatedAt)
	if last.Valid {
		t := last.Time
		pol.LastRunAt = &t
	}
	return pol, err
}

func scanBackupRun(row rowScanner) (BackupRun, error) {
	var r BackupRun
	var finished sql.NullTime
	err := row.Scan(&r.ID, &r.ClusterID, &r.PolicyID, &r.TargetID, &r.WorkloadID, &r.SnapshotID, &r.Status, &r.Error, &r.RestoredWorkloadID, &r.StartedAt, &finished)
	if finished.Valid {
		t := finished.Time
		r.FinishedAt = &t
	}
	return r, err
}

func scanBackupArtifact(row rowScanner) (BackupArtifact, error) {
	var a BackupArtifact
	err := row.Scan(&a.ID, &a.ClusterID, &a.RunID, &a.WorkloadID, &a.ChecksumSHA256, &a.SizeBytes, &a.Locator, &a.Format, &a.CreatedAt)
	return a, err
}
