package appdb

import (
	"context"
	"fmt"
)

// LVMVG is the VG UUID locator for an LVM-thin pool. Pool UUID remains desired identity.
type LVMVG struct {
	PoolID   string
	VGUUID   string
	VGName   string
	ThinPool string
}

// LVMLV is the LV UUID locator for a volume UUID.
type LVMLV struct {
	VolumeID string
	LVUUID   string
	LVName   string
}

func (m *Memory) UpsertLVMVG(_ context.Context, v LVMVG) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.lvmVGs == nil {
		m.lvmVGs = map[string]LVMVG{}
	}
	if v.ThinPool == "" {
		v.ThinPool = "thinpool"
	}
	for id, existing := range m.lvmVGs {
		if existing.VGUUID != "" && existing.VGUUID != "pending" && existing.VGUUID == v.VGUUID && id != v.PoolID {
			return fmt.Errorf("vg uuid is already imported")
		}
	}
	m.lvmVGs[v.PoolID] = v
	return nil
}

func (m *Memory) GetLVMVG(_ context.Context, poolID string) (*LVMVG, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	v, ok := m.lvmVGs[poolID]
	if !ok {
		return nil, nil
	}
	cp := v
	return &cp, nil
}

func (m *Memory) GetLVMVGByUUID(_ context.Context, vgUUID string) (*LVMVG, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, v := range m.lvmVGs {
		if v.VGUUID == vgUUID {
			cp := v
			return &cp, nil
		}
	}
	return nil, nil
}

func (m *Memory) UpsertLVMLV(_ context.Context, lv LVMLV) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.lvmLVs == nil {
		m.lvmLVs = map[string]LVMLV{}
	}
	m.lvmLVs[lv.VolumeID] = lv
	return nil
}

func (m *Memory) GetLVMLV(_ context.Context, volumeID string) (*LVMLV, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	lv, ok := m.lvmLVs[volumeID]
	if !ok {
		return nil, nil
	}
	cp := lv
	return &cp, nil
}
