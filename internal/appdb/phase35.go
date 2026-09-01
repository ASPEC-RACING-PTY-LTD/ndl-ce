package appdb

import (
	"context"
	"fmt"
	"sort"
	"time"
)

const (
	FeatureNotConfigured = "not_configured"
	FeatureUnavailable   = "unavailable"
	FeatureInstalled     = "installed"
	FeatureRemoved       = "removed"
	FeatureNotStarted    = "not_started"
)

// Feature is one optional or core module row.
type Feature struct {
	ClusterID     string
	ID            string
	Enabled       bool
	PackageStatus string
	RuntimeStatus string
	Reason        string
	UpdatedAt     time.Time
}

func (m *Memory) ListFeatures(_ context.Context, clusterID string) ([]Feature, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Feature
	for _, f := range m.features {
		if f.ClusterID == clusterID {
			out = append(out, f)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func (m *Memory) GetFeature(_ context.Context, clusterID, id string) (*Feature, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	f, ok := m.features[clusterID+"/"+id]
	if !ok {
		return nil, nil
	}
	cp := f
	return &cp, nil
}

func (m *Memory) UpsertFeature(_ context.Context, f Feature) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if f.ClusterID == "" || f.ID == "" {
		return fmt.Errorf("feature identity is required")
	}
	if m.features == nil {
		m.features = map[string]Feature{}
	}
	if f.PackageStatus == "" {
		f.PackageStatus = FeatureNotConfigured
	}
	if f.RuntimeStatus == "" {
		f.RuntimeStatus = FeatureNotStarted
	}
	if f.UpdatedAt.IsZero() {
		f.UpdatedAt = time.Now().UTC()
	}
	m.features[f.ClusterID+"/"+f.ID] = f
	return nil
}
