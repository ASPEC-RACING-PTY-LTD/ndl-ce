package appdb

import (
	"context"
	"time"
)

const (
	UXGuided   = "guided"
	UXAdvanced = "advanced"
	UXExpert   = "expert"
)

// UserPrefs are presentation settings. They never grant permissions.
type UserPrefs struct {
	UserID      string
	ClusterID   string
	UXLevel     string
	ExpertAckAt *time.Time
	// WorkloadSort is the saved Workloads page order: "", "name" or "custom".
	WorkloadSort string
	// WorkloadOrder is the user's custom Workloads order, by workload id.
	WorkloadOrder []string
	UpdatedAt     time.Time
}

func (m *Memory) GetUserPrefs(_ context.Context, userID string) (*UserPrefs, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.userPrefs == nil {
		return nil, nil
	}
	p, ok := m.userPrefs[userID]
	if !ok {
		return nil, nil
	}
	cp := p
	cp.WorkloadOrder = append([]string(nil), p.WorkloadOrder...)
	return &cp, nil
}

func (m *Memory) UpsertUserPrefs(_ context.Context, p UserPrefs) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.userPrefs == nil {
		m.userPrefs = map[string]UserPrefs{}
	}
	if p.UpdatedAt.IsZero() {
		p.UpdatedAt = time.Now().UTC()
	}
	p.WorkloadOrder = append([]string(nil), p.WorkloadOrder...)
	m.userPrefs[p.UserID] = p
	return nil
}
