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
	UpdatedAt   time.Time
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
	m.userPrefs[p.UserID] = p
	return nil
}
