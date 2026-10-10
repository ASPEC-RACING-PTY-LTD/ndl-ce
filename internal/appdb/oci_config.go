package appdb

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// UpdateWorkloadImage records a new image and privilege for a reconfigured
// OCI container.
func (m *Memory) UpdateWorkloadImage(_ context.Context, clusterID, id, image string, privileged bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	cur, ok := m.workloads[id]
	if !ok || cur.ClusterID != clusterID {
		return fmt.Errorf("workload not found")
	}
	cur.ImagePin = image
	cur.Privileged = privileged
	cur.UpdatedAt = time.Now().UTC()
	m.workloads[id] = cur
	return nil
}

func (p *Postgres) UpdateWorkloadImage(ctx context.Context, clusterID, id, image string, privileged bool) error {
	res, err := p.DB.ExecContext(ctx, `UPDATE workloads SET image_pin=$3, privileged=$4, updated_at=now() WHERE cluster_id=$1 AND id=$2`,
		clusterID, id, image, privileged)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return errors.New("workload not found")
	}
	return nil
}

// DeleteWorkloadDisk detaches one volume from a workload. The volume stays.
func (m *Memory) DeleteWorkloadDisk(_ context.Context, clusterID, workloadID, volumeID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, d := range m.workloadDisks {
		if d.ClusterID == clusterID && d.WorkloadID == workloadID && d.VolumeID == volumeID {
			delete(m.workloadDisks, id)
		}
	}
	return nil
}

func (p *Postgres) DeleteWorkloadDisk(ctx context.Context, clusterID, workloadID, volumeID string) error {
	_, err := p.DB.ExecContext(ctx, `DELETE FROM workload_disks WHERE cluster_id=$1 AND workload_id=$2 AND volume_id=$3`,
		clusterID, workloadID, volumeID)
	return err
}

// DeleteStackMember removes a container from its group. The container stays.
func (m *Memory) DeleteStackMember(_ context.Context, clusterID, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	mem, ok := m.stackMembers[id]
	if !ok || mem.ClusterID != clusterID {
		return fmt.Errorf("stack member not found")
	}
	delete(m.stackMembers, id)
	return nil
}

func (p *Postgres) DeleteStackMember(ctx context.Context, clusterID, id string) error {
	res, err := p.DB.ExecContext(ctx, `DELETE FROM stack_members WHERE cluster_id=$1 AND id=$2`, clusterID, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return errors.New("stack member not found")
	}
	return nil
}
