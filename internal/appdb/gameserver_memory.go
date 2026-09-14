package appdb

import (
	"context"
	"fmt"
	"sort"
	"time"
)

func (m *Memory) gsInit() {
	if m.gameServers == nil {
		m.gameServers = map[string]GameServer{}
	}
	if m.gameTemplates == nil {
		m.gameTemplates = map[string]GameTemplate{}
	}
	if m.gameSources == nil {
		m.gameSources = map[string]GameSource{}
	}
	if m.gameACL == nil {
		m.gameACL = map[string]GameACL{}
	}
	if m.gameEvents == nil {
		m.gameEvents = []GameEvent{}
	}
	if m.gameSchedules == nil {
		m.gameSchedules = map[string]GameSchedule{}
	}
	if m.gameBackups == nil {
		m.gameBackups = map[string]GameBackup{}
	}
	if m.gameContent == nil {
		m.gameContent = map[string]GameContent{}
	}
	if m.gameProfiles == nil {
		m.gameProfiles = map[string]GameContentProfile{}
	}
	if m.gameFavs == nil {
		m.gameFavs = map[string]GameConsoleFav{}
	}
	if m.gameHist == nil {
		m.gameHist = map[string][]string{}
	}
	if m.gamePrefs == nil {
		m.gamePrefs = map[string]string{}
	}
}

func (m *Memory) CreateGameServer(_ context.Context, s GameServer) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.gsInit()
	if s.ID == "" || s.ClusterID == "" {
		return fmt.Errorf("game server identity is required")
	}
	if _, ok := m.gameServers[s.ID]; ok {
		return fmt.Errorf("game server exists")
	}
	if s.CreatedAt.IsZero() {
		s.CreatedAt = time.Now().UTC()
	}
	s.UpdatedAt = s.CreatedAt
	m.gameServers[s.ID] = s
	return nil
}

func (m *Memory) GetGameServer(_ context.Context, clusterID, id string) (*GameServer, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.gsInit()
	s, ok := m.gameServers[id]
	if !ok || s.ClusterID != clusterID {
		return nil, nil
	}
	cp := s
	return &cp, nil
}

func (m *Memory) ListGameServers(_ context.Context, clusterID string) ([]GameServer, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.gsInit()
	var out []GameServer
	for _, s := range m.gameServers {
		if s.ClusterID == clusterID {
			out = append(out, s)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func (m *Memory) UpdateGameServer(_ context.Context, s GameServer) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.gsInit()
	cur, ok := m.gameServers[s.ID]
	if !ok || cur.ClusterID != s.ClusterID {
		return fmt.Errorf("game server not found")
	}
	s.CreatedAt = cur.CreatedAt
	s.UpdatedAt = time.Now().UTC()
	m.gameServers[s.ID] = s
	return nil
}

func (m *Memory) DeleteGameServer(_ context.Context, clusterID, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.gsInit()
	s, ok := m.gameServers[id]
	if !ok || s.ClusterID != clusterID {
		return fmt.Errorf("game server not found")
	}
	delete(m.gameServers, id)
	return nil
}

func (m *Memory) UpsertGameTemplate(_ context.Context, t GameTemplate) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.gsInit()
	if t.UpdatedAt.IsZero() {
		t.UpdatedAt = time.Now().UTC()
	}
	if t.CreatedAt.IsZero() {
		t.CreatedAt = t.UpdatedAt
	}
	m.gameTemplates[t.ClusterID+"/"+t.ID] = t
	return nil
}

func (m *Memory) GetGameTemplate(_ context.Context, clusterID, id string) (*GameTemplate, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.gsInit()
	t, ok := m.gameTemplates[clusterID+"/"+id]
	if !ok {
		return nil, nil
	}
	cp := t
	return &cp, nil
}

func (m *Memory) ListGameTemplates(_ context.Context, clusterID string) ([]GameTemplate, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.gsInit()
	var out []GameTemplate
	for _, t := range m.gameTemplates {
		if t.ClusterID == clusterID {
			out = append(out, t)
		}
	}
	return out, nil
}

func (m *Memory) UpsertGameSource(_ context.Context, s GameSource) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.gsInit()
	if s.CreatedAt.IsZero() {
		s.CreatedAt = time.Now().UTC()
	}
	m.gameSources[s.ClusterID+"/"+s.ID] = s
	return nil
}

func (m *Memory) ListGameSources(_ context.Context, clusterID string) ([]GameSource, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.gsInit()
	var out []GameSource
	for _, s := range m.gameSources {
		if s.ClusterID == clusterID {
			out = append(out, s)
		}
	}
	return out, nil
}

func (m *Memory) DeleteGameSource(_ context.Context, clusterID, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.gsInit()
	delete(m.gameSources, clusterID+"/"+id)
	return nil
}

func (m *Memory) UpsertGameACL(_ context.Context, a GameACL) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.gsInit()
	if a.CreatedAt.IsZero() {
		a.CreatedAt = time.Now().UTC()
	}
	m.gameACL[a.ServerID+"/"+a.UserID] = a
	return nil
}

func (m *Memory) ListGameACL(_ context.Context, serverID string) ([]GameACL, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.gsInit()
	var out []GameACL
	for _, a := range m.gameACL {
		if a.ServerID == serverID {
			out = append(out, a)
		}
	}
	return out, nil
}

func (m *Memory) GetGameACL(_ context.Context, serverID, userID string) (*GameACL, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.gsInit()
	a, ok := m.gameACL[serverID+"/"+userID]
	if !ok {
		return nil, nil
	}
	cp := a
	return &cp, nil
}

func (m *Memory) DeleteGameACL(_ context.Context, serverID, userID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.gsInit()
	delete(m.gameACL, serverID+"/"+userID)
	return nil
}

func (m *Memory) InsertGameEvent(_ context.Context, e GameEvent) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.gsInit()
	if e.CreatedAt.IsZero() {
		e.CreatedAt = time.Now().UTC()
	}
	m.gameEvents = append(m.gameEvents, e)
	return nil
}

func (m *Memory) ListGameEvents(_ context.Context, clusterID, serverID string, limit int) ([]GameEvent, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.gsInit()
	var out []GameEvent
	for i := len(m.gameEvents) - 1; i >= 0; i-- {
		e := m.gameEvents[i]
		if e.ClusterID != clusterID {
			continue
		}
		if serverID != "" && e.ServerID != serverID {
			continue
		}
		out = append(out, e)
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out, nil
}

func (m *Memory) CreateGameSchedule(_ context.Context, s GameSchedule) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.gsInit()
	if s.CreatedAt.IsZero() {
		s.CreatedAt = time.Now().UTC()
	}
	m.gameSchedules[s.ID] = s
	return nil
}

func (m *Memory) ListGameSchedules(_ context.Context, serverID string) ([]GameSchedule, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.gsInit()
	var out []GameSchedule
	for _, s := range m.gameSchedules {
		if s.ServerID == serverID {
			out = append(out, s)
		}
	}
	return out, nil
}

func (m *Memory) DeleteGameSchedule(_ context.Context, serverID, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.gsInit()
	s, ok := m.gameSchedules[id]
	if !ok || s.ServerID != serverID {
		return fmt.Errorf("schedule not found")
	}
	delete(m.gameSchedules, id)
	return nil
}

func (m *Memory) UpdateGameSchedule(_ context.Context, s GameSchedule) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.gsInit()
	m.gameSchedules[s.ID] = s
	return nil
}

func (m *Memory) CreateGameBackup(_ context.Context, b GameBackup) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.gsInit()
	if b.CreatedAt.IsZero() {
		b.CreatedAt = time.Now().UTC()
	}
	m.gameBackups[b.ID] = b
	return nil
}

func (m *Memory) ListGameBackups(_ context.Context, serverID string) ([]GameBackup, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.gsInit()
	var out []GameBackup
	for _, b := range m.gameBackups {
		if b.ServerID == serverID {
			out = append(out, b)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out, nil
}

func (m *Memory) GetGameBackup(_ context.Context, serverID, id string) (*GameBackup, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.gsInit()
	b, ok := m.gameBackups[id]
	if !ok || b.ServerID != serverID {
		return nil, nil
	}
	cp := b
	return &cp, nil
}

func (m *Memory) DeleteGameBackup(_ context.Context, serverID, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.gsInit()
	b, ok := m.gameBackups[id]
	if !ok || b.ServerID != serverID {
		return fmt.Errorf("backup not found")
	}
	delete(m.gameBackups, id)
	return nil
}

func (m *Memory) UpsertGameContent(_ context.Context, c GameContent) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.gsInit()
	if c.CreatedAt.IsZero() {
		c.CreatedAt = time.Now().UTC()
	}
	c.UpdatedAt = time.Now().UTC()
	m.gameContent[c.ID] = c
	return nil
}

func (m *Memory) ListGameContent(_ context.Context, serverID string) ([]GameContent, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.gsInit()
	var out []GameContent
	for _, c := range m.gameContent {
		if c.ServerID == serverID {
			out = append(out, c)
		}
	}
	return out, nil
}

func (m *Memory) GetGameContent(_ context.Context, serverID, id string) (*GameContent, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.gsInit()
	c, ok := m.gameContent[id]
	if !ok || c.ServerID != serverID {
		return nil, nil
	}
	cp := c
	return &cp, nil
}

func (m *Memory) DeleteGameContent(_ context.Context, serverID, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.gsInit()
	delete(m.gameContent, id)
	return nil
}

func (m *Memory) UpsertGameContentProfile(_ context.Context, p GameContentProfile) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.gsInit()
	if p.CreatedAt.IsZero() {
		p.CreatedAt = time.Now().UTC()
	}
	m.gameProfiles[p.ID] = p
	return nil
}

func (m *Memory) ListGameContentProfiles(_ context.Context, serverID string) ([]GameContentProfile, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.gsInit()
	var out []GameContentProfile
	for _, p := range m.gameProfiles {
		if p.ServerID == serverID {
			out = append(out, p)
		}
	}
	return out, nil
}

func (m *Memory) UpsertGameConsoleFav(_ context.Context, f GameConsoleFav) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.gsInit()
	m.gameFavs[f.ID] = f
	return nil
}

func (m *Memory) ListGameConsoleFavs(_ context.Context, serverID string) ([]GameConsoleFav, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.gsInit()
	var out []GameConsoleFav
	for _, f := range m.gameFavs {
		if f.ServerID == serverID {
			out = append(out, f)
		}
	}
	return out, nil
}

func (m *Memory) DeleteGameConsoleFav(_ context.Context, serverID, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.gsInit()
	delete(m.gameFavs, id)
	return nil
}

func (m *Memory) AppendGameConsoleHist(_ context.Context, serverID, command string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.gsInit()
	m.gameHist[serverID] = append(m.gameHist[serverID], command)
	if len(m.gameHist[serverID]) > 200 {
		m.gameHist[serverID] = m.gameHist[serverID][len(m.gameHist[serverID])-200:]
	}
	return nil
}

func (m *Memory) ListGameConsoleHist(_ context.Context, serverID string, limit int) ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.gsInit()
	h := append([]string{}, m.gameHist[serverID]...)
	if limit > 0 && len(h) > limit {
		h = h[len(h)-limit:]
	}
	return h, nil
}

func (m *Memory) GetGameUserPref(_ context.Context, clusterID, userID, key string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.gsInit()
	return m.gamePrefs[clusterID+"/"+userID+"/"+key], nil
}

func (m *Memory) SetGameUserPref(_ context.Context, clusterID, userID, key, value string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.gsInit()
	m.gamePrefs[clusterID+"/"+userID+"/"+key] = value
	return nil
}
