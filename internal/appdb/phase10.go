package appdb

import (
	"context"
	"fmt"
	"sort"
	"time"
)

const (
	SnapshotAvailable = "available"
	MechanismOverlay  = "qcow2-overlay"
)

// Snapshot is a point-in-time disk restore object. It is not a backup.
type Snapshot struct {
	ID         string
	ClusterID  string
	WorkloadID string
	VolumeID   string
	Name       string
	PurposeTag string
	Mechanism  string
	BackendRef string
	ParentID   string
	ChainDepth int
	Status     string
	CreatedAt  time.Time
}

func (m *Memory) CreateSnapshot(_ context.Context, s Snapshot) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.snapshots == nil {
		m.snapshots = map[string]Snapshot{}
	}
	if s.CreatedAt.IsZero() {
		s.CreatedAt = time.Now().UTC()
	}
	m.snapshots[s.ID] = s
	return nil
}

func (m *Memory) ListSnapshots(_ context.Context, clusterID, workloadID string) ([]Snapshot, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Snapshot
	for _, s := range m.snapshots {
		if s.ClusterID == clusterID && s.WorkloadID == workloadID {
			out = append(out, s)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out, nil
}

func (m *Memory) GetSnapshot(_ context.Context, clusterID, id string) (*Snapshot, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.snapshots[id]
	if !ok || s.ClusterID != clusterID {
		return nil, nil
	}
	cp := s
	return &cp, nil
}

func (m *Memory) UpdateVolumeLocator(_ context.Context, clusterID, id, backendRef string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	v, ok := m.volumes[id]
	if !ok || v.ClusterID != clusterID {
		return fmt.Errorf("volume not found")
	}
	v.BackendRef = backendRef
	v.UpdatedAt = time.Now().UTC()
	m.volumes[id] = v
	return nil
}
