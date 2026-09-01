package appdb

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"time"
)

// VMTemplate is a snapshot plus a redacted desired spec. It is not a running VM.
type VMTemplate struct {
	ID               string
	ClusterID        string
	Name             string
	SourceWorkloadID string
	SnapshotID       string
	SpecJSON         json.RawMessage
	CreatedAt        time.Time
}

// USBAttachment is an exclusive usb-host claim. It is not a QEMU argv string.
type USBAttachment struct {
	ID         string
	ClusterID  string
	WorkloadID string
	Address    string
	Vendor     string
	Product    string
	Exclusive  bool
	CreatedAt  time.Time
}

func (m *Memory) DeleteVolume(_ context.Context, clusterID, volumeID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	v, ok := m.volumes[volumeID]
	if !ok || v.ClusterID != clusterID {
		return fmt.Errorf("volume not found")
	}
	delete(m.volumes, volumeID)
	return nil
}

func (m *Memory) CreateVMTemplate(_ context.Context, t VMTemplate) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.vmTemplates == nil {
		m.vmTemplates = map[string]VMTemplate{}
	}
	if t.CreatedAt.IsZero() {
		t.CreatedAt = time.Now().UTC()
	}
	m.vmTemplates[t.ID] = t
	return nil
}

func (m *Memory) ListVMTemplates(_ context.Context, clusterID string) ([]VMTemplate, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []VMTemplate
	for _, t := range m.vmTemplates {
		if t.ClusterID == clusterID {
			out = append(out, t)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func (m *Memory) GetVMTemplate(_ context.Context, clusterID, id string) (*VMTemplate, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	t, ok := m.vmTemplates[id]
	if !ok || t.ClusterID != clusterID {
		return nil, nil
	}
	cp := t
	return &cp, nil
}

func (m *Memory) CreateUSBAttachment(_ context.Context, a USBAttachment) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.usbAttachments == nil {
		m.usbAttachments = map[string]USBAttachment{}
	}
	if a.CreatedAt.IsZero() {
		a.CreatedAt = time.Now().UTC()
	}
	for _, existing := range m.usbAttachments {
		if existing.ClusterID == a.ClusterID && existing.Address == a.Address && existing.Exclusive {
			return fmt.Errorf("usb device is already exclusively claimed")
		}
	}
	m.usbAttachments[a.ID] = a
	return nil
}

func (m *Memory) ListUSBAttachments(_ context.Context, clusterID, workloadID string) ([]USBAttachment, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []USBAttachment
	for _, a := range m.usbAttachments {
		if a.ClusterID == clusterID && (workloadID == "" || a.WorkloadID == workloadID) {
			out = append(out, a)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Address < out[j].Address })
	return out, nil
}

func (m *Memory) DeleteUSBAttachment(_ context.Context, clusterID, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	a, ok := m.usbAttachments[id]
	if !ok || a.ClusterID != clusterID {
		return fmt.Errorf("usb attachment not found")
	}
	delete(m.usbAttachments, id)
	return nil
}
