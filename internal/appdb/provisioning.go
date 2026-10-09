package appdb

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"sort"
	"time"
)

// ProvisionedService links a service in an outside hosting platform (its
// external id) to the No-dal resource that runs it.
type ProvisionedService struct {
	ClusterID    string
	ExternalID   string
	Kind         string // "game" or "container"
	ResourceID   string
	Template     string // game template id or container image pin
	DesiredPower string // "running" or "stopped"
	Suspended    bool
	Labels       map[string]string
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

func provKey(clusterID, externalID string) string { return clusterID + "\x00" + externalID }

func (m *Memory) GetProvisionedService(_ context.Context, clusterID, externalID string) (*ProvisionedService, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if v, ok := m.provisioned[provKey(clusterID, externalID)]; ok {
		return &v, nil
	}
	return nil, nil
}

func (m *Memory) UpsertProvisionedService(_ context.Context, ps ProvisionedService) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.provisioned == nil {
		m.provisioned = map[string]ProvisionedService{}
	}
	now := time.Now().UTC()
	if cur, ok := m.provisioned[provKey(ps.ClusterID, ps.ExternalID)]; ok {
		ps.CreatedAt = cur.CreatedAt
	} else if ps.CreatedAt.IsZero() {
		ps.CreatedAt = now
	}
	ps.UpdatedAt = now
	m.provisioned[provKey(ps.ClusterID, ps.ExternalID)] = ps
	return nil
}

func (m *Memory) DeleteProvisionedService(_ context.Context, clusterID, externalID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.provisioned, provKey(clusterID, externalID))
	return nil
}

func (m *Memory) ListProvisionedServices(_ context.Context, clusterID string) ([]ProvisionedService, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []ProvisionedService
	for _, v := range m.provisioned {
		if v.ClusterID == clusterID {
			out = append(out, v)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out, nil
}

const provColumns = `cluster_id::text, external_id, kind, resource_id, template, desired_power, suspended, labels, created_at, updated_at`

func scanProvisioned(row rowScanner) (ProvisionedService, error) {
	var ps ProvisionedService
	var labels []byte
	if err := row.Scan(&ps.ClusterID, &ps.ExternalID, &ps.Kind, &ps.ResourceID, &ps.Template, &ps.DesiredPower, &ps.Suspended, &labels, &ps.CreatedAt, &ps.UpdatedAt); err != nil {
		return ps, err
	}
	_ = json.Unmarshal(labels, &ps.Labels)
	return ps, nil
}

func (p *Postgres) GetProvisionedService(ctx context.Context, clusterID, externalID string) (*ProvisionedService, error) {
	ps, err := scanProvisioned(p.DB.QueryRowContext(ctx, `SELECT `+provColumns+` FROM provisioning_services WHERE cluster_id=$1 AND external_id=$2`, clusterID, externalID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &ps, nil
}

func (p *Postgres) UpsertProvisionedService(ctx context.Context, ps ProvisionedService) error {
	labels, _ := json.Marshal(ps.Labels)
	if ps.Labels == nil {
		labels = []byte(`{}`)
	}
	_, err := p.DB.ExecContext(ctx, `
INSERT INTO provisioning_services (cluster_id, external_id, kind, resource_id, template, desired_power, suspended, labels)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
ON CONFLICT (cluster_id, external_id) DO UPDATE SET
  kind=EXCLUDED.kind, resource_id=EXCLUDED.resource_id, template=EXCLUDED.template,
  desired_power=EXCLUDED.desired_power, suspended=EXCLUDED.suspended, labels=EXCLUDED.labels, updated_at=now()`,
		ps.ClusterID, ps.ExternalID, ps.Kind, ps.ResourceID, ps.Template, ps.DesiredPower, ps.Suspended, labels)
	return err
}

func (p *Postgres) DeleteProvisionedService(ctx context.Context, clusterID, externalID string) error {
	_, err := p.DB.ExecContext(ctx, `DELETE FROM provisioning_services WHERE cluster_id=$1 AND external_id=$2`, clusterID, externalID)
	return err
}

func (p *Postgres) ListProvisionedServices(ctx context.Context, clusterID string) ([]ProvisionedService, error) {
	rows, err := p.DB.QueryContext(ctx, `SELECT `+provColumns+` FROM provisioning_services WHERE cluster_id=$1 ORDER BY created_at`, clusterID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ProvisionedService
	for rows.Next() {
		ps, err := scanProvisioned(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, ps)
	}
	return out, rows.Err()
}
