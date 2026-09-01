package appdb

import (
	"context"
	"fmt"
	"sort"
	"time"
)

// GPUAssignment is a desired GPU claim. It is not proof of a live VFIO bind.
type GPUAssignment struct {
	ID          string
	ClusterID   string
	GPUID       string
	WorkloadID  string
	Mode        string
	Exclusive   bool
	IOMMUGroup  string
	PCIDevices  []string
	DeviceNodes []string
	Status      string
	Reason      string
	CreatedAt   time.Time
}

func (m *Memory) CreateGPUAssignment(_ context.Context, a GPUAssignment) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.gpuAssignments == nil {
		m.gpuAssignments = map[string]GPUAssignment{}
	}
	if a.CreatedAt.IsZero() {
		a.CreatedAt = time.Now().UTC()
	}
	for _, existing := range m.gpuAssignments {
		if existing.ClusterID != a.ClusterID {
			continue
		}
		if existing.GPUID == a.GPUID && (existing.Exclusive || a.Exclusive) {
			return fmt.Errorf("gpu is already exclusively claimed")
		}
		if a.IOMMUGroup != "" && existing.IOMMUGroup == a.IOMMUGroup && (existing.Exclusive || a.Exclusive) {
			return fmt.Errorf("gpu is already exclusively claimed")
		}
	}
	m.gpuAssignments[a.ID] = a
	return nil
}

func (m *Memory) ListGPUAssignments(_ context.Context, clusterID string) ([]GPUAssignment, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []GPUAssignment
	for _, a := range m.gpuAssignments {
		if a.ClusterID == clusterID {
			out = append(out, a)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].GPUID < out[j].GPUID })
	return out, nil
}

func (m *Memory) ListGPUAssignmentsForGPU(_ context.Context, clusterID, gpuID string) ([]GPUAssignment, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []GPUAssignment
	for _, a := range m.gpuAssignments {
		if a.ClusterID == clusterID && a.GPUID == gpuID {
			out = append(out, a)
		}
	}
	return out, nil
}

func (m *Memory) GetGPUAssignment(_ context.Context, clusterID, id string) (*GPUAssignment, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	a, ok := m.gpuAssignments[id]
	if !ok || a.ClusterID != clusterID {
		return nil, nil
	}
	cp := a
	return &cp, nil
}

func (m *Memory) DeleteGPUAssignment(_ context.Context, clusterID, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	a, ok := m.gpuAssignments[id]
	if !ok || a.ClusterID != clusterID {
		return fmt.Errorf("assignment not found")
	}
	delete(m.gpuAssignments, id)
	return nil
}
