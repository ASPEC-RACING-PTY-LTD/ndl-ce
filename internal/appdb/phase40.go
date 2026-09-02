package appdb

import (
	"context"
	"fmt"
	"sort"
	"time"
)

const (
	PolicyPending   = "pending"
	PolicySucceeded = "succeeded"
	PolicySkipped   = "skipped"
	PolicyDenied    = "denied"
	PolicyFailed    = "failed"
)

// Policy is a deterministic automation rule. It is not an LLM loop.
type Policy struct {
	ID               string
	ClusterID        string
	Name             string
	Kind             string
	Action           string
	ThresholdPercent int
	RequireApproval  bool
	Enabled          bool
	SpecYAML         string
	CreatedAt        time.Time
}

// PolicyRun is one evaluation. ActorID is the service identity.
type PolicyRun struct {
	ID           string
	ClusterID    string
	PolicyID     string
	ActorID      string
	Status       string
	Reason       string
	OperationIDs []string
	CreatedAt    time.Time
}

func (m *Memory) CreatePolicy(_ context.Context, p Policy) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.policies == nil {
		m.policies = map[string]Policy{}
	}
	for _, existing := range m.policies {
		if existing.ClusterID == p.ClusterID && existing.Name == p.Name {
			return fmt.Errorf("policy name already exists")
		}
	}
	if p.CreatedAt.IsZero() {
		p.CreatedAt = time.Now().UTC()
	}
	m.policies[p.ID] = p
	return nil
}

func (m *Memory) ListPolicies(_ context.Context, clusterID string) ([]Policy, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Policy
	for _, p := range m.policies {
		if p.ClusterID == clusterID {
			out = append(out, p)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out, nil
}

func (m *Memory) GetPolicy(_ context.Context, clusterID, id string) (*Policy, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.policies[id]
	if !ok || p.ClusterID != clusterID {
		return nil, nil
	}
	cp := p
	return &cp, nil
}

func (m *Memory) CreatePolicyRun(_ context.Context, r PolicyRun) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.policyRuns == nil {
		m.policyRuns = map[string]PolicyRun{}
	}
	if r.CreatedAt.IsZero() {
		r.CreatedAt = time.Now().UTC()
	}
	m.policyRuns[r.ID] = r
	return nil
}

func (m *Memory) ListPolicyRuns(_ context.Context, clusterID string, limit int) ([]PolicyRun, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []PolicyRun
	for _, r := range m.policyRuns {
		if r.ClusterID == clusterID {
			out = append(out, r)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}
