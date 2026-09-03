package appdb

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"
)

const (
	UpdateRunning     = "running"
	UpdateSucceeded   = "succeeded"
	UpdateFailed      = "failed"
	UpdateUnsupported = "unsupported"
)

// UpdateOperation is one control-plane package update action.
type UpdateOperation struct {
	ID         string
	ClusterID  string
	Action     string
	Status     string
	DryRun     bool
	Error      string
	Version    string
	Packages   []string
	StartedAt  time.Time
	FinishedAt *time.Time
}

func (m *Memory) CreateUpdateOperation(_ context.Context, op UpdateOperation) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.updateOps == nil {
		m.updateOps = map[string]UpdateOperation{}
	}
	if op.StartedAt.IsZero() {
		op.StartedAt = time.Now().UTC()
	}
	m.updateOps[op.ID] = op
	return nil
}

func (m *Memory) ListUpdateOperations(_ context.Context, clusterID string, limit int) ([]UpdateOperation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []UpdateOperation
	for _, op := range m.updateOps {
		if op.ClusterID == clusterID {
			out = append(out, op)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].StartedAt.After(out[j].StartedAt)
	})
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (m *Memory) GetLatestUpdateOperation(_ context.Context, clusterID string) (*UpdateOperation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var latest *UpdateOperation
	for _, op := range m.updateOps {
		if op.ClusterID != clusterID {
			continue
		}
		cp := op
		if latest == nil || cp.StartedAt.After(latest.StartedAt) {
			latest = &cp
		}
	}
	return latest, nil
}

func (m *Memory) GetLatestCheckUpdateOperation(_ context.Context, clusterID string) (*UpdateOperation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var latest *UpdateOperation
	for _, op := range m.updateOps {
		if op.ClusterID != clusterID || op.Action != "check" || strings.TrimSpace(op.Version) == "" {
			continue
		}
		cp := op
		if latest == nil || cp.StartedAt.After(latest.StartedAt) || (cp.StartedAt.Equal(latest.StartedAt) && cp.ID < latest.ID) {
			latest = &cp
		}
	}
	return latest, nil
}

func (m *Memory) UpdateUpdateOperation(_ context.Context, op UpdateOperation) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	cur, ok := m.updateOps[op.ID]
	if !ok || cur.ClusterID != op.ClusterID {
		return fmt.Errorf("update operation not found")
	}
	m.updateOps[op.ID] = op
	return nil
}
