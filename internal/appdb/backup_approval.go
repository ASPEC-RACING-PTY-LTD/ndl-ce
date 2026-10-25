package appdb

import (
	"context"
	"database/sql"
	"sync"
	"time"
)

// Backup approval states. A workload created after an "All workloads"
// backup policy is pending until someone approves or denies it; denying
// adds it to those policies' Leave out lists.
const (
	BackupApprovalPending  = "pending"
	BackupApprovalApproved = "approved"
	BackupApprovalDenied   = "denied"
)

// BackupApproval is whether "All workloads" backup policies may back up one
// workload.
type BackupApproval struct {
	ClusterID  string
	WorkloadID string
	State      string
	DecidedBy  string
	CreatedAt  time.Time
	DecidedAt  *time.Time
}

type approvalMem struct {
	mu   sync.Mutex
	rows map[string]BackupApproval
}

func (m *Memory) BackupApprovals(_ context.Context, clusterID string) (map[string]BackupApproval, error) {
	m.approvals.mu.Lock()
	defer m.approvals.mu.Unlock()
	out := map[string]BackupApproval{}
	for _, a := range m.approvals.rows {
		if a.ClusterID == clusterID {
			out[a.WorkloadID] = a
		}
	}
	return out, nil
}

func (m *Memory) PutBackupApproval(_ context.Context, a BackupApproval) error {
	m.approvals.mu.Lock()
	defer m.approvals.mu.Unlock()
	if m.approvals.rows == nil {
		m.approvals.rows = map[string]BackupApproval{}
	}
	if a.CreatedAt.IsZero() {
		a.CreatedAt = time.Now().UTC()
	}
	m.approvals.rows[a.ClusterID+"/"+a.WorkloadID] = a
	return nil
}

func (m *Memory) DeleteBackupApproval(_ context.Context, clusterID, workloadID string) error {
	m.approvals.mu.Lock()
	defer m.approvals.mu.Unlock()
	delete(m.approvals.rows, clusterID+"/"+workloadID)
	return nil
}

func (p *Postgres) BackupApprovals(ctx context.Context, clusterID string) (map[string]BackupApproval, error) {
	rows, err := p.DB.QueryContext(ctx, `
SELECT cluster_id::text, workload_id::text, state, decided_by, created_at, decided_at
FROM backup_workload_approvals WHERE cluster_id=$1`, clusterID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]BackupApproval{}
	for rows.Next() {
		var a BackupApproval
		var decided sql.NullTime
		if err := rows.Scan(&a.ClusterID, &a.WorkloadID, &a.State, &a.DecidedBy, &a.CreatedAt, &decided); err != nil {
			return nil, err
		}
		if decided.Valid {
			t := decided.Time
			a.DecidedAt = &t
		}
		out[a.WorkloadID] = a
	}
	return out, rows.Err()
}

func (p *Postgres) PutBackupApproval(ctx context.Context, a BackupApproval) error {
	if a.CreatedAt.IsZero() {
		a.CreatedAt = time.Now().UTC()
	}
	_, err := p.DB.ExecContext(ctx, `
INSERT INTO backup_workload_approvals (cluster_id, workload_id, state, decided_by, created_at, decided_at)
VALUES ($1,$2,$3,$4,$5,$6)
ON CONFLICT (cluster_id, workload_id) DO UPDATE SET state=EXCLUDED.state, decided_by=EXCLUDED.decided_by, decided_at=EXCLUDED.decided_at`,
		a.ClusterID, a.WorkloadID, a.State, a.DecidedBy, a.CreatedAt, a.DecidedAt)
	return err
}

func (p *Postgres) DeleteBackupApproval(ctx context.Context, clusterID, workloadID string) error {
	if !ValidUUID(workloadID) {
		return nil
	}
	_, err := p.DB.ExecContext(ctx, `DELETE FROM backup_workload_approvals WHERE cluster_id=$1 AND workload_id=$2`, clusterID, workloadID)
	return err
}
