package appdb

import (
	"context"
	"fmt"
	"time"
)

func (m *Memory) CreateNetwork(_ context.Context, n Network) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.networks == nil {
		m.networks = map[string]Network{}
	}
	for _, existing := range m.networks {
		if existing.ClusterID == n.ClusterID && existing.Name == n.Name {
			return fmt.Errorf("network name already exists")
		}
	}
	if n.CreatedAt.IsZero() {
		n.CreatedAt = time.Now().UTC()
	}
	n.UpdatedAt = n.CreatedAt
	m.networks[n.ID] = n
	return nil
}

func (m *Memory) ListNetworks(_ context.Context, clusterID string) ([]Network, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Network
	for _, n := range m.networks {
		if n.ClusterID == clusterID {
			out = append(out, n)
		}
	}
	return out, nil
}

func (m *Memory) GetNetwork(_ context.Context, clusterID, id string) (*Network, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	n, ok := m.networks[id]
	if !ok || n.ClusterID != clusterID {
		return nil, nil
	}
	cp := n
	return &cp, nil
}

func (m *Memory) UpdateNetworkObserved(_ context.Context, n Network) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	cur, ok := m.networks[n.ID]
	if !ok {
		return fmt.Errorf("network not found")
	}
	cur.Status = n.Status
	cur.Reason = n.Reason
	cur.Warnings = n.Warnings
	cur.ManagementIfIndex = n.ManagementIfIndex
	cur.UpdatedAt = time.Now().UTC()
	m.networks[n.ID] = cur
	return nil
}

func (m *Memory) CreateAddress(_ context.Context, a Address) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.addresses == nil {
		m.addresses = map[string]Address{}
	}
	if a.CreatedAt.IsZero() {
		a.CreatedAt = time.Now().UTC()
	}
	m.addresses[a.ID] = a
	return nil
}

func (m *Memory) ListAddresses(_ context.Context, clusterID, networkID string) ([]Address, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Address
	for _, a := range m.addresses {
		if a.ClusterID == clusterID && (networkID == "" || a.NetworkID == networkID) {
			out = append(out, a)
		}
	}
	return out, nil
}

func (m *Memory) CreateReservation(_ context.Context, r DHCPReservation) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.reservations == nil {
		m.reservations = map[string]DHCPReservation{}
	}
	for _, existing := range m.reservations {
		if existing.NetworkID == r.NetworkID && (existing.MAC == r.MAC || existing.IPv4 == r.IPv4) {
			return fmt.Errorf("reservation already exists")
		}
	}
	if r.CreatedAt.IsZero() {
		r.CreatedAt = time.Now().UTC()
	}
	m.reservations[r.ID] = r
	return nil
}

func (m *Memory) ListReservations(_ context.Context, clusterID, networkID string) ([]DHCPReservation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []DHCPReservation
	for _, r := range m.reservations {
		if r.ClusterID == clusterID && (networkID == "" || r.NetworkID == networkID) {
			out = append(out, r)
		}
	}
	return out, nil
}
