package appdb

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// ClusterJoinSettings is how new nodes reach this control node to join.
// AccessSecretSealed is a Cloudflare Access service token secret, sealed.
type ClusterJoinSettings struct {
	ClusterID          string
	JoinURL            string
	AccessClientID     string
	AccessSecretSealed string
	UpdatedBy          string
	UpdatedAt          time.Time
}

func (m *Memory) GetClusterJoinSettings(_ context.Context, clusterID string) (*ClusterJoinSettings, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if st, ok := m.joinSettings[clusterID]; ok {
		return &st, nil
	}
	return nil, nil
}

func (m *Memory) PutClusterJoinSettings(_ context.Context, st ClusterJoinSettings) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.joinSettings == nil {
		m.joinSettings = map[string]ClusterJoinSettings{}
	}
	st.UpdatedAt = time.Now().UTC()
	m.joinSettings[st.ClusterID] = st
	return nil
}

func (p *Postgres) GetClusterJoinSettings(ctx context.Context, clusterID string) (*ClusterJoinSettings, error) {
	if !ValidUUID(clusterID) {
		return nil, nil
	}
	var st ClusterJoinSettings
	err := p.DB.QueryRowContext(ctx, `SELECT cluster_id::text, join_url, access_client_id, access_secret_sealed, updated_by, updated_at
FROM cluster_join_settings WHERE cluster_id=$1`, clusterID).Scan(&st.ClusterID, &st.JoinURL, &st.AccessClientID, &st.AccessSecretSealed, &st.UpdatedBy, &st.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &st, nil
}

func (p *Postgres) PutClusterJoinSettings(ctx context.Context, st ClusterJoinSettings) error {
	_, err := p.DB.ExecContext(ctx, `
INSERT INTO cluster_join_settings (cluster_id, join_url, access_client_id, access_secret_sealed, updated_by, updated_at)
VALUES ($1,$2,$3,$4,$5,now())
ON CONFLICT (cluster_id) DO UPDATE SET join_url=EXCLUDED.join_url, access_client_id=EXCLUDED.access_client_id,
  access_secret_sealed=EXCLUDED.access_secret_sealed, updated_by=EXCLUDED.updated_by, updated_at=now()`,
		st.ClusterID, st.JoinURL, st.AccessClientID, st.AccessSecretSealed, st.UpdatedBy)
	return err
}
