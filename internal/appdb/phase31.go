package appdb

import (
	"context"
	"fmt"
	"time"
)

// NodeGroup is a CE placement group.
type NodeGroup struct {
	ID        string
	ClusterID string
	Name      string
	CreatedAt time.Time
}

// NodeMaintenance marks a node as draining. Migrate execution is Phase 32.
type NodeMaintenance struct {
	NodeID    string
	ClusterID string
	Since     time.Time
	Reason    string
}

// WorkloadPlacement is the requested scheduler policy for a workload.
type WorkloadPlacement struct {
	WorkloadID             string
	ClusterID              string
	Mode                   string
	NodeGroupID            string
	RequireGPU             bool
	RequireStorageClass    string
	AffinityWorkloadID     string
	AntiAffinityWorkloadID string
	Priority               int
}

func (m *Memory) CreateNodeGroup(_ context.Context, g NodeGroup) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.nodeGroups == nil {
		m.nodeGroups = map[string]NodeGroup{}
	}
	for _, existing := range m.nodeGroups {
		if existing.ClusterID == g.ClusterID && existing.Name == g.Name {
			return fmt.Errorf("node group name already exists")
		}
	}
	if g.CreatedAt.IsZero() {
		g.CreatedAt = time.Now().UTC()
	}
	m.nodeGroups[g.ID] = g
	return nil
}

func (m *Memory) ListNodeGroups(_ context.Context, clusterID string) ([]NodeGroup, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []NodeGroup
	for _, g := range m.nodeGroups {
		if g.ClusterID == clusterID {
			out = append(out, g)
		}
	}
	return out, nil
}

func (m *Memory) GetNodeGroup(_ context.Context, clusterID, id string) (*NodeGroup, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	g, ok := m.nodeGroups[id]
	if !ok || g.ClusterID != clusterID {
		return nil, nil
	}
	cp := g
	return &cp, nil
}

func (m *Memory) AddNodeGroupMember(_ context.Context, clusterID, groupID, nodeID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	g, ok := m.nodeGroups[groupID]
	if !ok || g.ClusterID != clusterID {
		return fmt.Errorf("node group not found")
	}
	n, ok := m.nodes[nodeID]
	if !ok || n.ClusterID != clusterID {
		return fmt.Errorf("node not found")
	}
	if m.nodeGroupMembers == nil {
		m.nodeGroupMembers = map[string][]string{}
	}
	for _, id := range m.nodeGroupMembers[groupID] {
		if id == nodeID {
			return nil
		}
	}
	m.nodeGroupMembers[groupID] = append(m.nodeGroupMembers[groupID], nodeID)
	return nil
}

func (m *Memory) ListNodeGroupMembers(_ context.Context, clusterID, groupID string) ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	g, ok := m.nodeGroups[groupID]
	if !ok || g.ClusterID != clusterID {
		return nil, nil
	}
	out := append([]string{}, m.nodeGroupMembers[groupID]...)
	return out, nil
}

func (m *Memory) SetNodeMaintenance(_ context.Context, row NodeMaintenance) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.nodeMaint == nil {
		m.nodeMaint = map[string]NodeMaintenance{}
	}
	if row.Since.IsZero() {
		row.Since = time.Now().UTC()
	}
	m.nodeMaint[row.NodeID] = row
	return nil
}

func (m *Memory) ClearNodeMaintenance(_ context.Context, clusterID, nodeID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	cur, ok := m.nodeMaint[nodeID]
	if !ok || cur.ClusterID != clusterID {
		return nil
	}
	delete(m.nodeMaint, nodeID)
	return nil
}

func (m *Memory) GetNodeMaintenance(_ context.Context, clusterID, nodeID string) (*NodeMaintenance, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	row, ok := m.nodeMaint[nodeID]
	if !ok || row.ClusterID != clusterID {
		return nil, nil
	}
	cp := row
	return &cp, nil
}

func (m *Memory) ListNodeMaintenance(_ context.Context, clusterID string) ([]NodeMaintenance, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []NodeMaintenance
	for _, row := range m.nodeMaint {
		if row.ClusterID == clusterID {
			out = append(out, row)
		}
	}
	return out, nil
}

func (m *Memory) UpsertWorkloadPlacement(_ context.Context, p WorkloadPlacement) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.placements == nil {
		m.placements = map[string]WorkloadPlacement{}
	}
	m.placements[p.WorkloadID] = p
	return nil
}

func (m *Memory) GetWorkloadPlacement(_ context.Context, clusterID, workloadID string) (*WorkloadPlacement, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.placements[workloadID]
	if !ok || p.ClusterID != clusterID {
		return nil, nil
	}
	cp := p
	return &cp, nil
}
