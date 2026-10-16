package appdb

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// CodeholdCLISettings configures the CodeHold CLI extra. Zero values mean the
// defaults (applied by the control plane).
type CodeholdCLISettings struct {
	ClusterID      string
	URL            string
	AutoInstall    bool
	Concurrency    int
	TimeoutSeconds int
	UpdatedBy      string
	UpdatedAt      time.Time
}

func (m *Memory) GetCodeholdCLISettings(_ context.Context, clusterID string) (*CodeholdCLISettings, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if st, ok := m.chCLI[clusterID]; ok {
		return &st, nil
	}
	return nil, nil
}

func (m *Memory) PutCodeholdCLISettings(_ context.Context, st CodeholdCLISettings) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.chCLI == nil {
		m.chCLI = map[string]CodeholdCLISettings{}
	}
	st.UpdatedAt = time.Now().UTC()
	m.chCLI[st.ClusterID] = st
	return nil
}

func (p *Postgres) GetCodeholdCLISettings(ctx context.Context, clusterID string) (*CodeholdCLISettings, error) {
	var st CodeholdCLISettings
	err := p.DB.QueryRowContext(ctx, `SELECT cluster_id::text, url, auto_install, concurrency, timeout_seconds, updated_by, updated_at
FROM codehold_cli_settings WHERE cluster_id=$1`, clusterID).Scan(&st.ClusterID, &st.URL, &st.AutoInstall, &st.Concurrency, &st.TimeoutSeconds, &st.UpdatedBy, &st.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &st, nil
}

func (p *Postgres) PutCodeholdCLISettings(ctx context.Context, st CodeholdCLISettings) error {
	_, err := p.DB.ExecContext(ctx, `
INSERT INTO codehold_cli_settings (cluster_id, url, auto_install, concurrency, timeout_seconds, updated_by, updated_at)
VALUES ($1,$2,$3,$4,$5,$6,now())
ON CONFLICT (cluster_id) DO UPDATE SET url=EXCLUDED.url, auto_install=EXCLUDED.auto_install, concurrency=EXCLUDED.concurrency,
  timeout_seconds=EXCLUDED.timeout_seconds, updated_by=EXCLUDED.updated_by, updated_at=now()`,
		st.ClusterID, st.URL, st.AutoInstall, st.Concurrency, st.TimeoutSeconds, st.UpdatedBy)
	return err
}
