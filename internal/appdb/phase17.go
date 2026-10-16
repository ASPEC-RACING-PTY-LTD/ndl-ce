package appdb

import (
	"context"
	"encoding/json"
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
	// UI holds interface preferences by key, such as the theme, sidebar
	// order and table columns.
	UI        map[string]json.RawMessage
	UpdatedAt time.Time
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
	cp.UI = copyUI(p.UI)
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
	// UpsertUserPrefs never changes interface preferences; MergeUserUIPrefs does.
	p.UI = copyUI(m.userPrefs[p.UserID].UI)
	m.userPrefs[p.UserID] = p
	return nil
}

func (m *Memory) MergeUserUIPrefs(_ context.Context, userID, clusterID string, set map[string]json.RawMessage, remove []string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.userPrefs == nil {
		m.userPrefs = map[string]UserPrefs{}
	}
	p, ok := m.userPrefs[userID]
	if !ok {
		p = UserPrefs{UserID: userID, ClusterID: clusterID, UXLevel: UXGuided}
	}
	ui := copyUI(p.UI)
	if ui == nil {
		ui = map[string]json.RawMessage{}
	}
	for k, v := range set {
		ui[k] = append(json.RawMessage(nil), v...)
	}
	for _, k := range remove {
		delete(ui, k)
	}
	p.UI = ui
	p.UpdatedAt = time.Now().UTC()
	m.userPrefs[userID] = p
	return nil
}

func copyUI(in map[string]json.RawMessage) map[string]json.RawMessage {
	if in == nil {
		return nil
	}
	out := make(map[string]json.RawMessage, len(in))
	for k, v := range in {
		out[k] = append(json.RawMessage(nil), v...)
	}
	return out
}
