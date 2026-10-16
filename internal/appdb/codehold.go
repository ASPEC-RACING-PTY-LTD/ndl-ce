package appdb

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"sort"
	"time"
)

// CodeholdController is the connection to a CodeHold server, which treats
// No-dal as one of its host controllers.
type CodeholdController struct {
	ClusterID           string
	URL                 string
	RunnerURL           string
	ControllerID        string
	Name                string
	Status              string
	StatusReason        string
	Settings            json.RawMessage
	LastSyncAt          *time.Time
	CredentialExpiresAt *time.Time
	CreatedBy           string
	CreatedAt           time.Time
	UpdatedAt           time.Time
}

// CodeholdRunner is one runner CodeHold asked for and the container serving it.
type CodeholdRunner struct {
	ClusterID   string
	RunnerID    string
	Name        string
	WorkloadID  string
	Desired     string
	State       string
	Error       string
	Busy        int
	Spec        string
	AppliedSpec string
	TokenHash   string
	Limits      json.RawMessage
	Detached    bool
	DeleteAfter *time.Time
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// --- memory ---

func (m *Memory) GetCodeholdController(_ context.Context, clusterID string) (*CodeholdController, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	c, ok := m.chControllers[clusterID]
	if !ok {
		return nil, nil
	}
	return &c, nil
}

func (m *Memory) PutCodeholdController(_ context.Context, c CodeholdController) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.chControllers == nil {
		m.chControllers = map[string]CodeholdController{}
	}
	now := time.Now().UTC()
	if prev, ok := m.chControllers[c.ClusterID]; ok {
		c.CreatedAt = prev.CreatedAt
	} else if c.CreatedAt.IsZero() {
		c.CreatedAt = now
	}
	c.UpdatedAt = now
	m.chControllers[c.ClusterID] = c
	return nil
}

func (m *Memory) DeleteCodeholdController(_ context.Context, clusterID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.chControllers, clusterID)
	for k := range m.chSecrets {
		if len(k) > len(clusterID) && k[:len(clusterID)] == clusterID {
			delete(m.chSecrets, k)
		}
	}
	return nil
}

func (m *Memory) PutCodeholdSecret(_ context.Context, clusterID, name, sealed string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.chSecrets == nil {
		m.chSecrets = map[string]string{}
	}
	if sealed == "" {
		delete(m.chSecrets, clusterID+"/"+name)
		return nil
	}
	m.chSecrets[clusterID+"/"+name] = sealed
	return nil
}

func (m *Memory) GetCodeholdSecret(_ context.Context, clusterID, name string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.chSecrets[clusterID+"/"+name], nil
}

func (m *Memory) ListCodeholdRunners(_ context.Context, clusterID string) ([]CodeholdRunner, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []CodeholdRunner
	for _, r := range m.chRunners {
		if r.ClusterID == clusterID {
			out = append(out, r)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Name < out[j].Name || (out[i].Name == out[j].Name && out[i].RunnerID < out[j].RunnerID)
	})
	return out, nil
}

func (m *Memory) PutCodeholdRunner(_ context.Context, r CodeholdRunner) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.chRunners == nil {
		m.chRunners = map[string]CodeholdRunner{}
	}
	now := time.Now().UTC()
	key := r.ClusterID + "/" + r.RunnerID
	if prev, ok := m.chRunners[key]; ok {
		r.CreatedAt = prev.CreatedAt
	} else if r.CreatedAt.IsZero() {
		r.CreatedAt = now
	}
	r.UpdatedAt = now
	m.chRunners[key] = r
	return nil
}

func (m *Memory) DeleteCodeholdRunner(_ context.Context, clusterID, runnerID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.chRunners, clusterID+"/"+runnerID)
	return nil
}

// --- postgres ---

const chControllerCols = `cluster_id::text, url, runner_url, controller_id, name, status, status_reason, settings, last_sync_at,
credential_expires_at, created_by, created_at, updated_at`

func (p *Postgres) GetCodeholdController(ctx context.Context, clusterID string) (*CodeholdController, error) {
	var c CodeholdController
	var last, exp sql.NullTime
	var settings []byte
	err := p.DB.QueryRowContext(ctx, `SELECT `+chControllerCols+` FROM codehold_controllers WHERE cluster_id=$1`, clusterID).Scan(
		&c.ClusterID, &c.URL, &c.RunnerURL, &c.ControllerID, &c.Name, &c.Status, &c.StatusReason, &settings, &last, &exp, &c.CreatedBy, &c.CreatedAt, &c.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	c.Settings = settings
	if last.Valid {
		t := last.Time.UTC()
		c.LastSyncAt = &t
	}
	if exp.Valid {
		t := exp.Time.UTC()
		c.CredentialExpiresAt = &t
	}
	return &c, nil
}

func (p *Postgres) PutCodeholdController(ctx context.Context, c CodeholdController) error {
	settings := c.Settings
	if len(settings) == 0 {
		settings = json.RawMessage(`{}`)
	}
	_, err := p.DB.ExecContext(ctx, `
INSERT INTO codehold_controllers (cluster_id, url, runner_url, controller_id, name, status, status_reason, settings, last_sync_at,
	credential_expires_at, created_by, created_at, updated_at)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,now(),now())
ON CONFLICT (cluster_id) DO UPDATE SET url=EXCLUDED.url, runner_url=EXCLUDED.runner_url, controller_id=EXCLUDED.controller_id,
	name=EXCLUDED.name, status=EXCLUDED.status, status_reason=EXCLUDED.status_reason, settings=EXCLUDED.settings,
	last_sync_at=EXCLUDED.last_sync_at, credential_expires_at=EXCLUDED.credential_expires_at, updated_at=now()`,
		c.ClusterID, c.URL, c.RunnerURL, c.ControllerID, c.Name, c.Status, c.StatusReason, string(settings), c.LastSyncAt, c.CredentialExpiresAt, c.CreatedBy)
	return err
}

func (p *Postgres) DeleteCodeholdController(ctx context.Context, clusterID string) error {
	if _, err := p.DB.ExecContext(ctx, `DELETE FROM secrets.codehold_secrets WHERE cluster_id=$1`, clusterID); err != nil {
		return err
	}
	_, err := p.DB.ExecContext(ctx, `DELETE FROM codehold_controllers WHERE cluster_id=$1`, clusterID)
	return err
}

func (p *Postgres) PutCodeholdSecret(ctx context.Context, clusterID, name, sealed string) error {
	if sealed == "" {
		_, err := p.DB.ExecContext(ctx, `DELETE FROM secrets.codehold_secrets WHERE cluster_id=$1 AND name=$2`, clusterID, name)
		return err
	}
	_, err := p.DB.ExecContext(ctx, `
INSERT INTO secrets.codehold_secrets (cluster_id, name, sealed, updated_at) VALUES ($1,$2,$3,now())
ON CONFLICT (cluster_id, name) DO UPDATE SET sealed=EXCLUDED.sealed, updated_at=now()`, clusterID, name, sealed)
	return err
}

func (p *Postgres) GetCodeholdSecret(ctx context.Context, clusterID, name string) (string, error) {
	var sealed string
	err := p.DB.QueryRowContext(ctx, `SELECT sealed FROM secrets.codehold_secrets WHERE cluster_id=$1 AND name=$2`, clusterID, name).Scan(&sealed)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return sealed, err
}

const chRunnerCols = `cluster_id::text, runner_id, name, workload_id, desired, state, error, busy, spec, applied_spec, token_hash, limits,
detached, delete_after, created_at, updated_at`

func (p *Postgres) ListCodeholdRunners(ctx context.Context, clusterID string) ([]CodeholdRunner, error) {
	rows, err := p.DB.QueryContext(ctx, `SELECT `+chRunnerCols+` FROM codehold_runners WHERE cluster_id=$1 ORDER BY name, runner_id`, clusterID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []CodeholdRunner
	for rows.Next() {
		var r CodeholdRunner
		var limits []byte
		var del sql.NullTime
		if err := rows.Scan(&r.ClusterID, &r.RunnerID, &r.Name, &r.WorkloadID, &r.Desired, &r.State, &r.Error, &r.Busy, &r.Spec, &r.AppliedSpec,
			&r.TokenHash, &limits, &r.Detached, &del, &r.CreatedAt, &r.UpdatedAt); err != nil {
			return nil, err
		}
		r.Limits = limits
		if del.Valid {
			t := del.Time.UTC()
			r.DeleteAfter = &t
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (p *Postgres) PutCodeholdRunner(ctx context.Context, r CodeholdRunner) error {
	limits := r.Limits
	if len(limits) == 0 {
		limits = json.RawMessage(`{}`)
	}
	_, err := p.DB.ExecContext(ctx, `
INSERT INTO codehold_runners (cluster_id, runner_id, name, workload_id, desired, state, error, busy, spec, applied_spec, token_hash, limits,
	detached, delete_after, created_at, updated_at)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,now(),now())
ON CONFLICT (cluster_id, runner_id) DO UPDATE SET name=EXCLUDED.name, workload_id=EXCLUDED.workload_id, desired=EXCLUDED.desired,
	state=EXCLUDED.state, error=EXCLUDED.error, busy=EXCLUDED.busy, spec=EXCLUDED.spec, applied_spec=EXCLUDED.applied_spec,
	token_hash=EXCLUDED.token_hash, limits=EXCLUDED.limits, detached=EXCLUDED.detached, delete_after=EXCLUDED.delete_after, updated_at=now()`,
		r.ClusterID, r.RunnerID, r.Name, r.WorkloadID, r.Desired, r.State, r.Error, r.Busy, r.Spec, r.AppliedSpec, r.TokenHash, string(limits),
		r.Detached, r.DeleteAfter)
	return err
}

func (p *Postgres) DeleteCodeholdRunner(ctx context.Context, clusterID, runnerID string) error {
	_, err := p.DB.ExecContext(ctx, `DELETE FROM codehold_runners WHERE cluster_id=$1 AND runner_id=$2`, clusterID, runnerID)
	return err
}
