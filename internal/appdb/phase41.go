package appdb

import (
	"context"
	"fmt"
	"sort"
	"time"
)

const (
	AIModeAsk = "ask"
)

// AIProvider is a BYO model endpoint. The API key is not on this row.
type AIProvider struct {
	ID        string
	ClusterID string
	Name      string
	Kind      string
	Endpoint  string
	Model     string
	Enabled   bool
	CreatedAt time.Time
}

// AIProfile is a permission profile. Ask is read-only.
type AIProfile struct {
	ID         string
	ClusterID  string
	Name       string
	ProviderID string
	Mode       string
	Grants     []string
	CreatedAt  time.Time
}

func (m *Memory) CreateAIProvider(_ context.Context, p AIProvider, apiKey string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.aiProviders == nil {
		m.aiProviders = map[string]AIProvider{}
		m.aiProviderKeys = map[string]string{}
	}
	for _, existing := range m.aiProviders {
		if existing.ClusterID == p.ClusterID && existing.Name == p.Name {
			return fmt.Errorf("provider name already exists")
		}
	}
	if p.CreatedAt.IsZero() {
		p.CreatedAt = time.Now().UTC()
	}
	m.aiProviders[p.ID] = p
	m.aiProviderKeys[p.ID] = apiKey
	return nil
}

func (m *Memory) ListAIProviders(_ context.Context, clusterID string) ([]AIProvider, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []AIProvider
	for _, p := range m.aiProviders {
		if p.ClusterID == clusterID {
			out = append(out, p)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out, nil
}

func (m *Memory) GetAIProvider(_ context.Context, clusterID, id string) (*AIProvider, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.aiProviders[id]
	if !ok || p.ClusterID != clusterID {
		return nil, nil
	}
	cp := p
	return &cp, nil
}

func (m *Memory) AIProviderKey(_ context.Context, clusterID, id string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.aiProviders[id]
	if !ok || p.ClusterID != clusterID {
		return "", nil
	}
	return m.aiProviderKeys[id], nil
}

func (m *Memory) CreateAIProfile(_ context.Context, p AIProfile) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.aiProfiles == nil {
		m.aiProfiles = map[string]AIProfile{}
	}
	for _, existing := range m.aiProfiles {
		if existing.ClusterID == p.ClusterID && existing.Name == p.Name {
			return fmt.Errorf("profile name already exists")
		}
	}
	if p.CreatedAt.IsZero() {
		p.CreatedAt = time.Now().UTC()
	}
	if p.Grants == nil {
		p.Grants = []string{}
	}
	m.aiProfiles[p.ID] = p
	return nil
}

func (m *Memory) ListAIProfiles(_ context.Context, clusterID string) ([]AIProfile, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []AIProfile
	for _, p := range m.aiProfiles {
		if p.ClusterID == clusterID {
			out = append(out, p)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out, nil
}

func (m *Memory) GetAIProfile(_ context.Context, clusterID, id string) (*AIProfile, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.aiProfiles[id]
	if !ok || p.ClusterID != clusterID {
		return nil, nil
	}
	cp := p
	return &cp, nil
}
