package appdb

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/google/uuid"
)

func (p *Postgres) UpdateBackupArtifact(ctx context.Context, a BackupArtifact) error {
	status := a.VerifyStatus
	if status == "" {
		status = BackupUnverified
	}
	res, err := p.DB.ExecContext(ctx, `
UPDATE backup_artifacts SET
  verify_status=$3, verify_error=$4, last_tested_at=$5, throwaway_workload_id=$6,
  local_complete=$7, remote_state=$8, logical_bytes=$9, physical_new_data=$10,
  chunks_new=$11, chunks_reused=$12, capture_duration_ns=$13, upload_duration_ns=$14,
  consistency=$15, capture_mode=$16, blueprint_json=$17, stats_json=$18,
  engine_version=$19, backup_id=$20, namespace=$21, transferred_bytes=$22
WHERE cluster_id=$1 AND id=$2`,
		a.ClusterID, a.ID, status, a.VerifyError, a.LastTestedAt, nullIfEmpty(a.ThrowawayWorkloadID),
		a.LocalComplete, a.RemoteState, a.LogicalBytes, a.PhysicalNewData,
		a.ChunksNew, a.ChunksReused, a.CaptureDurationNS, a.UploadDurationNS,
		a.Consistency, a.CaptureMode, a.BlueprintJSON, a.StatsJSON,
		a.EngineVersion, a.BackupID, a.Namespace, a.TransferredBytes)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return errors.New("backup artifact not found")
	}
	return nil
}

func (p *Postgres) GetBackupWorkspaceSettings(ctx context.Context, clusterID string) (*BackupWorkspaceSettings, error) {
	row := p.DB.QueryRowContext(ctx, `
SELECT cluster_id::text, max_local_bytes, min_host_free_bytes, capture_concurrency, upload_workers, bandwidth_limit_bps, COALESCE(cache_retention_hours, 0), updated_at
FROM backup_workspace_settings WHERE cluster_id=$1`, clusterID)
	var s BackupWorkspaceSettings
	err := row.Scan(&s.ClusterID, &s.MaxLocalBytes, &s.MinHostFreeBytes, &s.CaptureConcurrency, &s.UploadWorkers, &s.BandwidthLimitBPS, &s.CacheRetentionHours, &s.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return DefaultBackupWorkspaceSettings(clusterID), nil
	}
	if err != nil {
		return nil, err
	}
	return &s, nil
}

func (p *Postgres) UpsertBackupWorkspaceSettings(ctx context.Context, s BackupWorkspaceSettings) error {
	if s.UpdatedAt.IsZero() {
		s.UpdatedAt = time.Now().UTC()
	}
	_, err := p.DB.ExecContext(ctx, `
INSERT INTO backup_workspace_settings (cluster_id, max_local_bytes, min_host_free_bytes, capture_concurrency, upload_workers, bandwidth_limit_bps, cache_retention_hours, updated_at)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
ON CONFLICT (cluster_id) DO UPDATE SET
  max_local_bytes=EXCLUDED.max_local_bytes,
  min_host_free_bytes=EXCLUDED.min_host_free_bytes,
  capture_concurrency=EXCLUDED.capture_concurrency,
  upload_workers=EXCLUDED.upload_workers,
  bandwidth_limit_bps=EXCLUDED.bandwidth_limit_bps,
  cache_retention_hours=EXCLUDED.cache_retention_hours,
  updated_at=EXCLUDED.updated_at`,
		s.ClusterID, s.MaxLocalBytes, s.MinHostFreeBytes, s.CaptureConcurrency, s.UploadWorkers, s.BandwidthLimitBPS, s.CacheRetentionHours, s.UpdatedAt)
	return err
}

func (p *Postgres) UpsertBackupRepository(ctx context.Context, r BackupRepository) error {
	if r.ID == "" {
		r.ID = uuid.NewString()
	}
	if r.UpdatedAt.IsZero() {
		r.UpdatedAt = time.Now().UTC()
	}
	_, err := p.DB.ExecContext(ctx, `
INSERT INTO backup_repositories (id, cluster_id, root_path, size_bytes, status, updated_at)
VALUES ($1,$2,$3,$4,$5,$6)
ON CONFLICT (cluster_id) DO UPDATE SET
  root_path=EXCLUDED.root_path, size_bytes=EXCLUDED.size_bytes, status=EXCLUDED.status, updated_at=EXCLUDED.updated_at`,
		r.ID, r.ClusterID, r.RootPath, r.SizeBytes, r.Status, r.UpdatedAt)
	return err
}

func (p *Postgres) GetBackupRepository(ctx context.Context, clusterID string) (*BackupRepository, error) {
	row := p.DB.QueryRowContext(ctx, `
SELECT id::text, cluster_id::text, root_path, size_bytes, status, updated_at
FROM backup_repositories WHERE cluster_id=$1`, clusterID)
	var r BackupRepository
	err := row.Scan(&r.ID, &r.ClusterID, &r.RootPath, &r.SizeBytes, &r.Status, &r.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &r, nil
}

func (p *Postgres) UpsertBackupRestorePoint(ctx context.Context, pt BackupRestorePoint) error {
	if pt.ID == "" {
		pt.ID = uuid.NewString()
	}
	if pt.CreatedAt.IsZero() {
		pt.CreatedAt = time.Now().UTC()
	}
	_, err := p.DB.ExecContext(ctx, `
INSERT INTO backup_restore_points (id, cluster_id, artifact_id, run_id, workload_id, backup_id, namespace, capture_mode, local_complete, remote_state, logical_bytes, physical_new_data, created_at)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)
ON CONFLICT (id) DO UPDATE SET
  local_complete=EXCLUDED.local_complete, remote_state=EXCLUDED.remote_state,
  logical_bytes=EXCLUDED.logical_bytes, physical_new_data=EXCLUDED.physical_new_data`,
		pt.ID, pt.ClusterID, nullIfEmpty(pt.ArtifactID), nullIfEmpty(pt.RunID), pt.WorkloadID, pt.BackupID, pt.Namespace,
		pt.CaptureMode, pt.LocalComplete, pt.RemoteState, pt.LogicalBytes, pt.PhysicalNewData, pt.CreatedAt)
	return err
}

func (p *Postgres) ListBackupRestorePoints(ctx context.Context, clusterID string) ([]BackupRestorePoint, error) {
	rows, err := p.DB.QueryContext(ctx, `
SELECT id::text, cluster_id::text, COALESCE(artifact_id::text, ''), COALESCE(run_id::text, ''), workload_id::text,
       backup_id, namespace, capture_mode, local_complete, remote_state, logical_bytes, physical_new_data, created_at
FROM backup_restore_points WHERE cluster_id=$1 ORDER BY created_at DESC`, clusterID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []BackupRestorePoint
	for rows.Next() {
		var pt BackupRestorePoint
		if err := rows.Scan(&pt.ID, &pt.ClusterID, &pt.ArtifactID, &pt.RunID, &pt.WorkloadID, &pt.BackupID, &pt.Namespace, &pt.CaptureMode, &pt.LocalComplete, &pt.RemoteState, &pt.LogicalBytes, &pt.PhysicalNewData, &pt.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, pt)
	}
	return out, rows.Err()
}

func (p *Postgres) GetBackupRestorePoint(ctx context.Context, clusterID, id string) (*BackupRestorePoint, error) {
	row := p.DB.QueryRowContext(ctx, `
SELECT id::text, cluster_id::text, COALESCE(artifact_id::text, ''), COALESCE(run_id::text, ''), workload_id::text,
       backup_id, namespace, capture_mode, local_complete, remote_state, logical_bytes, physical_new_data, created_at
FROM backup_restore_points WHERE cluster_id=$1 AND id=$2`, clusterID, id)
	var pt BackupRestorePoint
	err := row.Scan(&pt.ID, &pt.ClusterID, &pt.ArtifactID, &pt.RunID, &pt.WorkloadID, &pt.BackupID, &pt.Namespace, &pt.CaptureMode, &pt.LocalComplete, &pt.RemoteState, &pt.LogicalBytes, &pt.PhysicalNewData, &pt.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &pt, nil
}

func (p *Postgres) ReplaceBackupUploadJobs(ctx context.Context, clusterID string, jobs []BackupUploadJob) error {
	tx, err := p.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `DELETE FROM backup_upload_jobs WHERE cluster_id=$1`, clusterID); err != nil {
		return err
	}
	for _, j := range jobs {
		if j.ID == "" {
			j.ID = uuid.NewString()
		}
		if j.UpdatedAt.IsZero() {
			j.UpdatedAt = time.Now().UTC()
		}
		if _, err := tx.ExecContext(ctx, `
INSERT INTO backup_upload_jobs (id, cluster_id, artifact_id, backup_id, pack_id, state, attempts, updated_at)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`,
			j.ID, clusterID, nullIfEmpty(j.ArtifactID), j.BackupID, j.PackID, j.State, j.Attempts, j.UpdatedAt); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (p *Postgres) ListBackupUploadJobs(ctx context.Context, clusterID string) ([]BackupUploadJob, error) {
	rows, err := p.DB.QueryContext(ctx, `
SELECT id::text, cluster_id::text, COALESCE(artifact_id::text, ''), backup_id, pack_id, state, attempts, updated_at
FROM backup_upload_jobs WHERE cluster_id=$1 ORDER BY updated_at DESC`, clusterID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []BackupUploadJob
	for rows.Next() {
		var j BackupUploadJob
		if err := rows.Scan(&j.ID, &j.ClusterID, &j.ArtifactID, &j.BackupID, &j.PackID, &j.State, &j.Attempts, &j.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, j)
	}
	return out, rows.Err()
}

// CompactBackupArtifactStats rewrites oversized stored statistics one row at a
// time so a table of very large rows is never loaded at once.
func (p *Postgres) CompactBackupArtifactStats(ctx context.Context, clusterID string, minBytes int, compact func(stats, blueprint string) (string, string)) (int, error) {
	rows, err := p.DB.QueryContext(ctx, `
SELECT id::text FROM backup_artifacts
WHERE cluster_id=$1 AND octet_length(stats_json) + octet_length(blueprint_json) >= $2`, clusterID, minBytes)
	if err != nil {
		return 0, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return 0, err
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	n := 0
	for _, id := range ids {
		var stats, blueprint string
		if err := p.DB.QueryRowContext(ctx, `SELECT stats_json, blueprint_json FROM backup_artifacts WHERE cluster_id=$1 AND id=$2`, clusterID, id).Scan(&stats, &blueprint); err != nil {
			continue
		}
		ns, nb := compact(stats, blueprint)
		if ns == stats && nb == blueprint {
			continue
		}
		if _, err := p.DB.ExecContext(ctx, `UPDATE backup_artifacts SET stats_json=$3, blueprint_json=$4 WHERE cluster_id=$1 AND id=$2`, clusterID, id, ns, nb); err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}

func (p *Postgres) SetBackupArtifactProtected(ctx context.Context, clusterID, id string, protected bool) error {
	res, err := p.DB.ExecContext(ctx, `
INSERT INTO backup_artifact_flags (artifact_id, cluster_id, protected, updated_at)
SELECT id, cluster_id, $3, now() FROM backup_artifacts WHERE cluster_id=$1 AND id=$2
ON CONFLICT (artifact_id) DO UPDATE SET protected=EXCLUDED.protected, updated_at=now()`, clusterID, id, protected)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (p *Postgres) ListProtectedBackupArtifacts(ctx context.Context, clusterID string) (map[string]bool, error) {
	rows, err := p.DB.QueryContext(ctx, `SELECT artifact_id::text FROM backup_artifact_flags WHERE cluster_id=$1 AND protected`, clusterID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out[id] = true
	}
	return out, rows.Err()
}

func (p *Postgres) GetBackupPolicyOffsite(ctx context.Context, clusterID, policyID string) (BackupOffsite, error) {
	var o BackupOffsite
	err := p.DB.QueryRowContext(ctx, `SELECT keep_daily, keep_weekly, keep_monthly FROM backup_policy_offsite WHERE cluster_id=$1 AND policy_id=$2`, clusterID, policyID).
		Scan(&o.KeepDaily, &o.KeepWeekly, &o.KeepMonthly)
	if errors.Is(err, sql.ErrNoRows) {
		return BackupOffsite{}, nil
	}
	return o, err
}

func (p *Postgres) UpsertBackupPolicyOffsite(ctx context.Context, clusterID, policyID string, o BackupOffsite) error {
	_, err := p.DB.ExecContext(ctx, `
INSERT INTO backup_policy_offsite (policy_id, cluster_id, keep_daily, keep_weekly, keep_monthly)
VALUES ($2, $1, $3, $4, $5)
ON CONFLICT (policy_id) DO UPDATE SET keep_daily=EXCLUDED.keep_daily, keep_weekly=EXCLUDED.keep_weekly, keep_monthly=EXCLUDED.keep_monthly`,
		clusterID, policyID, o.KeepDaily, o.KeepWeekly, o.KeepMonthly)
	return err
}
