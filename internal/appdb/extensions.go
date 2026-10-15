package appdb

import (
	"context"
	"sort"
	"time"
)

// Extension states.
const (
	ExtensionInstalling = "installing"
	ExtensionInstalled  = "installed"
	ExtensionFailed     = "failed"
	ExtensionMissing    = "missing"
)

// WorkloadExtension is one optional extra on a system container.
type WorkloadExtension struct {
	WorkloadID string
	ClusterID  string
	ExtraID    string
	// Requested is true when someone chose this extra for the workload.
	Requested bool
	State     string
	Version   string
	Package   string
	Detail    string
	Message   string
	UpdatedAt time.Time
}

func (m *Memory) ListWorkloadExtensions(_ context.Context, clusterID, workloadID string) ([]WorkloadExtension, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []WorkloadExtension
	for _, x := range m.extensions {
		if x.ClusterID == clusterID && x.WorkloadID == workloadID {
			out = append(out, x)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ExtraID < out[j].ExtraID })
	return out, nil
}

func (m *Memory) PutWorkloadExtension(_ context.Context, x WorkloadExtension) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.extensions == nil {
		m.extensions = map[string]WorkloadExtension{}
	}
	x.UpdatedAt = time.Now().UTC()
	m.extensions[x.WorkloadID+"|"+x.ExtraID] = x
	return nil
}

func (p *Postgres) ListWorkloadExtensions(ctx context.Context, clusterID, workloadID string) ([]WorkloadExtension, error) {
	rows, err := p.DB.QueryContext(ctx, `SELECT workload_id::text, cluster_id::text, extra_id, requested, state, version, package, detail, message, updated_at
FROM workload_extensions WHERE cluster_id=$1 AND workload_id::text=$2 ORDER BY extra_id`, clusterID, workloadID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []WorkloadExtension
	for rows.Next() {
		var x WorkloadExtension
		if err := rows.Scan(&x.WorkloadID, &x.ClusterID, &x.ExtraID, &x.Requested, &x.State, &x.Version, &x.Package, &x.Detail, &x.Message, &x.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

func (p *Postgres) PutWorkloadExtension(ctx context.Context, x WorkloadExtension) error {
	_, err := p.DB.ExecContext(ctx, `
INSERT INTO workload_extensions (workload_id, cluster_id, extra_id, requested, state, version, package, detail, message, updated_at)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,now())
ON CONFLICT (workload_id, extra_id) DO UPDATE SET requested=EXCLUDED.requested, state=EXCLUDED.state, version=EXCLUDED.version,
  package=EXCLUDED.package, detail=EXCLUDED.detail, message=EXCLUDED.message, updated_at=now()`,
		x.WorkloadID, x.ClusterID, x.ExtraID, x.Requested, x.State, x.Version, x.Package, x.Detail, x.Message)
	return err
}
