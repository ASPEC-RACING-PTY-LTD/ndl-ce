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

// Deployment states. queued and waiting have not started; the rest are
// what the node reported.
const (
	DeployQueued      = "queued"
	DeployWaiting     = "waiting"
	DeployStarting    = "starting"
	DeployRunning     = "running"
	DeploySucceeded   = "succeeded"
	DeployFailed      = "failed"
	DeployCancelled   = "cancelled"
	DeployTimedOut    = "timed-out"
	DeployInterrupted = "interrupted"
)

// DeployActive reports whether a deployment has not finished.
func DeployActive(status string) bool {
	switch status {
	case DeployQueued, DeployWaiting, DeployStarting, DeployRunning:
		return true
	}
	return false
}

// DeployCommand is one line of a project's update commands.
type DeployCommand struct {
	Run             string `json:"run"`
	ContinueOnError bool   `json:"continue_on_error,omitempty"`
}

// DeployProjectConfig is what an Owner sets for a project.
type DeployProjectConfig struct {
	Commands   []DeployCommand `json:"commands"`
	RunAs      string          `json:"run_as,omitempty"`
	TimeoutSec int             `json:"timeout_sec,omitempty"`
	// Provider is auto, codehold, github or git.
	Provider string `json:"provider,omitempty"`
	Remote   string `json:"remote,omitempty"`
	Branch   string `json:"branch,omitempty"`
	// FallbackRemote is used when the provider's remote cannot be reached,
	// for example a GitHub mirror of a CodeHold repository.
	FallbackRemote    string `json:"fallback_remote,omitempty"`
	Detect            bool   `json:"detect"`
	DetectIntervalSec int    `json:"detect_interval_sec,omitempty"`
	AutoDeploy        bool   `json:"auto_deploy,omitempty"`
	// AutoWindow is "HH:MM-HH:MM" in the node's local time; empty is any time.
	AutoWindow string `json:"auto_window,omitempty"`
	// DeployDuringBackup skips waiting for a backup of the same container.
	DeployDuringBackup bool     `json:"deploy_during_backup,omitempty"`
	Webhook            bool     `json:"webhook,omitempty"`
	NotifyEvents       []string `json:"notify_events,omitempty"`
	NotifyChannels     []string `json:"notify_channels,omitempty"`
	EnvKeys            []string `json:"env_keys,omitempty"`
}

// DeployProjectState is what update detection and deployments found.
type DeployProjectState struct {
	CheckedAt     *time.Time `json:"checked_at,omitempty"`
	CheckError    string     `json:"check_error,omitempty"`
	CheckNote     string     `json:"check_note,omitempty"`
	Provider      string     `json:"provider,omitempty"`
	Remote        string     `json:"remote,omitempty"`
	RemoteURL     string     `json:"remote_url,omitempty"`
	Repo          string     `json:"repo,omitempty"`
	Branch        string     `json:"branch,omitempty"`
	LocalCommit   string     `json:"local_commit,omitempty"`
	RemoteCommit  string     `json:"remote_commit,omitempty"`
	Behind        int        `json:"behind"`
	Ahead         int        `json:"ahead"`
	Dirty         bool       `json:"dirty,omitempty"`
	Due           bool       `json:"due,omitempty"`
	DueSince      *time.Time `json:"due_since,omitempty"`
	RemoteSubject string     `json:"remote_subject,omitempty"`
	RemoteAuthor  string     `json:"remote_author,omitempty"`
	NotifiedDue   string     `json:"notified_due,omitempty"`
	// AutoCommit is the remote commit automatic deployment last queued.
	AutoCommit  string     `json:"auto_commit,omitempty"`
	NextCheckAt *time.Time `json:"next_check_at,omitempty"`

	LastDeployID     string     `json:"last_deploy_id,omitempty"`
	LastDeployStatus string     `json:"last_deploy_status,omitempty"`
	LastDeployAt     *time.Time `json:"last_deploy_at,omitempty"`
	LastSuccessAt    *time.Time `json:"last_success_at,omitempty"`
}

// DeployProject is one Docker Compose project and how it is updated.
type DeployProject struct {
	ID         string
	ClusterID  string
	WorkloadID string
	Name       string
	WorkingDir string
	Config     DeployProjectConfig
	State      DeployProjectState
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

// DeployStep is one command's progress as the node reported it.
type DeployStep struct {
	Command    string     `json:"command"`
	StartedAt  *time.Time `json:"started_at,omitempty"`
	FinishedAt *time.Time `json:"finished_at,omitempty"`
	ExitCode   *int       `json:"exit_code,omitempty"`
	Tolerated  bool       `json:"continue_on_error,omitempty"`
}

// DeploymentDetail is a deployment's record beyond its indexed columns.
type DeploymentDetail struct {
	ProjectName  string          `json:"project_name"`
	WorkingDir   string          `json:"working_dir"`
	RunAs        string          `json:"run_as,omitempty"`
	Commands     []DeployCommand `json:"commands"`
	TimeoutSec   int             `json:"timeout_sec,omitempty"`
	CommitBefore string          `json:"commit_before,omitempty"`
	CommitAfter  string          `json:"commit_after,omitempty"`
	ExitCode     *int            `json:"exit_code,omitempty"`
	FailedStep   int             `json:"failed_step,omitempty"`
	Error        string          `json:"error,omitempty"`
	WaitingFor   string          `json:"waiting_for,omitempty"`
	Force        bool            `json:"force,omitempty"`
	LogDir       string          `json:"log_dir,omitempty"`
	LogBytes     int64           `json:"log_bytes"`
	LogGone      bool            `json:"log_gone,omitempty"`
	Mode         string          `json:"mode,omitempty"`
	Steps        []DeployStep    `json:"steps,omitempty"`
	CancelBy     string          `json:"cancel_by,omitempty"`
	Health       []string        `json:"health,omitempty"`
	Notified     bool            `json:"notified,omitempty"`
}

// Deployment is one run of a project's update commands.
type Deployment struct {
	ID         string
	ClusterID  string
	ProjectID  string
	WorkloadID string
	BatchID    string
	Status     string
	Trigger    string
	Actor      string
	QueuedAt   time.Time
	StartedAt  *time.Time
	FinishedAt *time.Time
	Detail     DeploymentDetail
}

// DeploymentFilter narrows ListDeployments.
type DeploymentFilter struct {
	ProjectID string
	Active    bool
	Limit     int
}

// DeploySettings are a cluster's deployment defaults and limits.
type DeploySettings struct {
	ClusterID         string          `json:"-"`
	MaxConcurrent     int             `json:"max_concurrent"`
	PerContainer      int             `json:"per_container"`
	LogDir            string          `json:"log_dir"`
	RetentionDays     int             `json:"retention_days"`
	KeepPerProject    int             `json:"keep_per_project"`
	MaxLogGB          float64         `json:"max_log_gb"`
	DefaultCommands   []DeployCommand `json:"default_commands"`
	DetectIntervalSec int             `json:"detect_interval_sec"`
	DefaultTimeoutSec int             `json:"default_timeout_sec"`
	NotifyEvents      []string        `json:"notify_events"`
	UpdatedAt         time.Time       `json:"updated_at"`
}

// DefaultDeployCommand is what a new project runs until an Owner changes it.
const DefaultDeployCommand = "git pull origin main && docker compose up -d --build --force-recreate"

// DefaultDeploySettings are used until an Owner saves settings.
func DefaultDeploySettings(clusterID string) *DeploySettings {
	return &DeploySettings{
		ClusterID: clusterID, MaxConcurrent: 3, PerContainer: 1,
		LogDir: "/var/lib/ndl/deployments", RetentionDays: 30, KeepPerProject: 50, MaxLogGB: 10,
		DefaultCommands:   []DeployCommand{{Run: DefaultDeployCommand}},
		DetectIntervalSec: 600, DefaultTimeoutSec: 1800,
		NotifyEvents: []string{"deploy.failed", "deploy.succeeded", "bulk.finished"},
	}
}

// --- memory ---

type deployMem struct {
	projects    map[string]DeployProject
	deployments map[string]Deployment
	settings    map[string]DeploySettings
	secrets     map[string][2]string
}

func (m *Memory) dm() *deployMem {
	if m.deploy == nil {
		m.deploy = &deployMem{projects: map[string]DeployProject{}, deployments: map[string]Deployment{}, settings: map[string]DeploySettings{}, secrets: map[string][2]string{}}
	}
	return m.deploy
}

func (m *Memory) ListDeployProjects(_ context.Context, clusterID string) ([]DeployProject, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []DeployProject
	for _, p := range m.dm().projects {
		if clusterID == "" || p.ClusterID == clusterID {
			out = append(out, p)
		}
	}
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name) })
	return out, nil
}

func (m *Memory) GetDeployProject(_ context.Context, clusterID, id string) (*DeployProject, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.dm().projects[id]
	if !ok || p.ClusterID != clusterID {
		return nil, nil
	}
	return &p, nil
}

func (m *Memory) PutDeployProject(_ context.Context, p DeployProject) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	d := m.dm()
	for id, cur := range d.projects {
		if id != p.ID && cur.ClusterID == p.ClusterID && cur.WorkloadID == p.WorkloadID && cur.WorkingDir == p.WorkingDir {
			return ErrDeployProjectExists
		}
	}
	now := time.Now().UTC()
	if cur, ok := d.projects[p.ID]; ok {
		p.CreatedAt = cur.CreatedAt
	} else if p.CreatedAt.IsZero() {
		p.CreatedAt = now
	}
	p.UpdatedAt = now
	d.projects[p.ID] = p
	return nil
}

func (m *Memory) UpdateDeployProjectState(_ context.Context, clusterID, id string, st DeployProjectState) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	d := m.dm()
	p, ok := d.projects[id]
	if !ok || p.ClusterID != clusterID {
		return nil
	}
	p.State = st
	d.projects[id] = p
	return nil
}

func (m *Memory) DeleteDeployProject(_ context.Context, clusterID, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	d := m.dm()
	if p, ok := d.projects[id]; ok && p.ClusterID == clusterID {
		delete(d.projects, id)
		delete(d.secrets, id)
		for did, dep := range d.deployments {
			if dep.ProjectID == id {
				delete(d.deployments, did)
			}
		}
	}
	return nil
}

func (m *Memory) DeployProjectSecrets(_ context.Context, clusterID, id string) (map[string]string, string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	d := m.dm()
	if p, ok := d.projects[id]; !ok || p.ClusterID != clusterID {
		return nil, "", nil
	}
	s := d.secrets[id]
	env := map[string]string{}
	_ = json.Unmarshal([]byte(s[0]), &env)
	return env, s[1], nil
}

func (m *Memory) PutDeployProjectSecrets(_ context.Context, clusterID, id string, env map[string]string, webhookSecret string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	d := m.dm()
	if p, ok := d.projects[id]; !ok || p.ClusterID != clusterID {
		return fmt.Errorf("project %s not found", id)
	}
	raw, _ := json.Marshal(env)
	d.secrets[id] = [2]string{string(raw), webhookSecret}
	return nil
}

func (m *Memory) ListDeployments(_ context.Context, clusterID string, f DeploymentFilter) ([]Deployment, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Deployment
	for _, d := range m.dm().deployments {
		if (clusterID != "" && d.ClusterID != clusterID) || (f.ProjectID != "" && d.ProjectID != f.ProjectID) || (f.Active && !DeployActive(d.Status)) {
			continue
		}
		out = append(out, d)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].QueuedAt.After(out[j].QueuedAt) })
	if f.Limit > 0 && len(out) > f.Limit {
		out = out[:f.Limit]
	}
	return out, nil
}

func (m *Memory) GetDeployment(_ context.Context, clusterID, id string) (*Deployment, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	d, ok := m.dm().deployments[id]
	if !ok || d.ClusterID != clusterID {
		return nil, nil
	}
	return &d, nil
}

func (m *Memory) PutDeployment(_ context.Context, d Deployment) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.dm().projects[d.ProjectID]; !ok {
		return fmt.Errorf("project %s not found", d.ProjectID)
	}
	m.dm().deployments[d.ID] = d
	return nil
}

func (m *Memory) DeleteDeployments(_ context.Context, clusterID string, ids []string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, id := range ids {
		if d, ok := m.dm().deployments[id]; ok && d.ClusterID == clusterID {
			delete(m.dm().deployments, id)
		}
	}
	return nil
}

func (m *Memory) GetDeploySettings(_ context.Context, clusterID string) (*DeploySettings, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.dm().settings[clusterID]
	if !ok {
		return nil, nil
	}
	return &s, nil
}

func (m *Memory) PutDeploySettings(_ context.Context, s DeploySettings) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	s.UpdatedAt = time.Now().UTC()
	m.dm().settings[s.ClusterID] = s
	return nil
}

// ErrDeployProjectExists means the folder in that container is already a project.
var ErrDeployProjectExists = errors.New("that folder in that container is already a project")

// --- postgres ---

const deployProjectCols = `id::text, cluster_id::text, workload_id::text, name, working_dir, config, state, created_at, updated_at`

func scanDeployProject(s interface{ Scan(...any) error }) (DeployProject, error) {
	var p DeployProject
	var cfg, st string
	if err := s.Scan(&p.ID, &p.ClusterID, &p.WorkloadID, &p.Name, &p.WorkingDir, &cfg, &st, &p.CreatedAt, &p.UpdatedAt); err != nil {
		return p, err
	}
	_ = json.Unmarshal([]byte(cfg), &p.Config)
	_ = json.Unmarshal([]byte(st), &p.State)
	return p, nil
}

func (p *Postgres) ListDeployProjects(ctx context.Context, clusterID string) ([]DeployProject, error) {
	rows, err := p.DB.QueryContext(ctx, `SELECT `+deployProjectCols+` FROM deploy_projects
WHERE ($1='' OR cluster_id::text=$1) ORDER BY lower(name), working_dir`, clusterID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []DeployProject
	for rows.Next() {
		d, err := scanDeployProject(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

func (p *Postgres) GetDeployProject(ctx context.Context, clusterID, id string) (*DeployProject, error) {
	d, err := scanDeployProject(p.DB.QueryRowContext(ctx, `SELECT `+deployProjectCols+` FROM deploy_projects WHERE cluster_id=$1 AND id=$2`, clusterID, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &d, nil
}

func (p *Postgres) PutDeployProject(ctx context.Context, d DeployProject) error {
	cfg, _ := json.Marshal(d.Config)
	st, _ := json.Marshal(d.State)
	_, err := p.DB.ExecContext(ctx, `
INSERT INTO deploy_projects (id, cluster_id, workload_id, name, working_dir, config, state)
VALUES ($1,$2,$3,$4,$5,$6,$7)
ON CONFLICT (id) DO UPDATE SET name=EXCLUDED.name, working_dir=EXCLUDED.working_dir, workload_id=EXCLUDED.workload_id,
  config=EXCLUDED.config, state=EXCLUDED.state, updated_at=now()`,
		d.ID, d.ClusterID, d.WorkloadID, d.Name, d.WorkingDir, string(cfg), string(st))
	if err != nil && strings.Contains(err.Error(), "deploy_projects_cluster_id_workload_id_working_dir_key") {
		return ErrDeployProjectExists
	}
	return err
}

func (p *Postgres) UpdateDeployProjectState(ctx context.Context, clusterID, id string, st DeployProjectState) error {
	raw, _ := json.Marshal(st)
	_, err := p.DB.ExecContext(ctx, `UPDATE deploy_projects SET state=$3 WHERE cluster_id=$1 AND id=$2`, clusterID, id, string(raw))
	return err
}

func (p *Postgres) DeleteDeployProject(ctx context.Context, clusterID, id string) error {
	_, err := p.DB.ExecContext(ctx, `DELETE FROM deploy_projects WHERE cluster_id=$1 AND id=$2`, clusterID, id)
	return err
}

func (p *Postgres) DeployProjectSecrets(ctx context.Context, clusterID, id string) (map[string]string, string, error) {
	var envRaw, hook string
	err := p.DB.QueryRowContext(ctx, `SELECT env, webhook_secret FROM secrets.deploy_project_secrets WHERE cluster_id=$1 AND project_id=$2`, clusterID, id).Scan(&envRaw, &hook)
	if errors.Is(err, sql.ErrNoRows) {
		return map[string]string{}, "", nil
	}
	if err != nil {
		return nil, "", err
	}
	env := map[string]string{}
	_ = json.Unmarshal([]byte(envRaw), &env)
	return env, hook, nil
}

func (p *Postgres) PutDeployProjectSecrets(ctx context.Context, clusterID, id string, env map[string]string, webhookSecret string) error {
	raw, _ := json.Marshal(env)
	_, err := p.DB.ExecContext(ctx, `
INSERT INTO secrets.deploy_project_secrets (project_id, cluster_id, env, webhook_secret) VALUES ($2,$1,$3,$4)
ON CONFLICT (project_id) DO UPDATE SET env=EXCLUDED.env, webhook_secret=EXCLUDED.webhook_secret, updated_at=now()`,
		clusterID, id, string(raw), webhookSecret)
	return err
}

const deploymentCols = `id::text, cluster_id::text, project_id::text, workload_id::text, batch_id, status, trigger, actor, queued_at, started_at, finished_at, detail`

func scanDeployment(s interface{ Scan(...any) error }) (Deployment, error) {
	var d Deployment
	var started, finished sql.NullTime
	var detail string
	if err := s.Scan(&d.ID, &d.ClusterID, &d.ProjectID, &d.WorkloadID, &d.BatchID, &d.Status, &d.Trigger, &d.Actor, &d.QueuedAt, &started, &finished, &detail); err != nil {
		return d, err
	}
	if started.Valid {
		t := started.Time
		d.StartedAt = &t
	}
	if finished.Valid {
		t := finished.Time
		d.FinishedAt = &t
	}
	_ = json.Unmarshal([]byte(detail), &d.Detail)
	return d, nil
}

func (p *Postgres) ListDeployments(ctx context.Context, clusterID string, f DeploymentFilter) ([]Deployment, error) {
	limit := f.Limit
	if limit <= 0 {
		limit = 100000
	}
	rows, err := p.DB.QueryContext(ctx, `SELECT `+deploymentCols+` FROM deployments
WHERE ($1='' OR cluster_id::text=$1) AND ($2='' OR project_id::text=$2)
  AND (NOT $3 OR status IN ('queued','waiting','starting','running'))
ORDER BY queued_at DESC LIMIT $4`, clusterID, f.ProjectID, f.Active, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Deployment
	for rows.Next() {
		d, err := scanDeployment(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

func (p *Postgres) GetDeployment(ctx context.Context, clusterID, id string) (*Deployment, error) {
	d, err := scanDeployment(p.DB.QueryRowContext(ctx, `SELECT `+deploymentCols+` FROM deployments WHERE cluster_id=$1 AND id=$2`, clusterID, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &d, nil
}

func (p *Postgres) PutDeployment(ctx context.Context, d Deployment) error {
	detail, _ := json.Marshal(d.Detail)
	_, err := p.DB.ExecContext(ctx, `
INSERT INTO deployments (id, cluster_id, project_id, workload_id, batch_id, status, trigger, actor, queued_at, started_at, finished_at, detail)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)
ON CONFLICT (id) DO UPDATE SET status=EXCLUDED.status, started_at=EXCLUDED.started_at, finished_at=EXCLUDED.finished_at, detail=EXCLUDED.detail`,
		d.ID, d.ClusterID, d.ProjectID, d.WorkloadID, d.BatchID, d.Status, d.Trigger, d.Actor, d.QueuedAt, d.StartedAt, d.FinishedAt, string(detail))
	return err
}

func (p *Postgres) DeleteDeployments(ctx context.Context, clusterID string, ids []string) error {
	for _, id := range ids {
		if _, err := p.DB.ExecContext(ctx, `DELETE FROM deployments WHERE cluster_id=$1 AND id=$2`, clusterID, id); err != nil {
			return err
		}
	}
	return nil
}

func (p *Postgres) GetDeploySettings(ctx context.Context, clusterID string) (*DeploySettings, error) {
	var body string
	var at time.Time
	err := p.DB.QueryRowContext(ctx, `SELECT body, updated_at FROM deploy_settings WHERE cluster_id=$1`, clusterID).Scan(&body, &at)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	s := DefaultDeploySettings(clusterID)
	if err := json.Unmarshal([]byte(body), s); err != nil {
		return nil, err
	}
	s.ClusterID, s.UpdatedAt = clusterID, at
	return s, nil
}

func (p *Postgres) PutDeploySettings(ctx context.Context, s DeploySettings) error {
	raw, _ := json.Marshal(s)
	_, err := p.DB.ExecContext(ctx, `
INSERT INTO deploy_settings (cluster_id, body) VALUES ($1,$2)
ON CONFLICT (cluster_id) DO UPDATE SET body=EXCLUDED.body, updated_at=now()`, s.ClusterID, string(raw))
	return err
}
