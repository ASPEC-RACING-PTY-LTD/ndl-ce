package appdb

import (
	"context"
	"database/sql"
	"errors"
	"sync"
)

type claudeAgentMem struct {
	mu     sync.Mutex
	config map[string][]byte
}

func (m *Memory) ClaudeAgentConfig(_ context.Context, clusterID string) ([]byte, error) {
	m.claudeAgent.mu.Lock()
	defer m.claudeAgent.mu.Unlock()
	body, ok := m.claudeAgent.config[clusterID]
	if !ok {
		return nil, nil
	}
	return append([]byte(nil), body...), nil
}

func (m *Memory) PutClaudeAgentConfig(_ context.Context, clusterID string, body []byte, _ string) error {
	m.claudeAgent.mu.Lock()
	defer m.claudeAgent.mu.Unlock()
	if m.claudeAgent.config == nil {
		m.claudeAgent.config = map[string][]byte{}
	}
	m.claudeAgent.config[clusterID] = append([]byte(nil), body...)
	return nil
}

func (p *Postgres) ClaudeAgentConfig(ctx context.Context, clusterID string) ([]byte, error) {
	var body []byte
	err := p.DB.QueryRowContext(ctx, `SELECT body FROM claude_agent_config WHERE cluster_id=$1`, clusterID).Scan(&body)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return body, err
}

func (p *Postgres) PutClaudeAgentConfig(ctx context.Context, clusterID string, body []byte, by string) error {
	_, err := p.DB.ExecContext(ctx, `
INSERT INTO claude_agent_config (cluster_id, body, updated_by, updated_at) VALUES ($1,$2,$3,now())
ON CONFLICT (cluster_id) DO UPDATE SET body=EXCLUDED.body, updated_by=EXCLUDED.updated_by, updated_at=now()`,
		clusterID, body, by)
	return err
}
