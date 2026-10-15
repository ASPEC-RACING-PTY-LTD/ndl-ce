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

// SavedScript is a named JSON script kept for reuse (Docker tasks, Uptime imports).
type SavedScript struct {
	ID        string
	ClusterID string
	Kind      string
	Name      string
	Body      string
	CreatedBy string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// UptimeTarget is a container an Uptime monitor restarts, by machine and name.
type UptimeTarget struct {
	MachineID string `json:"machine_id"`
	Name      string `json:"name"`
}

// UptimeMonitor checks a URL or host:port and recovers the containers behind it.
type UptimeMonitor struct {
	ID            string
	ClusterID     string
	Name          string
	Target        string
	ExpectStatus  string
	Keyword       string
	Containers    []UptimeTarget
	AutoRestart   bool
	Enabled       bool
	State         string
	LastCheckAt   *time.Time
	LastOKAt      *time.Time
	LastChangeAt  *time.Time
	LastStatus    int
	LastLatencyMS int
	LastError     string
	Failures      int
	LastRestartAt *time.Time
	NextCheckAt   *time.Time
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// UptimeEvent is one check result or recovery action.
type UptimeEvent struct {
	MonitorID  string
	At         time.Time
	OK         bool
	StatusCode int
	LatencyMS  int
	Error      string
	Action     string
}

// uptimeEventKeep is how many events each monitor keeps.
const uptimeEventKeep = 200

// --- memory ---

func (m *Memory) ListSavedScripts(_ context.Context, clusterID, kind string) ([]SavedScript, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []SavedScript
	for _, s := range m.savedScripts {
		if s.ClusterID == clusterID && (kind == "" || s.Kind == kind) {
			out = append(out, s)
		}
	}
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name) })
	return out, nil
}

func (m *Memory) GetSavedScript(_ context.Context, clusterID, id string) (*SavedScript, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.savedScripts[id]
	if !ok || s.ClusterID != clusterID {
		return nil, nil
	}
	return &s, nil
}

func (m *Memory) PutSavedScript(_ context.Context, s SavedScript) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.savedScripts == nil {
		m.savedScripts = map[string]SavedScript{}
	}
	for id, cur := range m.savedScripts {
		if id != s.ID && cur.ClusterID == s.ClusterID && cur.Kind == s.Kind && cur.Name == s.Name {
			return fmt.Errorf("a saved script with that name already exists")
		}
	}
	now := time.Now().UTC()
	if cur, ok := m.savedScripts[s.ID]; ok {
		s.CreatedAt, s.CreatedBy = cur.CreatedAt, cur.CreatedBy
	} else {
		s.CreatedAt = now
	}
	s.UpdatedAt = now
	m.savedScripts[s.ID] = s
	return nil
}

func (m *Memory) DeleteSavedScript(_ context.Context, clusterID, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if s, ok := m.savedScripts[id]; ok && s.ClusterID == clusterID {
		delete(m.savedScripts, id)
	}
	return nil
}

func (m *Memory) ListUptimeMonitors(_ context.Context, clusterID string) ([]UptimeMonitor, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []UptimeMonitor
	for _, u := range m.uptimeMonitors {
		if clusterID == "" || u.ClusterID == clusterID {
			out = append(out, u)
		}
	}
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name) })
	return out, nil
}

func (m *Memory) GetUptimeMonitor(_ context.Context, clusterID, id string) (*UptimeMonitor, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	u, ok := m.uptimeMonitors[id]
	if !ok || u.ClusterID != clusterID {
		return nil, nil
	}
	return &u, nil
}

func (m *Memory) PutUptimeMonitor(_ context.Context, u UptimeMonitor) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.uptimeMonitors == nil {
		m.uptimeMonitors = map[string]UptimeMonitor{}
	}
	for id, cur := range m.uptimeMonitors {
		if id != u.ID && cur.ClusterID == u.ClusterID && cur.Name == u.Name {
			return fmt.Errorf("a monitor named %q already exists", u.Name)
		}
	}
	now := time.Now().UTC()
	if cur, ok := m.uptimeMonitors[u.ID]; ok {
		u.CreatedAt = cur.CreatedAt
	} else if u.CreatedAt.IsZero() {
		u.CreatedAt = now
	}
	u.UpdatedAt = now
	m.uptimeMonitors[u.ID] = u
	return nil
}

func (m *Memory) DeleteUptimeMonitor(_ context.Context, clusterID, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if u, ok := m.uptimeMonitors[id]; ok && u.ClusterID == clusterID {
		delete(m.uptimeMonitors, id)
		kept := m.uptimeEvents[:0]
		for _, e := range m.uptimeEvents {
			if e.MonitorID != id {
				kept = append(kept, e)
			}
		}
		m.uptimeEvents = kept
	}
	return nil
}

func (m *Memory) AddUptimeEvent(_ context.Context, e UptimeEvent) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if e.At.IsZero() {
		e.At = time.Now().UTC()
	}
	m.uptimeEvents = append(m.uptimeEvents, e)
	n := 0
	for i := len(m.uptimeEvents) - 1; i >= 0; i-- {
		if m.uptimeEvents[i].MonitorID == e.MonitorID {
			n++
			if n > uptimeEventKeep {
				m.uptimeEvents = append(m.uptimeEvents[:i], m.uptimeEvents[i+1:]...)
			}
		}
	}
	return nil
}

func (m *Memory) ListUptimeEvents(_ context.Context, monitorID string, limit int) ([]UptimeEvent, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []UptimeEvent
	for i := len(m.uptimeEvents) - 1; i >= 0 && (limit <= 0 || len(out) < limit); i-- {
		if m.uptimeEvents[i].MonitorID == monitorID {
			out = append(out, m.uptimeEvents[i])
		}
	}
	return out, nil
}

// --- postgres ---

func (p *Postgres) ListSavedScripts(ctx context.Context, clusterID, kind string) ([]SavedScript, error) {
	rows, err := p.DB.QueryContext(ctx, `
SELECT id::text, cluster_id::text, kind, name, body, created_by, created_at, updated_at
FROM saved_scripts WHERE cluster_id=$1 AND ($2='' OR kind=$2) ORDER BY lower(name)`, clusterID, kind)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SavedScript
	for rows.Next() {
		var s SavedScript
		if err := rows.Scan(&s.ID, &s.ClusterID, &s.Kind, &s.Name, &s.Body, &s.CreatedBy, &s.CreatedAt, &s.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func (p *Postgres) GetSavedScript(ctx context.Context, clusterID, id string) (*SavedScript, error) {
	var s SavedScript
	err := p.DB.QueryRowContext(ctx, `
SELECT id::text, cluster_id::text, kind, name, body, created_by, created_at, updated_at
FROM saved_scripts WHERE cluster_id=$1 AND id=$2`, clusterID, id).
		Scan(&s.ID, &s.ClusterID, &s.Kind, &s.Name, &s.Body, &s.CreatedBy, &s.CreatedAt, &s.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &s, nil
}

func (p *Postgres) PutSavedScript(ctx context.Context, s SavedScript) error {
	_, err := p.DB.ExecContext(ctx, `
INSERT INTO saved_scripts (id, cluster_id, kind, name, body, created_by) VALUES ($1,$2,$3,$4,$5,$6)
ON CONFLICT (id) DO UPDATE SET name=EXCLUDED.name, body=EXCLUDED.body, updated_at=now()`,
		s.ID, s.ClusterID, s.Kind, s.Name, s.Body, s.CreatedBy)
	if err != nil && strings.Contains(err.Error(), "saved_scripts_cluster_id_kind_name_key") {
		return fmt.Errorf("a saved script with that name already exists")
	}
	return err
}

func (p *Postgres) DeleteSavedScript(ctx context.Context, clusterID, id string) error {
	_, err := p.DB.ExecContext(ctx, `DELETE FROM saved_scripts WHERE cluster_id=$1 AND id=$2`, clusterID, id)
	return err
}

const uptimeColumns = `id::text, cluster_id::text, name, target, expect_status, keyword, containers, auto_restart, enabled,
state, last_check_at, last_ok_at, last_change_at, last_status, last_latency_ms, last_error, failures,
last_restart_at, next_check_at, created_at, updated_at`

func scanUptime(s interface{ Scan(...any) error }) (UptimeMonitor, error) {
	var u UptimeMonitor
	var containers []byte
	var lc, lo, lch, lr, nc sql.NullTime
	err := s.Scan(&u.ID, &u.ClusterID, &u.Name, &u.Target, &u.ExpectStatus, &u.Keyword, &containers, &u.AutoRestart, &u.Enabled,
		&u.State, &lc, &lo, &lch, &u.LastStatus, &u.LastLatencyMS, &u.LastError, &u.Failures, &lr, &nc, &u.CreatedAt, &u.UpdatedAt)
	if err != nil {
		return u, err
	}
	_ = json.Unmarshal(containers, &u.Containers)
	pt := func(n sql.NullTime) *time.Time {
		if !n.Valid {
			return nil
		}
		t := n.Time
		return &t
	}
	u.LastCheckAt, u.LastOKAt, u.LastChangeAt, u.LastRestartAt, u.NextCheckAt = pt(lc), pt(lo), pt(lch), pt(lr), pt(nc)
	return u, nil
}

func (p *Postgres) ListUptimeMonitors(ctx context.Context, clusterID string) ([]UptimeMonitor, error) {
	rows, err := p.DB.QueryContext(ctx, `SELECT `+uptimeColumns+` FROM uptime_monitors WHERE ($1='' OR cluster_id::text=$1) ORDER BY lower(name)`, clusterID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []UptimeMonitor
	for rows.Next() {
		u, err := scanUptime(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

func (p *Postgres) GetUptimeMonitor(ctx context.Context, clusterID, id string) (*UptimeMonitor, error) {
	u, err := scanUptime(p.DB.QueryRowContext(ctx, `SELECT `+uptimeColumns+` FROM uptime_monitors WHERE cluster_id=$1 AND id=$2`, clusterID, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &u, nil
}

func uptimeNullTime(t *time.Time) any {
	if t == nil {
		return nil
	}
	return *t
}

func (p *Postgres) PutUptimeMonitor(ctx context.Context, u UptimeMonitor) error {
	containers, _ := json.Marshal(u.Containers)
	if u.Containers == nil {
		containers = []byte("[]")
	}
	_, err := p.DB.ExecContext(ctx, `
INSERT INTO uptime_monitors (id, cluster_id, name, target, expect_status, keyword, containers, auto_restart, enabled,
  state, last_check_at, last_ok_at, last_change_at, last_status, last_latency_ms, last_error, failures, last_restart_at, next_check_at)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19)
ON CONFLICT (id) DO UPDATE SET name=EXCLUDED.name, target=EXCLUDED.target, expect_status=EXCLUDED.expect_status,
  keyword=EXCLUDED.keyword, containers=EXCLUDED.containers, auto_restart=EXCLUDED.auto_restart, enabled=EXCLUDED.enabled,
  state=EXCLUDED.state, last_check_at=EXCLUDED.last_check_at, last_ok_at=EXCLUDED.last_ok_at,
  last_change_at=EXCLUDED.last_change_at, last_status=EXCLUDED.last_status, last_latency_ms=EXCLUDED.last_latency_ms,
  last_error=EXCLUDED.last_error, failures=EXCLUDED.failures, last_restart_at=EXCLUDED.last_restart_at,
  next_check_at=EXCLUDED.next_check_at, updated_at=now()`,
		u.ID, u.ClusterID, u.Name, u.Target, u.ExpectStatus, u.Keyword, containers, u.AutoRestart, u.Enabled,
		u.State, uptimeNullTime(u.LastCheckAt), uptimeNullTime(u.LastOKAt), uptimeNullTime(u.LastChangeAt), u.LastStatus, u.LastLatencyMS,
		u.LastError, u.Failures, uptimeNullTime(u.LastRestartAt), uptimeNullTime(u.NextCheckAt))
	if err != nil && strings.Contains(err.Error(), "uptime_monitors_cluster_id_name_key") {
		return fmt.Errorf("a monitor named %q already exists", u.Name)
	}
	return err
}

func (p *Postgres) DeleteUptimeMonitor(ctx context.Context, clusterID, id string) error {
	_, err := p.DB.ExecContext(ctx, `DELETE FROM uptime_monitors WHERE cluster_id=$1 AND id=$2`, clusterID, id)
	return err
}

func (p *Postgres) AddUptimeEvent(ctx context.Context, e UptimeEvent) error {
	if _, err := p.DB.ExecContext(ctx, `
INSERT INTO uptime_events (monitor_id, ok, status_code, latency_ms, error, action) VALUES ($1,$2,$3,$4,$5,$6)`,
		e.MonitorID, e.OK, e.StatusCode, e.LatencyMS, e.Error, e.Action); err != nil {
		return err
	}
	_, err := p.DB.ExecContext(ctx, `
DELETE FROM uptime_events WHERE monitor_id=$1 AND id NOT IN (
  SELECT id FROM uptime_events WHERE monitor_id=$1 ORDER BY at DESC, id DESC LIMIT $2)`, e.MonitorID, uptimeEventKeep)
	return err
}

func (p *Postgres) ListUptimeEvents(ctx context.Context, monitorID string, limit int) ([]UptimeEvent, error) {
	if limit <= 0 {
		limit = uptimeEventKeep
	}
	rows, err := p.DB.QueryContext(ctx, `
SELECT monitor_id::text, at, ok, status_code, latency_ms, error, action FROM uptime_events
WHERE monitor_id=$1 ORDER BY at DESC, id DESC LIMIT $2`, monitorID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []UptimeEvent
	for rows.Next() {
		var e UptimeEvent
		if err := rows.Scan(&e.MonitorID, &e.At, &e.OK, &e.StatusCode, &e.LatencyMS, &e.Error, &e.Action); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
