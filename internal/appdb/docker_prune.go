package appdb

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// DockerPruneSettings schedules pruning of unused Docker images and build
// cache. A missing row means the defaults: on, daily at 06:00 Melbourne time.
type DockerPruneSettings struct {
	ClusterID  string
	Enabled    bool
	RunAt      string // HH:MM
	Timezone   string
	LastRunAt  *time.Time
	LastResult string // JSON summary of the last run
	UpdatedBy  string
	UpdatedAt  time.Time
}

func (m *Memory) GetDockerPruneSettings(_ context.Context, clusterID string) (*DockerPruneSettings, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if st, ok := m.dockerPrune[clusterID]; ok {
		return &st, nil
	}
	return nil, nil
}

func (m *Memory) PutDockerPruneSettings(_ context.Context, st DockerPruneSettings) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.dockerPrune == nil {
		m.dockerPrune = map[string]DockerPruneSettings{}
	}
	st.UpdatedAt = time.Now().UTC()
	m.dockerPrune[st.ClusterID] = st
	return nil
}

func (p *Postgres) GetDockerPruneSettings(ctx context.Context, clusterID string) (*DockerPruneSettings, error) {
	var st DockerPruneSettings
	var last sql.NullTime
	err := p.DB.QueryRowContext(ctx, `SELECT cluster_id::text, enabled, run_at, timezone, last_run_at, last_result, updated_by, updated_at
FROM docker_prune_settings WHERE cluster_id=$1`, clusterID).Scan(&st.ClusterID, &st.Enabled, &st.RunAt, &st.Timezone, &last, &st.LastResult, &st.UpdatedBy, &st.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if last.Valid {
		t := last.Time
		st.LastRunAt = &t
	}
	return &st, nil
}

func (p *Postgres) PutDockerPruneSettings(ctx context.Context, st DockerPruneSettings) error {
	_, err := p.DB.ExecContext(ctx, `
INSERT INTO docker_prune_settings (cluster_id, enabled, run_at, timezone, last_run_at, last_result, updated_by, updated_at)
VALUES ($1,$2,$3,$4,$5,$6,$7,now())
ON CONFLICT (cluster_id) DO UPDATE SET enabled=EXCLUDED.enabled, run_at=EXCLUDED.run_at, timezone=EXCLUDED.timezone,
  last_run_at=EXCLUDED.last_run_at, last_result=EXCLUDED.last_result, updated_by=EXCLUDED.updated_by, updated_at=now()`,
		st.ClusterID, st.Enabled, st.RunAt, st.Timezone, st.LastRunAt, st.LastResult, st.UpdatedBy)
	return err
}
