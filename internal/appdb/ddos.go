package appdb

import (
	"context"
	"database/sql"
	"errors"
	"sort"
	"sync"
	"time"
)

// DDoSEvent is one attack DDoS protection detected on a target.
type DDoSEvent struct {
	ID             string     `json:"id"`
	ClusterID      string     `json:"-"`
	RuleID         string     `json:"rule_id"`
	Target         string     `json:"target"`
	StartedAt      time.Time  `json:"started_at"`
	EndedAt        *time.Time `json:"ended_at,omitempty"`
	PeakPPS        int64      `json:"peak_pps"`
	DroppedPackets int64      `json:"dropped_packets"`
	DroppedBytes   int64      `json:"dropped_bytes"`
	TopReason      string     `json:"top_reason"`
	Sources        int        `json:"sources"`
}

// ddosEventsKept bounds the attack history per cluster.
const ddosEventsKept = 500

type ddosMem struct {
	mu     sync.Mutex
	config map[string][]byte
	events map[string]DDoSEvent
}

func (m *Memory) DDoSConfig(_ context.Context, clusterID string) ([]byte, error) {
	m.ddos.mu.Lock()
	defer m.ddos.mu.Unlock()
	body, ok := m.ddos.config[clusterID]
	if !ok {
		return nil, nil
	}
	return append([]byte(nil), body...), nil
}

func (m *Memory) PutDDoSConfig(_ context.Context, clusterID string, body []byte, _ string) error {
	m.ddos.mu.Lock()
	defer m.ddos.mu.Unlock()
	if m.ddos.config == nil {
		m.ddos.config = map[string][]byte{}
	}
	m.ddos.config[clusterID] = append([]byte(nil), body...)
	return nil
}

func (m *Memory) PutDDoSEvent(_ context.Context, ev DDoSEvent) error {
	if ev.ID == "" {
		return errors.New("event id is required")
	}
	m.ddos.mu.Lock()
	defer m.ddos.mu.Unlock()
	if m.ddos.events == nil {
		m.ddos.events = map[string]DDoSEvent{}
	}
	m.ddos.events[ev.ID] = ev
	return nil
}

func (m *Memory) ListDDoSEvents(_ context.Context, clusterID string, limit int) ([]DDoSEvent, error) {
	m.ddos.mu.Lock()
	defer m.ddos.mu.Unlock()
	var out []DDoSEvent
	for _, ev := range m.ddos.events {
		if ev.ClusterID == clusterID {
			out = append(out, ev)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].StartedAt.After(out[j].StartedAt) })
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (p *Postgres) DDoSConfig(ctx context.Context, clusterID string) ([]byte, error) {
	var body []byte
	err := p.DB.QueryRowContext(ctx, `SELECT body FROM ddos_config WHERE cluster_id=$1`, clusterID).Scan(&body)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return body, err
}

func (p *Postgres) PutDDoSConfig(ctx context.Context, clusterID string, body []byte, by string) error {
	_, err := p.DB.ExecContext(ctx, `
INSERT INTO ddos_config (cluster_id, body, updated_by, updated_at) VALUES ($1,$2,$3,now())
ON CONFLICT (cluster_id) DO UPDATE SET body=EXCLUDED.body, updated_by=EXCLUDED.updated_by, updated_at=now()`,
		clusterID, body, by)
	return err
}

func (p *Postgres) PutDDoSEvent(ctx context.Context, ev DDoSEvent) error {
	if !ValidUUID(ev.ID) {
		return errors.New("event id must be a UUID")
	}
	_, err := p.DB.ExecContext(ctx, `
INSERT INTO ddos_events (id, cluster_id, rule_id, target, started_at, ended_at, peak_pps, dropped_packets, dropped_bytes, top_reason, sources)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)
ON CONFLICT (id) DO UPDATE SET ended_at=EXCLUDED.ended_at, peak_pps=EXCLUDED.peak_pps, dropped_packets=EXCLUDED.dropped_packets,
  dropped_bytes=EXCLUDED.dropped_bytes, top_reason=EXCLUDED.top_reason, sources=EXCLUDED.sources, target=EXCLUDED.target`,
		ev.ID, ev.ClusterID, ev.RuleID, ev.Target, ev.StartedAt, ev.EndedAt, ev.PeakPPS, ev.DroppedPackets, ev.DroppedBytes, ev.TopReason, ev.Sources)
	if err != nil {
		return err
	}
	_, err = p.DB.ExecContext(ctx, `
DELETE FROM ddos_events WHERE cluster_id=$1 AND id NOT IN (
  SELECT id FROM ddos_events WHERE cluster_id=$1 ORDER BY started_at DESC LIMIT $2)`, ev.ClusterID, ddosEventsKept)
	return err
}

func (p *Postgres) ListDDoSEvents(ctx context.Context, clusterID string, limit int) ([]DDoSEvent, error) {
	if limit <= 0 || limit > ddosEventsKept {
		limit = ddosEventsKept
	}
	rows, err := p.DB.QueryContext(ctx, `
SELECT id::text, cluster_id::text, rule_id, target, started_at, ended_at, peak_pps, dropped_packets, dropped_bytes, top_reason, sources
FROM ddos_events WHERE cluster_id=$1 ORDER BY started_at DESC LIMIT $2`, clusterID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []DDoSEvent
	for rows.Next() {
		var ev DDoSEvent
		var ended sql.NullTime
		if err := rows.Scan(&ev.ID, &ev.ClusterID, &ev.RuleID, &ev.Target, &ev.StartedAt, &ended, &ev.PeakPPS,
			&ev.DroppedPackets, &ev.DroppedBytes, &ev.TopReason, &ev.Sources); err != nil {
			return nil, err
		}
		if ended.Valid {
			t := ended.Time
			ev.EndedAt = &t
		}
		out = append(out, ev)
	}
	return out, rows.Err()
}
