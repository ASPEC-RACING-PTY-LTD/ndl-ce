package appdb

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// GameServerSettings are cluster-wide game server settings.
type GameServerSettings struct {
	ClusterID           string
	CurseForgeKeySealed string
	UpdatedBy           string
	UpdatedAt           time.Time
}

func (m *Memory) GetGameServerSettings(_ context.Context, clusterID string) (*GameServerSettings, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if st, ok := m.gsSettings[clusterID]; ok {
		return &st, nil
	}
	return nil, nil
}

func (m *Memory) PutGameServerSettings(_ context.Context, st GameServerSettings) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.gsSettings == nil {
		m.gsSettings = map[string]GameServerSettings{}
	}
	st.UpdatedAt = time.Now().UTC()
	m.gsSettings[st.ClusterID] = st
	return nil
}

func (p *Postgres) GetGameServerSettings(ctx context.Context, clusterID string) (*GameServerSettings, error) {
	if !ValidUUID(clusterID) {
		return nil, nil
	}
	var st GameServerSettings
	err := p.DB.QueryRowContext(ctx, `SELECT cluster_id::text, curseforge_key_sealed, updated_by, updated_at
FROM gameserver_settings WHERE cluster_id=$1`, clusterID).Scan(&st.ClusterID, &st.CurseForgeKeySealed, &st.UpdatedBy, &st.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &st, nil
}

func (p *Postgres) PutGameServerSettings(ctx context.Context, st GameServerSettings) error {
	_, err := p.DB.ExecContext(ctx, `
INSERT INTO gameserver_settings (cluster_id, curseforge_key_sealed, updated_by, updated_at)
VALUES ($1,$2,$3,now())
ON CONFLICT (cluster_id) DO UPDATE SET curseforge_key_sealed=EXCLUDED.curseforge_key_sealed,
  updated_by=EXCLUDED.updated_by, updated_at=now()`,
		st.ClusterID, st.CurseForgeKeySealed, st.UpdatedBy)
	return err
}
