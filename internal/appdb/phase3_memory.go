package appdb

import (
	"context"
	"fmt"
	"time"
)

func (m *Memory) CreateStoragePool(_ context.Context, p StoragePool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.pools == nil {
		m.pools = map[string]StoragePool{}
	}
	for _, existing := range m.pools {
		if existing.ClusterID == p.ClusterID && existing.Name == p.Name {
			return fmt.Errorf("pool name already exists")
		}
	}
	if p.CreatedAt.IsZero() {
		p.CreatedAt = time.Now().UTC()
	}
	p.UpdatedAt = p.CreatedAt
	m.pools[p.ID] = p
	return nil
}

func (m *Memory) ListStoragePools(_ context.Context, clusterID string) ([]StoragePool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []StoragePool
	for _, p := range m.pools {
		if p.ClusterID == clusterID {
			out = append(out, p)
		}
	}
	return out, nil
}

func (m *Memory) GetStoragePool(_ context.Context, clusterID, id string) (*StoragePool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.pools[id]
	if !ok || p.ClusterID != clusterID {
		return nil, nil
	}
	cp := p
	return &cp, nil
}

func (m *Memory) UpdateStoragePoolObserved(_ context.Context, p StoragePool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	cur, ok := m.pools[p.ID]
	if !ok {
		return fmt.Errorf("pool not found")
	}
	cur.Status = p.Status
	cur.Reason = p.Reason
	cur.Warnings = p.Warnings
	cur.WarningText = p.WarningText
	cur.Capabilities = p.Capabilities
	cur.UsableBytes = p.UsableBytes
	cur.AllocatedBytes = p.AllocatedBytes
	cur.ProvisionedBytes = p.ProvisionedBytes
	cur.TotalBytes = p.TotalBytes
	cur.UpdatedAt = time.Now().UTC()
	m.pools[p.ID] = cur
	return nil
}

func (m *Memory) CreateVolume(_ context.Context, v Volume) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.volumes == nil {
		m.volumes = map[string]Volume{}
	}
	if _, ok := m.volumes[v.ID]; ok {
		return fmt.Errorf("volume exists")
	}
	if v.CreatedAt.IsZero() {
		v.CreatedAt = time.Now().UTC()
	}
	v.UpdatedAt = v.CreatedAt
	m.volumes[v.ID] = v
	return nil
}

func (m *Memory) ListVolumes(_ context.Context, clusterID, poolID string) ([]Volume, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Volume
	for _, v := range m.volumes {
		if v.ClusterID == clusterID && (poolID == "" || v.PoolID == poolID) {
			out = append(out, v)
		}
	}
	return out, nil
}

func (m *Memory) GetVolume(_ context.Context, clusterID, id string) (*Volume, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	v, ok := m.volumes[id]
	if !ok || v.ClusterID != clusterID {
		return nil, nil
	}
	cp := v
	return &cp, nil
}

func (m *Memory) UpdateVolumeObserved(_ context.Context, v Volume) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	cur, ok := m.volumes[v.ID]
	if !ok {
		return fmt.Errorf("volume not found")
	}
	cur.Status = v.Status
	cur.XattrState = v.XattrState
	cur.AllocatedBytes = v.AllocatedBytes
	cur.UpdatedAt = time.Now().UTC()
	m.volumes[v.ID] = cur
	return nil
}

func (m *Memory) CreateLibraryItem(_ context.Context, item LibraryItem) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.library == nil {
		m.library = map[string]LibraryItem{}
	}
	if item.ChecksumSHA256 != "" {
		for _, existing := range m.library {
			if existing.PoolID == item.PoolID && existing.ChecksumSHA256 == item.ChecksumSHA256 {
				return fmt.Errorf("duplicate checksum")
			}
		}
	}
	if item.CreatedAt.IsZero() {
		item.CreatedAt = time.Now().UTC()
	}
	item.UpdatedAt = item.CreatedAt
	m.library[item.ID] = item
	return nil
}

func (m *Memory) ListLibraryItems(_ context.Context, clusterID, poolID string) ([]LibraryItem, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []LibraryItem
	for _, item := range m.library {
		if item.ClusterID == clusterID && (poolID == "" || item.PoolID == poolID) {
			out = append(out, item)
		}
	}
	return out, nil
}

func (m *Memory) GetLibraryItem(_ context.Context, clusterID, id string) (*LibraryItem, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	item, ok := m.library[id]
	if !ok || item.ClusterID != clusterID {
		return nil, nil
	}
	cp := item
	return &cp, nil
}

func (m *Memory) GetLibraryByChecksum(_ context.Context, poolID, checksum string) (*LibraryItem, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, item := range m.library {
		if item.PoolID == poolID && item.ChecksumSHA256 == checksum {
			cp := item
			return &cp, nil
		}
	}
	return nil, nil
}

func (m *Memory) UpdateLibraryObserved(_ context.Context, item LibraryItem) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	cur, ok := m.library[item.ID]
	if !ok {
		return fmt.Errorf("library item not found")
	}
	cur.Status = item.Status
	cur.UpdatedAt = time.Now().UTC()
	m.library[item.ID] = cur
	return nil
}
