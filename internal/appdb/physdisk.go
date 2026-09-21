package appdb

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"time"
)

// PhysicalDiskAssignment is exclusive ownership of a host physical disk by one VM.
// It is not a storage volume. Deleting the row never wipes the device.
type PhysicalDiskAssignment struct {
	ID         string
	ClusterID  string
	WorkloadID string
	DeviceID   string
	ByIDPath   string
	KernelName string
	Model      string
	Serial     string
	SizeBytes  int64
	Role       string
	Slot       int
	Bus        string
	CreatedAt  time.Time
}

func (m *Memory) CreatePhysicalDiskAssignment(_ context.Context, a PhysicalDiskAssignment) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.physDisks == nil {
		m.physDisks = map[string]PhysicalDiskAssignment{}
	}
	if a.DeviceID == "" || a.WorkloadID == "" {
		return fmt.Errorf("physical disk assignment is incomplete")
	}
	for _, existing := range m.physDisks {
		if existing.ClusterID == a.ClusterID && existing.DeviceID == a.DeviceID {
			return fmt.Errorf("physical disk is already assigned")
		}
	}
	if a.CreatedAt.IsZero() {
		a.CreatedAt = time.Now().UTC()
	}
	m.physDisks[a.ClusterID+"/"+a.DeviceID] = a
	return nil
}

func (m *Memory) ListPhysicalDiskAssignments(_ context.Context, clusterID, workloadID string) ([]PhysicalDiskAssignment, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []PhysicalDiskAssignment
	for _, a := range m.physDisks {
		if a.ClusterID != clusterID {
			continue
		}
		if workloadID != "" && a.WorkloadID != workloadID {
			continue
		}
		out = append(out, a)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Slot != out[j].Slot {
			return out[i].Slot < out[j].Slot
		}
		return out[i].DeviceID < out[j].DeviceID
	})
	return out, nil
}

func (m *Memory) GetPhysicalDiskAssignment(_ context.Context, clusterID, deviceID string) (*PhysicalDiskAssignment, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	a, ok := m.physDisks[clusterID+"/"+deviceID]
	if !ok {
		return nil, nil
	}
	cp := a
	return &cp, nil
}

func (m *Memory) DeletePhysicalDiskAssignment(_ context.Context, clusterID, deviceID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.physDisks, clusterID+"/"+deviceID)
	return nil
}

func (p *Postgres) CreatePhysicalDiskAssignment(ctx context.Context, a PhysicalDiskAssignment) error {
	if a.CreatedAt.IsZero() {
		a.CreatedAt = time.Now().UTC()
	}
	if a.Role == "" {
		a.Role = "data"
	}
	if a.Bus == "" {
		a.Bus = "ahci"
	}
	_, err := p.DB.ExecContext(ctx, `
INSERT INTO physical_disk_assignments (
  id, cluster_id, workload_id, device_id, by_id_path, kernel_name, model, serial,
  size_bytes, role, slot, bus, created_at
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)`,
		a.ID, a.ClusterID, a.WorkloadID, a.DeviceID, a.ByIDPath, a.KernelName, a.Model, a.Serial,
		a.SizeBytes, a.Role, a.Slot, a.Bus, a.CreatedAt)
	if err != nil {
		return fmt.Errorf("physical disk is already assigned")
	}
	return nil
}

func (p *Postgres) ListPhysicalDiskAssignments(ctx context.Context, clusterID, workloadID string) ([]PhysicalDiskAssignment, error) {
	rows, err := p.DB.QueryContext(ctx, `
SELECT id::text, cluster_id::text, workload_id::text, device_id, by_id_path, kernel_name, model, serial,
       size_bytes, role, slot, bus, created_at
FROM physical_disk_assignments
WHERE cluster_id=$1 AND ($2='' OR workload_id::text=$2)
ORDER BY slot, device_id`, clusterID, workloadID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PhysicalDiskAssignment
	for rows.Next() {
		var a PhysicalDiskAssignment
		if err := rows.Scan(&a.ID, &a.ClusterID, &a.WorkloadID, &a.DeviceID, &a.ByIDPath, &a.KernelName, &a.Model, &a.Serial, &a.SizeBytes, &a.Role, &a.Slot, &a.Bus, &a.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (p *Postgres) GetPhysicalDiskAssignment(ctx context.Context, clusterID, deviceID string) (*PhysicalDiskAssignment, error) {
	row := p.DB.QueryRowContext(ctx, `
SELECT id::text, cluster_id::text, workload_id::text, device_id, by_id_path, kernel_name, model, serial,
       size_bytes, role, slot, bus, created_at
FROM physical_disk_assignments WHERE cluster_id=$1 AND device_id=$2`, clusterID, deviceID)
	var a PhysicalDiskAssignment
	if err := row.Scan(&a.ID, &a.ClusterID, &a.WorkloadID, &a.DeviceID, &a.ByIDPath, &a.KernelName, &a.Model, &a.Serial, &a.SizeBytes, &a.Role, &a.Slot, &a.Bus, &a.CreatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &a, nil
}

func (p *Postgres) DeletePhysicalDiskAssignment(ctx context.Context, clusterID, deviceID string) error {
	_, err := p.DB.ExecContext(ctx, `DELETE FROM physical_disk_assignments WHERE cluster_id=$1 AND device_id=$2`, clusterID, deviceID)
	return err
}
