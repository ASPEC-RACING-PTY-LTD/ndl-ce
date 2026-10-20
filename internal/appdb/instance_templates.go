package appdb

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"sync"
	"time"
)

// InstanceTemplate is a saved set of create-form settings for one instance
// type (system containers, OCI containers, game servers). VM templates are
// VMTemplate, which also keeps a disk snapshot.
type InstanceTemplate struct {
	ID               string
	ClusterID        string
	Kind             string
	Name             string
	Description      string
	Spec             json.RawMessage
	SourceWorkloadID string
	CreatedBy        string
	CreatedAt        time.Time
}

type tmplMem struct {
	mu    sync.Mutex
	items map[string]InstanceTemplate
}

func sortTemplates(out []InstanceTemplate) {
	sort.Slice(out, func(i, j int) bool {
		if out[i].Kind != out[j].Kind {
			return out[i].Kind < out[j].Kind
		}
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
}

func (m *Memory) ListInstanceTemplates(_ context.Context, clusterID string) ([]InstanceTemplate, error) {
	m.tmpl.mu.Lock()
	defer m.tmpl.mu.Unlock()
	out := []InstanceTemplate{}
	for _, t := range m.tmpl.items {
		if t.ClusterID == clusterID {
			out = append(out, t)
		}
	}
	sortTemplates(out)
	return out, nil
}

func (m *Memory) GetInstanceTemplate(_ context.Context, clusterID, id string) (*InstanceTemplate, error) {
	m.tmpl.mu.Lock()
	defer m.tmpl.mu.Unlock()
	t, ok := m.tmpl.items[id]
	if !ok || t.ClusterID != clusterID {
		return nil, nil
	}
	return &t, nil
}

func (m *Memory) CreateInstanceTemplate(_ context.Context, t InstanceTemplate) error {
	m.tmpl.mu.Lock()
	defer m.tmpl.mu.Unlock()
	if m.tmpl.items == nil {
		m.tmpl.items = map[string]InstanceTemplate{}
	}
	if t.CreatedAt.IsZero() {
		t.CreatedAt = time.Now().UTC()
	}
	m.tmpl.items[t.ID] = t
	return nil
}

func (m *Memory) DeleteInstanceTemplate(_ context.Context, clusterID, id string) (bool, error) {
	m.tmpl.mu.Lock()
	defer m.tmpl.mu.Unlock()
	t, ok := m.tmpl.items[id]
	if !ok || t.ClusterID != clusterID {
		return false, nil
	}
	delete(m.tmpl.items, id)
	return true, nil
}

const tmplCols = `id, cluster_id, kind, name, description, spec::text, source_workload_id, created_by, created_at`

func scanTemplate(row interface{ Scan(...any) error }) (InstanceTemplate, error) {
	var t InstanceTemplate
	var spec []byte
	err := row.Scan(&t.ID, &t.ClusterID, &t.Kind, &t.Name, &t.Description, &spec, &t.SourceWorkloadID, &t.CreatedBy, &t.CreatedAt)
	t.Spec = spec
	return t, err
}

func (p *Postgres) ListInstanceTemplates(ctx context.Context, clusterID string) ([]InstanceTemplate, error) {
	out := []InstanceTemplate{}
	if !ValidUUID(clusterID) {
		return out, nil
	}
	rows, err := p.DB.QueryContext(ctx, `SELECT `+tmplCols+` FROM instance_templates WHERE cluster_id=$1 ORDER BY kind, lower(name)`, clusterID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		t, err := scanTemplate(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (p *Postgres) GetInstanceTemplate(ctx context.Context, clusterID, id string) (*InstanceTemplate, error) {
	if !ValidUUID(clusterID) || !ValidUUID(id) {
		return nil, nil
	}
	t, err := scanTemplate(p.DB.QueryRowContext(ctx, `SELECT `+tmplCols+` FROM instance_templates WHERE cluster_id=$1 AND id=$2`, clusterID, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &t, nil
}

func (p *Postgres) CreateInstanceTemplate(ctx context.Context, t InstanceTemplate) error {
	spec := []byte(t.Spec)
	if len(spec) == 0 {
		spec = []byte(`{}`)
	}
	_, err := p.DB.ExecContext(ctx, `INSERT INTO instance_templates (id, cluster_id, kind, name, description, spec, source_workload_id, created_by)
		VALUES ($1,$2,$3,$4,$5,$6::jsonb,$7,$8)`, t.ID, t.ClusterID, t.Kind, t.Name, t.Description, string(spec), t.SourceWorkloadID, t.CreatedBy)
	return err
}

func (p *Postgres) DeleteInstanceTemplate(ctx context.Context, clusterID, id string) (bool, error) {
	if !ValidUUID(clusterID) || !ValidUUID(id) {
		return false, nil
	}
	res, err := p.DB.ExecContext(ctx, `DELETE FROM instance_templates WHERE cluster_id=$1 AND id=$2`, clusterID, id)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}
