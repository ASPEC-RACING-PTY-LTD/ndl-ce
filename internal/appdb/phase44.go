package appdb

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"time"
)

// MigrationSource is a stored external connection. Credentials are not in this row.
type MigrationSource struct {
	ID        string
	ClusterID string
	Adapter   string
	Label     string
	Endpoint  string
	Insecure  bool
	CreatedAt time.Time
	UpdatedAt time.Time
}

// MigrationJob is an import or export transfer. It never records source-delete actions.
type MigrationJob struct {
	ID              string
	ClusterID       string
	SourceID        string
	OperationID     string
	Adapter         string
	Direction       string
	State           string
	Stage           string
	PlanJSON        json.RawMessage
	StatusJSON      json.RawMessage
	CancelRequested bool
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

type migCred struct {
	Token    string
	Username string
	Extra    []byte
}

func (m *Memory) ensureMig() {
	if m.migSources == nil {
		m.migSources = map[string]MigrationSource{}
		m.migSourceCreds = map[string]migCred{}
		m.migJobs = map[string]MigrationJob{}
	}
}

func (m *Memory) CreateMigrationSource(_ context.Context, src MigrationSource, token, username string, extra []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ensureMig()
	if src.UpdatedAt.IsZero() {
		src.UpdatedAt = time.Now().UTC()
	}
	if src.CreatedAt.IsZero() {
		src.CreatedAt = src.UpdatedAt
	}
	m.migSources[src.ID] = src
	m.migSourceCreds[src.ID] = migCred{Token: token, Username: username, Extra: extra}
	return nil
}

func (m *Memory) ListMigrationSources(_ context.Context, clusterID string) ([]MigrationSource, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ensureMig()
	var out []MigrationSource
	for _, s := range m.migSources {
		if s.ClusterID == clusterID {
			out = append(out, s)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		return lessCreatedAtID(out[i].CreatedAt, out[j].CreatedAt, out[i].ID, out[j].ID)
	})
	return out, nil
}

func (m *Memory) GetMigrationSource(_ context.Context, clusterID, id string) (*MigrationSource, string, string, []byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ensureMig()
	s, ok := m.migSources[id]
	if !ok || s.ClusterID != clusterID {
		return nil, "", "", nil, nil
	}
	cp := s
	c := m.migSourceCreds[id]
	return &cp, c.Token, c.Username, c.Extra, nil
}

func (m *Memory) DeleteMigrationSource(_ context.Context, clusterID, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ensureMig()
	s, ok := m.migSources[id]
	if !ok || s.ClusterID != clusterID {
		return fmt.Errorf("migration source not found")
	}
	delete(m.migSources, id)
	delete(m.migSourceCreds, id)
	return nil
}

func (m *Memory) CreateMigrationJob(_ context.Context, j MigrationJob) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ensureMig()
	if j.CreatedAt.IsZero() {
		j.CreatedAt = time.Now().UTC()
	}
	j.UpdatedAt = time.Now().UTC()
	m.migJobs[j.ID] = j
	return nil
}

func (m *Memory) ListMigrationJobs(_ context.Context, clusterID string, limit int) ([]MigrationJob, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ensureMig()
	if limit <= 0 {
		limit = 50
	}
	var out []MigrationJob
	for _, j := range m.migJobs {
		if j.ClusterID == clusterID {
			out = append(out, j)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].CreatedAt.After(out[j].CreatedAt)
		}
		return out[i].ID < out[j].ID
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (m *Memory) GetMigrationJob(_ context.Context, clusterID, id string) (*MigrationJob, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ensureMig()
	j, ok := m.migJobs[id]
	if !ok || j.ClusterID != clusterID {
		return nil, nil
	}
	cp := j
	return &cp, nil
}

func (m *Memory) UpdateMigrationJob(_ context.Context, j MigrationJob) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ensureMig()
	cur, ok := m.migJobs[j.ID]
	if !ok || cur.ClusterID != j.ClusterID {
		return fmt.Errorf("migration job not found")
	}
	j.UpdatedAt = time.Now().UTC()
	m.migJobs[j.ID] = j
	return nil
}
