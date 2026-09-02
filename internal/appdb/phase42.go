package appdb

import (
	"context"
	"sort"
	"time"
)

const (
	PlanPreview   = "preview"
	PlanApproved  = "approved"
	PlanExecuting = "executing"
	PlanSucceeded = "succeeded"
	PlanFailed    = "failed"
	PlanStopped   = "stopped"
	PlanDenied    = "denied"
)

// AIPlan is a reviewable list of existing API calls. It is not a shell.
type AIPlan struct {
	ID        string
	ClusterID string
	ProfileID string
	Prompt    string
	Status    string
	ActorType string
	Reason    string
	CreatedBy string
	CreatedAt time.Time
}

// AIPlanStep is one existing API. Permission is checked at approve time.
type AIPlanStep struct {
	ID          string
	ClusterID   string
	PlanID      string
	Ordinal     int
	Action      string
	Permission  string
	Method      string
	Path        string
	Title       string
	BodyJSON    string
	Status      string
	Reason      string
	OperationID string
}

func (m *Memory) CreateAIPlan(_ context.Context, p AIPlan, steps []AIPlanStep) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.aiPlans == nil {
		m.aiPlans = map[string]AIPlan{}
		m.aiPlanSteps = map[string]AIPlanStep{}
	}
	if p.CreatedAt.IsZero() {
		p.CreatedAt = time.Now().UTC()
	}
	m.aiPlans[p.ID] = p
	for _, st := range steps {
		m.aiPlanSteps[st.ID] = st
	}
	return nil
}

func (m *Memory) GetAIPlan(_ context.Context, clusterID, id string) (*AIPlan, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.aiPlans[id]
	if !ok || p.ClusterID != clusterID {
		return nil, nil
	}
	cp := p
	return &cp, nil
}

func (m *Memory) ListAIPlans(_ context.Context, clusterID string, limit int) ([]AIPlan, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []AIPlan
	for _, p := range m.aiPlans {
		if p.ClusterID == clusterID {
			out = append(out, p)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (m *Memory) UpdateAIPlan(_ context.Context, p AIPlan) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.aiPlans[p.ID] = p
	return nil
}

func (m *Memory) ListAIPlanSteps(_ context.Context, clusterID, planID string) ([]AIPlanStep, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []AIPlanStep
	for _, st := range m.aiPlanSteps {
		if st.ClusterID == clusterID && st.PlanID == planID {
			out = append(out, st)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Ordinal < out[j].Ordinal })
	return out, nil
}

func (m *Memory) UpdateAIPlanStep(_ context.Context, st AIPlanStep) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.aiPlanSteps[st.ID] = st
	return nil
}
