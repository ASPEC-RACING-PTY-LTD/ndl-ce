package appdb

import (
	"context"
	"database/sql"
	"errors"
	"sync"
)

type edgeMem struct {
	mu     sync.Mutex
	config map[string][]byte
}

func (m *Memory) EdgeConfig(_ context.Context, clusterID string) ([]byte, error) {
	m.edge.mu.Lock()
	defer m.edge.mu.Unlock()
	body, ok := m.edge.config[clusterID]
	if !ok {
		return nil, nil
	}
	return append([]byte(nil), body...), nil
}

func (m *Memory) PutEdgeConfig(_ context.Context, clusterID string, body []byte, _ string) error {
	m.edge.mu.Lock()
	defer m.edge.mu.Unlock()
	if m.edge.config == nil {
		m.edge.config = map[string][]byte{}
	}
	m.edge.config[clusterID] = append([]byte(nil), body...)
	return nil
}

func (p *Postgres) EdgeConfig(ctx context.Context, clusterID string) ([]byte, error) {
	var body []byte
	err := p.DB.QueryRowContext(ctx, `SELECT body FROM edge_config WHERE cluster_id=$1`, clusterID).Scan(&body)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return body, err
}

func (p *Postgres) PutEdgeConfig(ctx context.Context, clusterID string, body []byte, by string) error {
	_, err := p.DB.ExecContext(ctx, `
INSERT INTO edge_config (cluster_id, body, updated_by, updated_at) VALUES ($1,$2,$3,now())
ON CONFLICT (cluster_id) DO UPDATE SET body=EXCLUDED.body, updated_by=EXCLUDED.updated_by, updated_at=now()`,
		clusterID, body, by)
	return err
}
