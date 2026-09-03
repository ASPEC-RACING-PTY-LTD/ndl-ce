package appdb

import (
	"context"
	"fmt"
	"sort"
	"time"
)

// NetworkVLAN is a stacked VLAN. VID is a locator tag. ID is the UUID.
type NetworkVLAN struct {
	ID           string
	ClusterID    string
	NetworkID    string
	Name         string
	VID          int
	ParentIfName string
	AccessIfName string
	Mode         string
	Locator      string
	Status       string
	Reason       string
	CreatedAt    time.Time
}

// NetworkBond is an active-backup or LACP bond. Member names are locators.
type NetworkBond struct {
	ID        string
	ClusterID string
	Name      string
	Mode      string
	Members   []string
	Locator   string
	Status    string
	Reason    string
	CreatedAt time.Time
}

// NetworkPolicy is guest-to-guest bridge policy. It cannot drop management INPUT.
type NetworkPolicy struct {
	ID            string
	ClusterID     string
	Name          string
	Action        string
	SrcWorkloadID string
	DstWorkloadID string
	SrcMAC        string
	DstMAC        string
	Status        string
	Reason        string
	CreatedAt     time.Time
}

// NetworkOverlay is VXLAN prep. Multi-node mesh is Phase 30.
type NetworkOverlay struct {
	ID        string
	ClusterID string
	Name      string
	VNI       int
	Locator   string
	Status    string
	Reason    string
	CreatedAt time.Time
}

func (m *Memory) CreateNetworkVLAN(_ context.Context, v NetworkVLAN) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.netVLANs == nil {
		m.netVLANs = map[string]NetworkVLAN{}
	}
	if v.CreatedAt.IsZero() {
		v.CreatedAt = time.Now().UTC()
	}
	m.netVLANs[v.ID] = v
	return nil
}

func (m *Memory) ListNetworkVLANs(_ context.Context, clusterID string) ([]NetworkVLAN, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []NetworkVLAN
	for _, v := range m.netVLANs {
		if v.ClusterID == clusterID {
			out = append(out, v)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		return lessCreatedAtID(out[i].CreatedAt, out[j].CreatedAt, out[i].ID, out[j].ID)
	})
	return out, nil
}

func (m *Memory) CreateNetworkBond(_ context.Context, b NetworkBond) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.netBonds == nil {
		m.netBonds = map[string]NetworkBond{}
	}
	if b.CreatedAt.IsZero() {
		b.CreatedAt = time.Now().UTC()
	}
	m.netBonds[b.ID] = b
	return nil
}

func (m *Memory) ListNetworkBonds(_ context.Context, clusterID string) ([]NetworkBond, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []NetworkBond
	for _, b := range m.netBonds {
		if b.ClusterID == clusterID {
			out = append(out, b)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		return lessCreatedAtID(out[i].CreatedAt, out[j].CreatedAt, out[i].ID, out[j].ID)
	})
	return out, nil
}

func (m *Memory) CreateNetworkPolicy(_ context.Context, p NetworkPolicy) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.netPolicies == nil {
		m.netPolicies = map[string]NetworkPolicy{}
	}
	if p.CreatedAt.IsZero() {
		p.CreatedAt = time.Now().UTC()
	}
	m.netPolicies[p.ID] = p
	return nil
}

func (m *Memory) ListNetworkPolicies(_ context.Context, clusterID string) ([]NetworkPolicy, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []NetworkPolicy
	for _, p := range m.netPolicies {
		if p.ClusterID == clusterID {
			out = append(out, p)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		return lessCreatedAtID(out[i].CreatedAt, out[j].CreatedAt, out[i].ID, out[j].ID)
	})
	return out, nil
}

func (m *Memory) GetNetworkPolicy(_ context.Context, clusterID, id string) (*NetworkPolicy, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.netPolicies[id]
	if !ok || p.ClusterID != clusterID {
		return nil, nil
	}
	cp := p
	return &cp, nil
}

func (m *Memory) UpdateNetworkPolicyStatus(_ context.Context, clusterID, id, status, reason string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.netPolicies[id]
	if !ok || p.ClusterID != clusterID {
		return fmt.Errorf("network policy not found")
	}
	p.Status, p.Reason = status, reason
	m.netPolicies[id] = p
	return nil
}

func (m *Memory) CreateNetworkOverlay(_ context.Context, o NetworkOverlay) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.netOverlays == nil {
		m.netOverlays = map[string]NetworkOverlay{}
	}
	if o.CreatedAt.IsZero() {
		o.CreatedAt = time.Now().UTC()
	}
	m.netOverlays[o.ID] = o
	return nil
}

func (m *Memory) ListNetworkOverlays(_ context.Context, clusterID string) ([]NetworkOverlay, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []NetworkOverlay
	for _, o := range m.netOverlays {
		if o.ClusterID == clusterID {
			out = append(out, o)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		return lessCreatedAtID(out[i].CreatedAt, out[j].CreatedAt, out[i].ID, out[j].ID)
	})
	return out, nil
}
