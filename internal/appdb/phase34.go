package appdb

import (
	"context"
	"fmt"
	"sort"
	"time"
)

const (
	HAModeSingleWriter     = "single-writer"
	HAReplicaNotConfigured = "not_configured"
	HAReplicaUnavailable   = "unavailable"
	HAFencingOperator      = "operator"
	RollingQueued          = "queued"
	RollingRunning         = "running"
	RollingSucceeded       = "succeeded"
	RollingUnavailable     = "unavailable"
	RollingFailed          = "failed"
	RollingActionDrain     = "drain"
	RollingActionUpdate    = "update"
)

// HAState is control-plane failover foundations. This is not multi-master.
type HAState struct {
	ClusterID       string
	Mode            string
	ReplicaStatus   string
	ReplicaEndpoint string
	FencingMode     string
	FencedHolder    string
	FencedAt        *time.Time
	PromotedHolder  string
	PromotedAt      *time.Time
	Reason          string
	UpdatedAt       time.Time
}

// RollingPlan is one cluster-aware package update pass.
type RollingPlan struct {
	ID         string
	ClusterID  string
	Status     string
	Reason     string
	CreatedAt  time.Time
	FinishedAt *time.Time
}

// RollingStep is drain or update of one node.
type RollingStep struct {
	ID                string
	PlanID            string
	ClusterID         string
	NodeID            string
	Ordinal           int
	Action            string
	Status            string
	Reason            string
	UpdateOperationID string
	CreatedAt         time.Time
}

func (m *Memory) FenceLease(_ context.Context, clusterID string, at time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.clusterLease == nil || m.clusterLease.ClusterID != clusterID {
		return nil
	}
	if at.IsZero() {
		at = time.Now().UTC().Add(-time.Second)
	}
	m.clusterLease.ExpiresAt = at
	m.clusterLease.Fenced = true
	return nil
}

func (m *Memory) GetHAState(_ context.Context, clusterID string) (*HAState, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.haState == nil {
		return nil, nil
	}
	h, ok := m.haState[clusterID]
	if !ok {
		return nil, nil
	}
	cp := h
	return &cp, nil
}

func (m *Memory) UpsertHAState(_ context.Context, h HAState) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.haState == nil {
		m.haState = map[string]HAState{}
	}
	if h.Mode == "" {
		h.Mode = HAModeSingleWriter
	}
	if h.ReplicaStatus == "" {
		h.ReplicaStatus = HAReplicaNotConfigured
	}
	if h.FencingMode == "" {
		h.FencingMode = HAFencingOperator
	}
	if h.UpdatedAt.IsZero() {
		h.UpdatedAt = time.Now().UTC()
	}
	m.haState[h.ClusterID] = h
	return nil
}

func (m *Memory) SetHAReplicaDSN(_ context.Context, clusterID, dsn string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.haReplicaDSN == nil {
		m.haReplicaDSN = map[string]string{}
	}
	m.haReplicaDSN[clusterID] = dsn
	return nil
}

func (m *Memory) GetHAReplicaDSN(_ context.Context, clusterID string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.haReplicaDSN == nil {
		return "", nil
	}
	return m.haReplicaDSN[clusterID], nil
}

func (m *Memory) CreateRollingPlan(_ context.Context, p RollingPlan) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.rollingPlans == nil {
		m.rollingPlans = map[string]RollingPlan{}
	}
	if p.CreatedAt.IsZero() {
		p.CreatedAt = time.Now().UTC()
	}
	m.rollingPlans[p.ID] = p
	return nil
}

func (m *Memory) GetRollingPlan(_ context.Context, clusterID, id string) (*RollingPlan, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.rollingPlans[id]
	if !ok || p.ClusterID != clusterID {
		return nil, nil
	}
	cp := p
	return &cp, nil
}

func (m *Memory) LatestRollingPlan(_ context.Context, clusterID string) (*RollingPlan, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var best *RollingPlan
	for _, p := range m.rollingPlans {
		if p.ClusterID != clusterID {
			continue
		}
		cp := p
		if best == nil || cp.CreatedAt.After(best.CreatedAt) {
			best = &cp
		}
	}
	return best, nil
}

func (m *Memory) UpdateRollingPlan(_ context.Context, p RollingPlan) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	cur, ok := m.rollingPlans[p.ID]
	if !ok || cur.ClusterID != p.ClusterID {
		return fmt.Errorf("rolling plan not found")
	}
	m.rollingPlans[p.ID] = p
	return nil
}

func (m *Memory) CreateRollingStep(_ context.Context, s RollingStep) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.rollingSteps == nil {
		m.rollingSteps = map[string]RollingStep{}
	}
	if s.CreatedAt.IsZero() {
		s.CreatedAt = time.Now().UTC()
	}
	m.rollingSteps[s.ID] = s
	return nil
}

func (m *Memory) ListRollingSteps(_ context.Context, clusterID, planID string) ([]RollingStep, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []RollingStep
	for _, s := range m.rollingSteps {
		if s.ClusterID == clusterID && s.PlanID == planID {
			out = append(out, s)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Ordinal < out[j].Ordinal })
	return out, nil
}

func (m *Memory) UpdateRollingStep(_ context.Context, s RollingStep) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	cur, ok := m.rollingSteps[s.ID]
	if !ok || cur.ClusterID != s.ClusterID {
		return fmt.Errorf("rolling step not found")
	}
	m.rollingSteps[s.ID] = s
	return nil
}
