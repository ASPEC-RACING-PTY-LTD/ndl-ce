package appdb

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"time"
)

const (
	StackStatusDraft    = "draft"
	StackStatusApplying = "applying"
	StackStatusApplied  = "applied"
	StackStatusPartial  = "partial"
	StackStatusFailed   = "failed"
)

const (
	MemberStatusPending     = "pending"
	MemberStatusCreating    = "creating"
	MemberStatusReady       = "ready"
	MemberStatusCollecting  = "collecting"
	MemberStatusUnavailable = "unavailable"
	MemberStatusFailed      = "failed"
)

// Stack is a desired multi-container application. Compose text is archival, not runtime SoT.
type Stack struct {
	ID            string
	ClusterID     string
	Name          string
	Status        string
	DesiredJSON   json.RawMessage
	SourceCompose string
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// StackMember is one imported service mapped to an OCI workload (after apply).
type StackMember struct {
	ID          string
	ClusterID   string
	StackID     string
	ServiceName string
	WorkloadID  string
	DesiredJSON json.RawMessage
	Status      string
	SortOrder   int
	Reason      string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

func (m *Memory) CreateStack(_ context.Context, s Stack) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.stacks == nil {
		m.stacks = map[string]Stack{}
	}
	for _, existing := range m.stacks {
		if existing.ClusterID == s.ClusterID && existing.Name == s.Name {
			return fmt.Errorf("stack name already exists")
		}
	}
	if s.CreatedAt.IsZero() {
		s.CreatedAt = time.Now().UTC()
	}
	s.UpdatedAt = s.CreatedAt
	if s.Status == "" {
		s.Status = StackStatusDraft
	}
	if len(s.DesiredJSON) == 0 {
		s.DesiredJSON = json.RawMessage(`{}`)
	}
	m.stacks[s.ID] = s
	return nil
}

func (m *Memory) ListStacks(_ context.Context, clusterID string) ([]Stack, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Stack
	for _, s := range m.stacks {
		if s.ClusterID == clusterID {
			out = append(out, s)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out, nil
}

func (m *Memory) GetStack(_ context.Context, clusterID, id string) (*Stack, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.stacks[id]
	if !ok || s.ClusterID != clusterID {
		return nil, nil
	}
	cp := s
	return &cp, nil
}

func (m *Memory) UpdateStack(_ context.Context, s Stack) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	cur, ok := m.stacks[s.ID]
	if !ok || cur.ClusterID != s.ClusterID {
		return fmt.Errorf("stack not found")
	}
	if s.Name != "" && s.Name != cur.Name {
		for _, other := range m.stacks {
			if other.ClusterID == s.ClusterID && other.Name == s.Name && other.ID != s.ID {
				return fmt.Errorf("stack name already exists")
			}
		}
		cur.Name = s.Name
	}
	if s.Status != "" {
		cur.Status = s.Status
	}
	if len(s.DesiredJSON) > 0 {
		cur.DesiredJSON = s.DesiredJSON
	}
	if s.SourceCompose != "" {
		cur.SourceCompose = s.SourceCompose
	}
	cur.UpdatedAt = time.Now().UTC()
	m.stacks[s.ID] = cur
	return nil
}

func (m *Memory) DeleteStack(_ context.Context, clusterID, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.stacks[id]
	if !ok || s.ClusterID != clusterID {
		return fmt.Errorf("stack not found")
	}
	delete(m.stacks, id)
	for mid, mem := range m.stackMembers {
		if mem.StackID == id {
			delete(m.stackMembers, mid)
		}
	}
	return nil
}

func (m *Memory) CreateStackMember(_ context.Context, mem StackMember) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.stackMembers == nil {
		m.stackMembers = map[string]StackMember{}
	}
	s, ok := m.stacks[mem.StackID]
	if !ok || s.ClusterID != mem.ClusterID {
		return fmt.Errorf("stack not found")
	}
	for _, existing := range m.stackMembers {
		if existing.StackID == mem.StackID && existing.ServiceName == mem.ServiceName {
			return fmt.Errorf("stack member already exists")
		}
	}
	if mem.CreatedAt.IsZero() {
		mem.CreatedAt = time.Now().UTC()
	}
	mem.UpdatedAt = mem.CreatedAt
	if mem.Status == "" {
		mem.Status = MemberStatusPending
	}
	if len(mem.DesiredJSON) == 0 {
		mem.DesiredJSON = json.RawMessage(`{}`)
	}
	m.stackMembers[mem.ID] = mem
	return nil
}

func (m *Memory) ListStackMembers(_ context.Context, clusterID, stackID string) ([]StackMember, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []StackMember
	for _, mem := range m.stackMembers {
		if mem.ClusterID == clusterID && mem.StackID == stackID {
			out = append(out, mem)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].SortOrder != out[j].SortOrder {
			return out[i].SortOrder < out[j].SortOrder
		}
		return out[i].ServiceName < out[j].ServiceName
	})
	return out, nil
}

func (m *Memory) GetStackMember(_ context.Context, clusterID, id string) (*StackMember, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	mem, ok := m.stackMembers[id]
	if !ok || mem.ClusterID != clusterID {
		return nil, nil
	}
	cp := mem
	return &cp, nil
}

func (m *Memory) GetStackMemberByService(_ context.Context, clusterID, stackID, service string) (*StackMember, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, mem := range m.stackMembers {
		if mem.ClusterID == clusterID && mem.StackID == stackID && mem.ServiceName == service {
			cp := mem
			return &cp, nil
		}
	}
	return nil, nil
}

func (m *Memory) UpdateStackMember(_ context.Context, mem StackMember) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	cur, ok := m.stackMembers[mem.ID]
	if !ok || cur.ClusterID != mem.ClusterID {
		return fmt.Errorf("stack member not found")
	}
	if mem.WorkloadID != "" {
		cur.WorkloadID = mem.WorkloadID
	}
	if len(mem.DesiredJSON) > 0 {
		cur.DesiredJSON = mem.DesiredJSON
	}
	if mem.Status != "" {
		cur.Status = mem.Status
	}
	cur.Reason = mem.Reason
	cur.UpdatedAt = time.Now().UTC()
	m.stackMembers[mem.ID] = cur
	return nil
}
