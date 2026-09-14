package appdb

import (
	"context"
	"time"
)

// GuestObservation is qemu-ga plus nodal_ga state for one VM.
type GuestObservation struct {
	WorkloadID     string
	ClusterID      string
	QEMUGAState    string
	NodalGAState   string
	NodalGAVersion string
	GuestOS        string
	GuestArch      string
	GuestIPv4      string
	ObservedAt     time.Time
	Stale          bool
}

func (m *Memory) UpsertGuestObservation(_ context.Context, g GuestObservation) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.guestObs == nil {
		m.guestObs = map[string]GuestObservation{}
	}
	if g.ObservedAt.IsZero() {
		g.ObservedAt = time.Now().UTC()
	}
	m.guestObs[g.WorkloadID] = g
	return nil
}

func (m *Memory) GetGuestObservation(_ context.Context, clusterID, workloadID string) (*GuestObservation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	g, ok := m.guestObs[workloadID]
	if !ok || g.ClusterID != clusterID {
		return nil, nil
	}
	cp := g
	return &cp, nil
}
