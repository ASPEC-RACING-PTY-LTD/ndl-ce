package appdb

import (
	"context"
	"fmt"
	"time"
)

func (m *Memory) CreateIOSession(_ context.Context, s IOSession) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.ioSessions == nil {
		m.ioSessions = map[string]IOSession{}
	}
	if _, ok := m.ioSessions[s.ID]; ok {
		return fmt.Errorf("io session already exists")
	}
	for _, existing := range m.ioSessions {
		if existing.TicketHash == s.TicketHash {
			return fmt.Errorf("io session ticket already exists")
		}
	}
	if s.CreatedAt.IsZero() {
		s.CreatedAt = time.Now().UTC()
	}
	if s.CWD == "" {
		s.CWD = "/"
	}
	if s.State == "" {
		s.State = IOStatePending
	}
	m.ioSessions[s.ID] = s
	return nil
}

func (m *Memory) GetIOSession(_ context.Context, clusterID, id string) (*IOSession, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.ioSessions[id]
	if !ok || s.ClusterID != clusterID {
		return nil, nil
	}
	cp := s
	return &cp, nil
}

func (m *Memory) GetIOSessionByTicketHash(_ context.Context, ticketHash string) (*IOSession, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if ticketHash == "" {
		return nil, nil
	}
	for _, s := range m.ioSessions {
		if s.TicketHash == ticketHash {
			cp := s
			return &cp, nil
		}
	}
	return nil, nil
}

func (m *Memory) UpdateIOSession(_ context.Context, s IOSession) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	cur, ok := m.ioSessions[s.ID]
	if !ok {
		return fmt.Errorf("io session not found")
	}
	cur.State = s.State
	cur.Reason = s.Reason
	cur.CWD = s.CWD
	cur.ConnectedAt = s.ConnectedAt
	cur.EndedAt = s.EndedAt
	m.ioSessions[s.ID] = cur
	return nil
}
