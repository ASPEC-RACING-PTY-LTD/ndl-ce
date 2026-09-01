package appdb

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

func (m *Memory) GetOperationByIdempotency(_ context.Context, clusterID, key string) (*Operation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if key == "" {
		return nil, nil
	}
	for _, op := range m.operations {
		if op.ClusterID == clusterID && op.IdempotencyKey == key {
			cp := op
			return &cp, nil
		}
	}
	return nil, nil
}

func (m *Memory) CreateWorkload(_ context.Context, w Workload) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.workloads == nil {
		m.workloads = map[string]Workload{}
	}
	for _, existing := range m.workloads {
		if existing.ClusterID == w.ClusterID && existing.Name == w.Name {
			return fmt.Errorf("workload name already exists")
		}
	}
	if w.CreatedAt.IsZero() {
		w.CreatedAt = time.Now().UTC()
	}
	w.UpdatedAt = w.CreatedAt
	if w.UIDMap == "" {
		w.UIDMap = "u 0 100000 65536"
	}
	if w.GIDMap == "" {
		w.GIDMap = "g 0 100000 65536"
	}
	if len(w.Devices) == 0 {
		w.Devices = json.RawMessage(`[]`)
	}
	if len(w.MigrateBlockers) == 0 {
		w.MigrateBlockers = json.RawMessage(`[]`)
	}
	if len(w.SpecJSON) == 0 {
		w.SpecJSON = json.RawMessage(`{}`)
	}
	if len(w.AppliedJSON) == 0 {
		w.AppliedJSON = json.RawMessage(`{}`)
	}
	m.workloads[w.ID] = w
	return nil
}

func (m *Memory) ListWorkloads(_ context.Context, clusterID string) ([]Workload, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Workload
	for _, w := range m.workloads {
		if w.ClusterID == clusterID {
			out = append(out, w)
		}
	}
	return out, nil
}

func (m *Memory) GetWorkload(_ context.Context, clusterID, id string) (*Workload, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	w, ok := m.workloads[id]
	if !ok || w.ClusterID != clusterID {
		return nil, nil
	}
	cp := w
	return &cp, nil
}

func (m *Memory) GetWorkloadByName(_ context.Context, clusterID, name string) (*Workload, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, w := range m.workloads {
		if w.ClusterID == clusterID && w.Name == name {
			cp := w
			return &cp, nil
		}
	}
	return nil, nil
}

func (m *Memory) GetWorkloadByIdempotency(ctx context.Context, clusterID, key string) (*Workload, error) {
	if key == "" {
		return nil, nil
	}
	m.mu.Lock()
	for _, w := range m.workloads {
		if w.ClusterID == clusterID && w.IdempotencyKey == key {
			cp := w
			m.mu.Unlock()
			return &cp, nil
		}
	}
	m.mu.Unlock()
	op, err := m.GetOperationByIdempotency(ctx, clusterID, key)
	if err != nil || op == nil || op.Message == "" {
		return nil, err
	}
	var payload struct {
		WorkloadID string `json:"workload_id"`
	}
	if json.Unmarshal([]byte(op.Message), &payload) != nil || payload.WorkloadID == "" {
		return nil, nil
	}
	return m.GetWorkload(ctx, clusterID, payload.WorkloadID)
}

func (m *Memory) UpdateWorkloadObserved(_ context.Context, w Workload) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	cur, ok := m.workloads[w.ID]
	if !ok {
		return fmt.Errorf("workload not found")
	}
	cur.Status = w.Status
	cur.Reason = w.Reason
	cur.PID = w.PID
	cur.UnitActive = w.UnitActive
	cur.ImageVerified = w.ImageVerified
	cur.Warnings = w.Warnings
	cur.MigrateReady = w.MigrateReady
	if len(w.MigrateBlockers) > 0 {
		cur.MigrateBlockers = w.MigrateBlockers
	}
	cur.UpdatedAt = time.Now().UTC()
	m.workloads[w.ID] = cur
	return nil
}

func (m *Memory) UpdateWorkloadSpec(_ context.Context, w Workload) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	cur, ok := m.workloads[w.ID]
	if !ok {
		return fmt.Errorf("workload not found")
	}
	if w.CPUs > 0 {
		cur.CPUs = w.CPUs
	}
	if w.MemoryBytes > 0 {
		cur.MemoryBytes = w.MemoryBytes
	}
	if w.DesiredPower != "" {
		cur.DesiredPower = w.DesiredPower
	}
	if len(w.SpecJSON) > 0 {
		cur.SpecJSON = w.SpecJSON
	}
	if len(w.AppliedJSON) > 0 {
		cur.AppliedJSON = w.AppliedJSON
	}
	cur.Autostart = w.Autostart
	cur.PendingRestart = w.PendingRestart
	if w.Firmware != "" {
		cur.Firmware = w.Firmware
	}
	cur.UpdatedAt = time.Now().UTC()
	m.workloads[w.ID] = cur
	return nil
}

func (m *Memory) CreateWorkloadDisk(_ context.Context, d WorkloadDisk) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.workloadDisks == nil {
		m.workloadDisks = map[string]WorkloadDisk{}
	}
	if d.CreatedAt.IsZero() {
		d.CreatedAt = time.Now().UTC()
	}
	m.workloadDisks[d.ID] = d
	return nil
}

func (m *Memory) ListWorkloadDisks(_ context.Context, clusterID, workloadID string) ([]WorkloadDisk, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []WorkloadDisk
	for _, d := range m.workloadDisks {
		if d.ClusterID == clusterID && (workloadID == "" || d.WorkloadID == workloadID) {
			out = append(out, d)
		}
	}
	return out, nil
}

func (m *Memory) CreateWorkloadNIC(_ context.Context, n WorkloadNIC) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.workloadNICs == nil {
		m.workloadNICs = map[string]WorkloadNIC{}
	}
	if n.CreatedAt.IsZero() {
		n.CreatedAt = time.Now().UTC()
	}
	m.workloadNICs[n.ID] = n
	return nil
}

func (m *Memory) ListWorkloadNICs(_ context.Context, clusterID, workloadID string) ([]WorkloadNIC, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []WorkloadNIC
	for _, n := range m.workloadNICs {
		if n.ClusterID == clusterID && (workloadID == "" || n.WorkloadID == workloadID) {
			out = append(out, n)
		}
	}
	return out, nil
}

func (m *Memory) UpdateWorkloadNIC(_ context.Context, n WorkloadNIC) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	cur, ok := m.workloadNICs[n.ID]
	if !ok {
		return fmt.Errorf("workload nic not found")
	}
	cur.IPv4 = n.IPv4
	if n.PCIAddr != "" {
		cur.PCIAddr = n.PCIAddr
	}
	if n.Model != "" {
		cur.Model = n.Model
	}
	m.workloadNICs[n.ID] = cur
	return nil
}
