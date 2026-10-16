package appdb

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

// CloudflareCredential is a saved Cloudflare API token, without the token.
// Scope is the JSON list of accounts, zones and permissions it was found to reach.
type CloudflareCredential struct {
	ID             string
	ClusterID      string
	Name           string
	AccountHint    string
	AccountOwned   bool
	TokenLast4     string
	Status         string
	StatusReason   string
	Scope          json.RawMessage
	ExpiresAt      *time.Time
	LastVerifiedAt *time.Time
	CreatedBy      string
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// CloudflareRoute is No-dal's record of a public hostname route: a disabled
// route kept here so it can be switched back on, or the workload it serves.
type CloudflareRoute struct {
	ID         string
	ClusterID  string
	AccountID  string
	TunnelID   string
	Hostname   string
	Path       string
	Rule       json.RawMessage
	Enabled    bool
	WorkloadID string
	OriginIP   string
	UpdatedBy  string
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

// CloudflareWorkloadPref remembers the account and tunnel last used for a workload.
type CloudflareWorkloadPref struct {
	WorkloadID string
	ClusterID  string
	AccountID  string
	TunnelID   string
}

// CloudflareConnector is a cloudflared service No-dal deployed on a machine.
type CloudflareConnector struct {
	ID        string
	ClusterID string
	AccountID string
	TunnelID  string
	MachineID string
	Unit      string
	CreatedBy string
	CreatedAt time.Time
}

// ErrCloudflareNameTaken is returned when a credential name is reused.
var ErrCloudflareNameTaken = errors.New("a Cloudflare credential with that name already exists")

func routeKey(r CloudflareRoute) string {
	return r.ClusterID + "|" + r.TunnelID + "|" + strings.ToLower(r.Hostname) + "|" + r.Path
}

// --- memory ---

func (m *Memory) cfInit() {
	if m.cfCreds == nil {
		m.cfCreds = map[string]CloudflareCredential{}
		m.cfTokens = map[string]string{}
		m.cfRoutes = map[string]CloudflareRoute{}
		m.cfPrefs = map[string]CloudflareWorkloadPref{}
		m.cfConnectors = map[string]CloudflareConnector{}
	}
}

func (m *Memory) ListCloudflareCredentials(_ context.Context, clusterID string) ([]CloudflareCredential, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cfInit()
	var out []CloudflareCredential
	for _, c := range m.cfCreds {
		if clusterID == "" || c.ClusterID == clusterID {
			out = append(out, c)
		}
	}
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name) })
	return out, nil
}

func (m *Memory) GetCloudflareCredential(_ context.Context, clusterID, id string) (*CloudflareCredential, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cfInit()
	c, ok := m.cfCreds[id]
	if !ok || c.ClusterID != clusterID {
		return nil, nil
	}
	return &c, nil
}

func (m *Memory) PutCloudflareCredential(_ context.Context, c CloudflareCredential) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cfInit()
	for id, x := range m.cfCreds {
		if id != c.ID && x.ClusterID == c.ClusterID && strings.EqualFold(x.Name, c.Name) {
			return ErrCloudflareNameTaken
		}
	}
	now := time.Now().UTC()
	if prev, ok := m.cfCreds[c.ID]; ok {
		c.CreatedAt = prev.CreatedAt
	} else if c.CreatedAt.IsZero() {
		c.CreatedAt = now
	}
	c.UpdatedAt = now
	m.cfCreds[c.ID] = c
	return nil
}

func (m *Memory) DeleteCloudflareCredential(_ context.Context, clusterID, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cfInit()
	if c, ok := m.cfCreds[id]; ok && c.ClusterID == clusterID {
		delete(m.cfCreds, id)
		delete(m.cfTokens, id)
	}
	return nil
}

func (m *Memory) PutCloudflareToken(_ context.Context, clusterID, credentialID, sealed string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cfInit()
	if c, ok := m.cfCreds[credentialID]; !ok || c.ClusterID != clusterID {
		return fmt.Errorf("credential not found")
	}
	m.cfTokens[credentialID] = sealed
	return nil
}

func (m *Memory) GetCloudflareToken(_ context.Context, clusterID, credentialID string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cfInit()
	if c, ok := m.cfCreds[credentialID]; !ok || c.ClusterID != clusterID {
		return "", fmt.Errorf("credential not found")
	}
	return m.cfTokens[credentialID], nil
}

func (m *Memory) ListCloudflareRoutes(_ context.Context, clusterID string) ([]CloudflareRoute, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cfInit()
	var out []CloudflareRoute
	for _, r := range m.cfRoutes {
		if r.ClusterID == clusterID {
			out = append(out, r)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Hostname+out[i].Path < out[j].Hostname+out[j].Path })
	return out, nil
}

func (m *Memory) PutCloudflareRoute(_ context.Context, r CloudflareRoute) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cfInit()
	if r.ID == "" {
		return fmt.Errorf("route id is required")
	}
	r.Hostname = strings.ToLower(r.Hostname)
	now := time.Now().UTC()
	for id, x := range m.cfRoutes {
		if routeKey(x) == routeKey(r) {
			r.ID, r.CreatedAt = id, x.CreatedAt
		}
	}
	if r.CreatedAt.IsZero() {
		r.CreatedAt = now
	}
	r.UpdatedAt = now
	m.cfRoutes[r.ID] = r
	return nil
}

func (m *Memory) DeleteCloudflareRoute(_ context.Context, clusterID, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cfInit()
	if r, ok := m.cfRoutes[id]; ok && r.ClusterID == clusterID {
		delete(m.cfRoutes, id)
	}
	return nil
}

func (m *Memory) GetCloudflareWorkloadPref(_ context.Context, clusterID, workloadID string) (*CloudflareWorkloadPref, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cfInit()
	p, ok := m.cfPrefs[workloadID]
	if !ok || p.ClusterID != clusterID {
		return nil, nil
	}
	return &p, nil
}

func (m *Memory) PutCloudflareWorkloadPref(_ context.Context, p CloudflareWorkloadPref) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cfInit()
	m.cfPrefs[p.WorkloadID] = p
	return nil
}

func (m *Memory) ListCloudflareConnectors(_ context.Context, clusterID string) ([]CloudflareConnector, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cfInit()
	var out []CloudflareConnector
	for _, c := range m.cfConnectors {
		if c.ClusterID == clusterID {
			out = append(out, c)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out, nil
}

func (m *Memory) PutCloudflareConnector(_ context.Context, c CloudflareConnector) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cfInit()
	for id, x := range m.cfConnectors {
		if x.ClusterID == c.ClusterID && x.MachineID == c.MachineID && x.Unit == c.Unit {
			c.ID = id
		}
	}
	if c.CreatedAt.IsZero() {
		c.CreatedAt = time.Now().UTC()
	}
	m.cfConnectors[c.ID] = c
	return nil
}

func (m *Memory) DeleteCloudflareConnector(_ context.Context, clusterID, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cfInit()
	if c, ok := m.cfConnectors[id]; ok && c.ClusterID == clusterID {
		delete(m.cfConnectors, id)
	}
	return nil
}

// --- postgres ---

const cfCredColumns = `id::text, cluster_id::text, name, account_hint, account_owned, token_last4, status, status_reason,
scope, expires_at, last_verified_at, created_by, created_at, updated_at`

func nullTimePtr(n sql.NullTime) *time.Time {
	if !n.Valid {
		return nil
	}
	t := n.Time
	return &t
}

func scanCFCred(s interface{ Scan(...any) error }) (CloudflareCredential, error) {
	var c CloudflareCredential
	var scope []byte
	var exp, ver sql.NullTime
	err := s.Scan(&c.ID, &c.ClusterID, &c.Name, &c.AccountHint, &c.AccountOwned, &c.TokenLast4, &c.Status, &c.StatusReason,
		&scope, &exp, &ver, &c.CreatedBy, &c.CreatedAt, &c.UpdatedAt)
	c.Scope = json.RawMessage(scope)
	c.ExpiresAt, c.LastVerifiedAt = nullTimePtr(exp), nullTimePtr(ver)
	return c, err
}

func (p *Postgres) ListCloudflareCredentials(ctx context.Context, clusterID string) ([]CloudflareCredential, error) {
	rows, err := p.DB.QueryContext(ctx, `SELECT `+cfCredColumns+` FROM cloudflare_credentials WHERE ($1='' OR cluster_id::text=$1) ORDER BY lower(name)`, clusterID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []CloudflareCredential
	for rows.Next() {
		c, err := scanCFCred(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (p *Postgres) GetCloudflareCredential(ctx context.Context, clusterID, id string) (*CloudflareCredential, error) {
	c, err := scanCFCred(p.DB.QueryRowContext(ctx, `SELECT `+cfCredColumns+` FROM cloudflare_credentials WHERE cluster_id=$1 AND id::text=$2`, clusterID, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &c, nil
}

func (p *Postgres) PutCloudflareCredential(ctx context.Context, c CloudflareCredential) error {
	scope := c.Scope
	if len(scope) == 0 {
		scope = json.RawMessage(`[]`)
	}
	_, err := p.DB.ExecContext(ctx, `
INSERT INTO cloudflare_credentials (id, cluster_id, name, account_hint, account_owned, token_last4, status, status_reason, scope,
  expires_at, last_verified_at, created_by)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9::jsonb,$10,$11,$12)
ON CONFLICT (id) DO UPDATE SET name=EXCLUDED.name, account_hint=EXCLUDED.account_hint, account_owned=EXCLUDED.account_owned,
  token_last4=EXCLUDED.token_last4, status=EXCLUDED.status, status_reason=EXCLUDED.status_reason, scope=EXCLUDED.scope,
  expires_at=EXCLUDED.expires_at, last_verified_at=EXCLUDED.last_verified_at, updated_at=now()`,
		c.ID, c.ClusterID, c.Name, c.AccountHint, c.AccountOwned, c.TokenLast4, c.Status, c.StatusReason, string(scope),
		c.ExpiresAt, c.LastVerifiedAt, c.CreatedBy)
	if err != nil && strings.Contains(err.Error(), "cloudflare_credentials_cluster_id_name_key") {
		return ErrCloudflareNameTaken
	}
	return err
}

func (p *Postgres) DeleteCloudflareCredential(ctx context.Context, clusterID, id string) error {
	_, err := p.DB.ExecContext(ctx, `DELETE FROM cloudflare_credentials WHERE cluster_id=$1 AND id::text=$2`, clusterID, id)
	return err
}

func (p *Postgres) PutCloudflareToken(ctx context.Context, clusterID, credentialID, sealed string) error {
	_, err := p.DB.ExecContext(ctx, `
INSERT INTO secrets.cloudflare_tokens (credential_id, cluster_id, sealed, updated_at) VALUES ($1,$2,$3,now())
ON CONFLICT (credential_id) DO UPDATE SET sealed=EXCLUDED.sealed, updated_at=now()`, credentialID, clusterID, sealed)
	return err
}

func (p *Postgres) GetCloudflareToken(ctx context.Context, clusterID, credentialID string) (string, error) {
	var sealed string
	err := p.DB.QueryRowContext(ctx, `SELECT sealed FROM secrets.cloudflare_tokens WHERE cluster_id=$1 AND credential_id::text=$2`, clusterID, credentialID).Scan(&sealed)
	if errors.Is(err, sql.ErrNoRows) {
		return "", fmt.Errorf("credential has no stored token")
	}
	return sealed, err
}

const cfRouteColumns = `id::text, cluster_id::text, account_id, tunnel_id, hostname, path, rule, enabled, COALESCE(workload_id::text,''),
origin_ip, updated_by, created_at, updated_at`

func (p *Postgres) ListCloudflareRoutes(ctx context.Context, clusterID string) ([]CloudflareRoute, error) {
	rows, err := p.DB.QueryContext(ctx, `SELECT `+cfRouteColumns+` FROM cloudflare_routes WHERE cluster_id=$1 ORDER BY hostname, path`, clusterID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []CloudflareRoute
	for rows.Next() {
		var r CloudflareRoute
		var rule []byte
		if err := rows.Scan(&r.ID, &r.ClusterID, &r.AccountID, &r.TunnelID, &r.Hostname, &r.Path, &rule, &r.Enabled, &r.WorkloadID,
			&r.OriginIP, &r.UpdatedBy, &r.CreatedAt, &r.UpdatedAt); err != nil {
			return nil, err
		}
		r.Rule = json.RawMessage(rule)
		out = append(out, r)
	}
	return out, rows.Err()
}

func (p *Postgres) PutCloudflareRoute(ctx context.Context, r CloudflareRoute) error {
	rule := r.Rule
	if len(rule) == 0 {
		rule = json.RawMessage(`{}`)
	}
	var wl any
	if r.WorkloadID != "" {
		wl = r.WorkloadID
	}
	_, err := p.DB.ExecContext(ctx, `
INSERT INTO cloudflare_routes (id, cluster_id, account_id, tunnel_id, hostname, path, rule, enabled, workload_id, origin_ip, updated_by)
VALUES ($1,$2,$3,$4,lower($5),$6,$7::jsonb,$8,$9,$10,$11)
ON CONFLICT (cluster_id, tunnel_id, hostname, path) DO UPDATE SET account_id=EXCLUDED.account_id, rule=EXCLUDED.rule,
  enabled=EXCLUDED.enabled, workload_id=EXCLUDED.workload_id, origin_ip=EXCLUDED.origin_ip, updated_by=EXCLUDED.updated_by, updated_at=now()`,
		r.ID, r.ClusterID, r.AccountID, r.TunnelID, r.Hostname, r.Path, string(rule), r.Enabled, wl, r.OriginIP, r.UpdatedBy)
	return err
}

func (p *Postgres) DeleteCloudflareRoute(ctx context.Context, clusterID, id string) error {
	_, err := p.DB.ExecContext(ctx, `DELETE FROM cloudflare_routes WHERE cluster_id=$1 AND id::text=$2`, clusterID, id)
	return err
}

func (p *Postgres) GetCloudflareWorkloadPref(ctx context.Context, clusterID, workloadID string) (*CloudflareWorkloadPref, error) {
	pref := CloudflareWorkloadPref{WorkloadID: workloadID, ClusterID: clusterID}
	err := p.DB.QueryRowContext(ctx, `SELECT account_id, tunnel_id FROM cloudflare_workload_prefs WHERE cluster_id=$1 AND workload_id::text=$2`,
		clusterID, workloadID).Scan(&pref.AccountID, &pref.TunnelID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &pref, nil
}

func (p *Postgres) PutCloudflareWorkloadPref(ctx context.Context, pref CloudflareWorkloadPref) error {
	_, err := p.DB.ExecContext(ctx, `
INSERT INTO cloudflare_workload_prefs (workload_id, cluster_id, account_id, tunnel_id) VALUES ($1,$2,$3,$4)
ON CONFLICT (workload_id) DO UPDATE SET account_id=EXCLUDED.account_id, tunnel_id=EXCLUDED.tunnel_id, updated_at=now()`,
		pref.WorkloadID, pref.ClusterID, pref.AccountID, pref.TunnelID)
	return err
}

func (p *Postgres) ListCloudflareConnectors(ctx context.Context, clusterID string) ([]CloudflareConnector, error) {
	rows, err := p.DB.QueryContext(ctx, `SELECT id::text, cluster_id::text, account_id, tunnel_id, machine_id, unit, created_by, created_at
FROM cloudflare_connectors WHERE cluster_id=$1 ORDER BY created_at`, clusterID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []CloudflareConnector
	for rows.Next() {
		var c CloudflareConnector
		if err := rows.Scan(&c.ID, &c.ClusterID, &c.AccountID, &c.TunnelID, &c.MachineID, &c.Unit, &c.CreatedBy, &c.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (p *Postgres) PutCloudflareConnector(ctx context.Context, c CloudflareConnector) error {
	_, err := p.DB.ExecContext(ctx, `
INSERT INTO cloudflare_connectors (id, cluster_id, account_id, tunnel_id, machine_id, unit, created_by) VALUES ($1,$2,$3,$4,$5,$6,$7)
ON CONFLICT (cluster_id, machine_id, unit) DO UPDATE SET account_id=EXCLUDED.account_id, tunnel_id=EXCLUDED.tunnel_id`,
		c.ID, c.ClusterID, c.AccountID, c.TunnelID, c.MachineID, c.Unit, c.CreatedBy)
	return err
}

func (p *Postgres) DeleteCloudflareConnector(ctx context.Context, clusterID, id string) error {
	_, err := p.DB.ExecContext(ctx, `DELETE FROM cloudflare_connectors WHERE cluster_id=$1 AND id::text=$2`, clusterID, id)
	return err
}
