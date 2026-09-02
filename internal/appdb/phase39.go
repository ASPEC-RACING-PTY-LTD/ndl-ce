package appdb

import (
	"context"
	"time"
)

// DistributedPool is the locator row for an attached external cluster. Pool UUID remains identity.
type DistributedPool struct {
	PoolID    string
	ClusterID string
	Locator   string
	CephPool  string
	CephUser  string
	FSID      string
}

// DistributedOSD is desired OSD bring-up state. Enabling the feature does not create rows.
type DistributedOSD struct {
	ID        string
	ClusterID string
	NodeID    string
	PoolID    string
	Disk      string
	OSDNumber int
	Status    string
	Reason    string
	CreatedAt time.Time
	UpdatedAt time.Time
}

func (m *Memory) UpsertDistributedPool(_ context.Context, d DistributedPool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.distributedPools == nil {
		m.distributedPools = map[string]DistributedPool{}
	}
	m.distributedPools[d.PoolID] = d
	return nil
}

func (m *Memory) GetDistributedPool(_ context.Context, poolID string) (*DistributedPool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	d, ok := m.distributedPools[poolID]
	if !ok {
		return nil, nil
	}
	cp := d
	return &cp, nil
}

func (m *Memory) UpsertDistributedSecret(_ context.Context, poolID, cephxKey string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.distributedSecrets == nil {
		m.distributedSecrets = map[string]string{}
	}
	m.distributedSecrets[poolID] = cephxKey
	return nil
}

func (m *Memory) DistributedSecret(_ context.Context, poolID string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.distributedSecrets[poolID], nil
}

func (m *Memory) CreateDistributedOSD(_ context.Context, o DistributedOSD) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.distributedOSDs == nil {
		m.distributedOSDs = map[string]DistributedOSD{}
	}
	m.distributedOSDs[o.ID] = o
	return nil
}

func (m *Memory) ListDistributedOSDs(_ context.Context, clusterID string) ([]DistributedOSD, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []DistributedOSD
	for _, o := range m.distributedOSDs {
		if o.ClusterID == clusterID {
			out = append(out, o)
		}
	}
	return out, nil
}

func (m *Memory) UpdateDistributedOSD(_ context.Context, o DistributedOSD) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.distributedOSDs == nil {
		m.distributedOSDs = map[string]DistributedOSD{}
	}
	m.distributedOSDs[o.ID] = o
	return nil
}
