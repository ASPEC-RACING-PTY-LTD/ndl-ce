package appdb

import (
	"context"
	"fmt"
	"sort"
	"time"
)

const (
	StoreClassCommunity = "community"
	StoreClassVerified  = "verified"
	StoreClassOfficial  = "official"
	StoreInstallQueued  = "queued"
	StoreInstallRunning = "running"
	StoreInstallOK      = "installed"
	StoreInstallFailed  = "failed"
	StoreInstallRolled  = "rolled_back"
)

// StorePackage is one Store catalog row. Manifest YAML is the source of truth.
type StorePackage struct {
	ID              string
	ClusterID       string
	Name            string
	Version         string
	Class           string
	Title           string
	Summary         string
	ManifestYAML    string
	UnsignedWarning bool
	CreatedAt       time.Time
}

// StoreInstallation is one install job mapped to stack/workload create.
type StoreInstallation struct {
	ID         string
	ClusterID  string
	PackageID  string
	Status     string
	Reason     string
	StackID    string
	WorkloadID string
	NodeID     string
	Warning    string
	CreatedAt  time.Time
	FinishedAt *time.Time
}

func (m *Memory) UpsertStorePackage(_ context.Context, p StorePackage) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.storePackages == nil {
		m.storePackages = map[string]StorePackage{}
	}
	if p.CreatedAt.IsZero() {
		p.CreatedAt = time.Now().UTC()
	}
	m.storePackages[p.ID] = p
	return nil
}

func (m *Memory) ListStorePackages(_ context.Context, clusterID string) ([]StorePackage, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []StorePackage
	for _, p := range m.storePackages {
		if p.ClusterID == clusterID {
			out = append(out, p)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Name == out[j].Name {
			return out[i].Version < out[j].Version
		}
		return out[i].Name < out[j].Name
	})
	return out, nil
}

func (m *Memory) GetStorePackage(_ context.Context, clusterID, id string) (*StorePackage, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.storePackages[id]
	if !ok || p.ClusterID != clusterID {
		return nil, nil
	}
	cp := p
	return &cp, nil
}

func (m *Memory) GetStorePackageByName(_ context.Context, clusterID, name, version string) (*StorePackage, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, p := range m.storePackages {
		if p.ClusterID == clusterID && p.Name == name && (version == "" || p.Version == version) {
			cp := p
			return &cp, nil
		}
	}
	return nil, nil
}

func (m *Memory) CreateStoreInstallation(_ context.Context, in StoreInstallation) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.storeInstalls == nil {
		m.storeInstalls = map[string]StoreInstallation{}
	}
	if in.CreatedAt.IsZero() {
		in.CreatedAt = time.Now().UTC()
	}
	if _, ok := m.storeInstalls[in.ID]; ok {
		return fmt.Errorf("installation already exists")
	}
	m.storeInstalls[in.ID] = in
	return nil
}

func (m *Memory) GetStoreInstallation(_ context.Context, clusterID, id string) (*StoreInstallation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	in, ok := m.storeInstalls[id]
	if !ok || in.ClusterID != clusterID {
		return nil, nil
	}
	cp := in
	return &cp, nil
}

func (m *Memory) ListStoreInstallations(_ context.Context, clusterID string) ([]StoreInstallation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []StoreInstallation
	for _, in := range m.storeInstalls {
		if in.ClusterID == clusterID {
			out = append(out, in)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out, nil
}

func (m *Memory) UpdateStoreInstallation(_ context.Context, in StoreInstallation) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	cur, ok := m.storeInstalls[in.ID]
	if !ok || cur.ClusterID != in.ClusterID {
		return fmt.Errorf("installation not found")
	}
	m.storeInstalls[in.ID] = in
	return nil
}
