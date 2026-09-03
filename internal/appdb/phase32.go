package appdb

import (
	"context"
	"fmt"
	"sort"
	"time"
)

// MigrateJob is one ownership transfer. Identity is the job UUID.
type MigrateJob struct {
	ID            string
	ClusterID     string
	WorkloadID    string
	OperationID   string
	SourceNodeID  string
	DestNodeID    string
	Mode          string
	State         string
	EpochAtStart  int
	SourceRunning bool
	DestRunning   bool
	Reason        string
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

func (m *Memory) SetWorkloadDesiredNode(_ context.Context, clusterID, workloadID, destNodeID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	cur, ok := m.workloads[workloadID]
	if !ok || cur.ClusterID != clusterID {
		return fmt.Errorf("workload not found")
	}
	cur.DesiredNodeID = destNodeID
	cur.UpdatedAt = time.Now().UTC()
	m.workloads[workloadID] = cur
	return nil
}

func (m *Memory) TransferWorkloadOwnership(_ context.Context, clusterID, workloadID, destNodeID string, expectedEpoch int) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	cur, ok := m.workloads[workloadID]
	if !ok || cur.ClusterID != clusterID {
		return 0, fmt.Errorf("workload not found")
	}
	if cur.OwnershipEpoch != expectedEpoch {
		return cur.OwnershipEpoch, fmt.Errorf("ownership epoch mismatch")
	}
	cur.NodeID = destNodeID
	cur.OwnerNodeID = destNodeID
	cur.DesiredNodeID = destNodeID
	cur.OwnershipEpoch = expectedEpoch + 1
	cur.UpdatedAt = time.Now().UTC()
	m.workloads[workloadID] = cur
	return cur.OwnershipEpoch, nil
}

func (m *Memory) CreateMigrateJob(_ context.Context, j MigrateJob) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.migrateJobs == nil {
		m.migrateJobs = map[string]MigrateJob{}
	}
	if j.CreatedAt.IsZero() {
		j.CreatedAt = time.Now().UTC()
	}
	j.UpdatedAt = j.CreatedAt
	m.migrateJobs[j.ID] = j
	return nil
}

func (m *Memory) GetMigrateJob(_ context.Context, clusterID, id string) (*MigrateJob, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	j, ok := m.migrateJobs[id]
	if !ok || j.ClusterID != clusterID {
		return nil, nil
	}
	cp := j
	return &cp, nil
}

func (m *Memory) UpdateMigrateJob(_ context.Context, j MigrateJob) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	cur, ok := m.migrateJobs[j.ID]
	if !ok {
		return fmt.Errorf("migrate job not found")
	}
	j.CreatedAt = cur.CreatedAt
	j.UpdatedAt = time.Now().UTC()
	m.migrateJobs[j.ID] = j
	return nil
}

func (m *Memory) ListMigrateJobs(_ context.Context, clusterID string, limit int) ([]MigrateJob, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if limit <= 0 {
		limit = 50
	}
	var out []MigrateJob
	for _, j := range m.migrateJobs {
		if j.ClusterID == clusterID {
			out = append(out, j)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].CreatedAt.After(out[j].CreatedAt)
		}
		return out[i].ID < out[j].ID
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}
