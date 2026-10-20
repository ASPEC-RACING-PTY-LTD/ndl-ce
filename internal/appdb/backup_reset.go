package appdb

import "context"

// Clearing backup records. Policies, targets (and their credentials),
// per-workload options and workspace settings stay; everything a backup run
// left behind goes.

// DeleteBackupRunsWithoutArtifacts removes a policy's finished runs that no
// restore point refers to (deleting a run would delete its restore point's
// record with it). It returns how many it removed and how many it kept.
func (m *Memory) DeleteBackupRunsWithoutArtifacts(_ context.Context, clusterID, policyID string) (int, int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	used := map[string]bool{}
	for _, a := range m.backupArtifacts {
		used[a.RunID] = true
	}
	removed, kept := 0, 0
	for id, r := range m.backupRuns {
		if r.ClusterID != clusterID || r.PolicyID != policyID || r.Status == BackupRunning {
			continue
		}
		if used[id] {
			kept++
			continue
		}
		delete(m.backupRuns, id)
		removed++
	}
	return removed, kept, nil
}

// ResetBackupRecords deletes every backup run, restore point and upload
// record of a cluster.
func (m *Memory) ResetBackupRecords(_ context.Context, clusterID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, a := range m.backupArtifacts {
		if a.ClusterID == clusterID {
			delete(m.backupArtifacts, id)
			delete(m.protectedArtifacts, id)
		}
	}
	for id, r := range m.backupRuns {
		if r.ClusterID == clusterID {
			delete(m.backupRuns, id)
		}
	}
	for id, pol := range m.backupPolicies {
		if pol.ClusterID == clusterID {
			pol.RunQueued = false
			m.backupPolicies[id] = pol
		}
	}
	for key, h := range m.backupGitHolds {
		if h.ClusterID == clusterID {
			delete(m.backupGitHolds, key)
		}
	}
	return nil
}

func (p *Postgres) DeleteBackupRunsWithoutArtifacts(ctx context.Context, clusterID, policyID string) (int, int, error) {
	res, err := p.DB.ExecContext(ctx, `
DELETE FROM backup_runs r
WHERE r.cluster_id=$1 AND r.policy_id=$2 AND r.status <> 'running'
  AND NOT EXISTS (SELECT 1 FROM backup_artifacts a WHERE a.run_id = r.id)`, clusterID, policyID)
	if err != nil {
		return 0, 0, err
	}
	n, _ := res.RowsAffected()
	var kept int
	if err := p.DB.QueryRowContext(ctx, `SELECT count(*) FROM backup_runs WHERE cluster_id=$1 AND policy_id=$2`, clusterID, policyID).Scan(&kept); err != nil {
		return int(n), 0, err
	}
	return int(n), kept, nil
}

func (p *Postgres) ResetBackupRecords(ctx context.Context, clusterID string) error {
	tx, err := p.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	for _, q := range []string{
		`DELETE FROM backup_upload_jobs WHERE cluster_id=$1`,
		`DELETE FROM backup_restore_points WHERE cluster_id=$1`,
		`DELETE FROM backup_artifact_flags WHERE cluster_id=$1`,
		`DELETE FROM backup_artifacts WHERE cluster_id=$1`,
		`DELETE FROM backup_runs WHERE cluster_id=$1`,
		`DELETE FROM backup_git_holds WHERE cluster_id=$1`,
		`UPDATE backup_repositories SET size_bytes=0, updated_at=now() WHERE cluster_id=$1`,
		`UPDATE backup_policies SET run_queued=false WHERE cluster_id=$1`,
	} {
		if _, err := tx.ExecContext(ctx, q, clusterID); err != nil {
			return err
		}
	}
	return tx.Commit()
}
