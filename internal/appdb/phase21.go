package appdb

import (
	"context"
	"sort"
	"time"
)

const (
	RegistryConfigured    = "configured"
	RegistryNotConfigured = "not_configured"
)

// Registry is a private OCI registry endpoint. Passwords are never stored on this row.
type Registry struct {
	ID             string
	ClusterID      string
	Name           string
	URL            string
	Insecure       bool
	HasCredentials bool
	Status         string
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

func (m *Memory) CreateRegistry(_ context.Context, r Registry, username, password string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.registries == nil {
		m.registries = map[string]Registry{}
	}
	if m.registrySecrets == nil {
		m.registrySecrets = map[string][2]string{}
	}
	if r.CreatedAt.IsZero() {
		r.CreatedAt = time.Now().UTC()
	}
	r.UpdatedAt = r.CreatedAt
	r.HasCredentials = username != "" || password != ""
	if r.Status == "" {
		r.Status = RegistryConfigured
	}
	m.registries[r.ID] = r
	m.registrySecrets[r.ID] = [2]string{username, password}
	return nil
}

func (m *Memory) ListRegistries(_ context.Context, clusterID string) ([]Registry, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Registry
	for _, r := range m.registries {
		if r.ClusterID == clusterID {
			out = append(out, r)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out, nil
}

func (m *Memory) GetRegistry(_ context.Context, clusterID, id string) (*Registry, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.registries[id]
	if !ok || r.ClusterID != clusterID {
		return nil, nil
	}
	cp := r
	return &cp, nil
}

func (m *Memory) RegistrySecrets(_ context.Context, clusterID, id string) (username, password string, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.registries[id]
	if !ok || r.ClusterID != clusterID {
		return "", "", nil
	}
	sec := m.registrySecrets[id]
	return sec[0], sec[1], nil
}
