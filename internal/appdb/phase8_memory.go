package appdb

import (
	"context"
	"fmt"
	"time"
)

func (m *Memory) DeleteWorkload(_ context.Context, clusterID, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	w, ok := m.workloads[id]
	if !ok || w.ClusterID != clusterID {
		return fmt.Errorf("workload not found")
	}
	for k, d := range m.workloadDisks {
		if d.WorkloadID == id {
			delete(m.workloadDisks, k)
		}
	}
	for k, n := range m.workloadNICs {
		if n.WorkloadID == id {
			delete(m.workloadNICs, k)
		}
	}
	if m.vmCidata != nil {
		delete(m.vmCidata, id)
	}
	if m.vmFirmware != nil {
		delete(m.vmFirmware, id)
	}
	for k, a := range m.usbAttachments {
		if a.WorkloadID == id {
			delete(m.usbAttachments, k)
		}
	}
	for k, a := range m.gpuAssignments {
		if a.WorkloadID == id {
			delete(m.gpuAssignments, k)
		}
	}
	delete(m.workloads, id)
	return nil
}

func (m *Memory) UpsertVMCidata(_ context.Context, row VMCidata) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.vmCidata == nil {
		m.vmCidata = map[string]VMCidata{}
	}
	row.UpdatedAt = time.Now().UTC()
	m.vmCidata[row.WorkloadID] = row
	return nil
}

func (m *Memory) GetVMCidata(_ context.Context, clusterID, workloadID string) (*VMCidata, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	row, ok := m.vmCidata[workloadID]
	if !ok || row.ClusterID != clusterID {
		return nil, nil
	}
	cp := row
	return &cp, nil
}

func (m *Memory) UpsertVMFirmware(_ context.Context, row VMFirmware) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.vmFirmware == nil {
		m.vmFirmware = map[string]VMFirmware{}
	}
	row.UpdatedAt = time.Now().UTC()
	m.vmFirmware[row.WorkloadID] = row
	return nil
}

func (m *Memory) GetVMFirmware(_ context.Context, clusterID, workloadID string) (*VMFirmware, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	row, ok := m.vmFirmware[workloadID]
	if !ok || row.ClusterID != clusterID {
		return nil, nil
	}
	cp := row
	return &cp, nil
}
