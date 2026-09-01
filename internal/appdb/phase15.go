package appdb

import (
	"context"
	"fmt"
)

// ZFSPool is the GUID locator for a ZFS pool. Pool UUID remains desired identity.
type ZFSPool struct {
	PoolID    string
	ZPoolGUID string
	ZPoolName string
}

// ZFSDataset is the dataset/zvol locator for a volume UUID.
type ZFSDataset struct {
	VolumeID    string
	DatasetGUID string
	DatasetName string
}

func (m *Memory) UpsertZFSPool(_ context.Context, p ZFSPool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.zfsPools == nil {
		m.zfsPools = map[string]ZFSPool{}
	}
	for id, existing := range m.zfsPools {
		if existing.ZPoolGUID == p.ZPoolGUID && id != p.PoolID {
			return fmt.Errorf("zpool guid is already imported")
		}
	}
	m.zfsPools[p.PoolID] = p
	return nil
}

func (m *Memory) GetZFSPool(_ context.Context, poolID string) (*ZFSPool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.zfsPools[poolID]
	if !ok {
		return nil, nil
	}
	cp := p
	return &cp, nil
}

func (m *Memory) GetZFSPoolByGUID(_ context.Context, guid string) (*ZFSPool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, p := range m.zfsPools {
		if p.ZPoolGUID == guid {
			cp := p
			return &cp, nil
		}
	}
	return nil, nil
}

func (m *Memory) UpsertZFSDataset(_ context.Context, d ZFSDataset) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.zfsDatasets == nil {
		m.zfsDatasets = map[string]ZFSDataset{}
	}
	m.zfsDatasets[d.VolumeID] = d
	return nil
}

func (m *Memory) GetZFSDataset(_ context.Context, volumeID string) (*ZFSDataset, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	d, ok := m.zfsDatasets[volumeID]
	if !ok {
		return nil, nil
	}
	cp := d
	return &cp, nil
}
