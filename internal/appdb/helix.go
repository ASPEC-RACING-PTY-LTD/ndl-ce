package appdb

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"sync"
	"time"
)

// HelixEvent is one entry in the Helix event feed. Seq orders the feed and
// is Helix's cursor.
type HelixEvent struct {
	Seq       int64
	ClusterID string
	ID        string
	Type      string
	Severity  string
	MachineID string
	Body      json.RawMessage
	CreatedAt time.Time
}

type helixMem struct {
	mu     sync.Mutex
	config map[string][]byte
	events []HelixEvent
	seq    int64
}

func (m *Memory) HelixConfig(_ context.Context, clusterID string) ([]byte, error) {
	m.helix.mu.Lock()
	defer m.helix.mu.Unlock()
	body, ok := m.helix.config[clusterID]
	if !ok {
		return nil, nil
	}
	return append([]byte(nil), body...), nil
}

func (m *Memory) PutHelixConfig(_ context.Context, clusterID string, body []byte, _ string) error {
	m.helix.mu.Lock()
	defer m.helix.mu.Unlock()
	if m.helix.config == nil {
		m.helix.config = map[string][]byte{}
	}
	m.helix.config[clusterID] = append([]byte(nil), body...)
	return nil
}

func (m *Memory) InsertHelixEvent(_ context.Context, e HelixEvent) (int64, error) {
	m.helix.mu.Lock()
	defer m.helix.mu.Unlock()
	m.helix.seq++
	e.Seq = m.helix.seq
	m.helix.events = append(m.helix.events, e)
	return e.Seq, nil
}

func (m *Memory) ListHelixEvents(_ context.Context, clusterID string, after int64, limit int) ([]HelixEvent, error) {
	m.helix.mu.Lock()
	defer m.helix.mu.Unlock()
	out := []HelixEvent{}
	for _, e := range m.helix.events {
		if e.ClusterID == clusterID && e.Seq > after {
			out = append(out, e)
			if len(out) >= limit {
				break
			}
		}
	}
	return out, nil
}

func (m *Memory) LatestHelixEventSeq(_ context.Context, clusterID string) (int64, error) {
	m.helix.mu.Lock()
	defer m.helix.mu.Unlock()
	var max int64
	for _, e := range m.helix.events {
		if e.ClusterID == clusterID && e.Seq > max {
			max = e.Seq
		}
	}
	return max, nil
}

func (m *Memory) PruneHelixEvents(_ context.Context, clusterID string, before time.Time) error {
	m.helix.mu.Lock()
	defer m.helix.mu.Unlock()
	kept := m.helix.events[:0]
	for _, e := range m.helix.events {
		if e.ClusterID != clusterID || !e.CreatedAt.Before(before) {
			kept = append(kept, e)
		}
	}
	m.helix.events = kept
	return nil
}

func (p *Postgres) HelixConfig(ctx context.Context, clusterID string) ([]byte, error) {
	var body []byte
	err := p.DB.QueryRowContext(ctx, `SELECT body FROM helix_config WHERE cluster_id=$1`, clusterID).Scan(&body)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return body, err
}

func (p *Postgres) PutHelixConfig(ctx context.Context, clusterID string, body []byte, by string) error {
	_, err := p.DB.ExecContext(ctx, `
INSERT INTO helix_config (cluster_id, body, updated_by, updated_at) VALUES ($1,$2,$3,now())
ON CONFLICT (cluster_id) DO UPDATE SET body=EXCLUDED.body, updated_by=EXCLUDED.updated_by, updated_at=now()`,
		clusterID, body, by)
	return err
}

func (p *Postgres) InsertHelixEvent(ctx context.Context, e HelixEvent) (int64, error) {
	body := e.Body
	if len(body) == 0 {
		body = json.RawMessage(`{}`)
	}
	var seq int64
	err := p.DB.QueryRowContext(ctx, `
INSERT INTO helix_events (cluster_id, id, type, severity, machine_id, body, created_at)
VALUES ($1,$2,$3,$4,$5,$6,$7) RETURNING seq`,
		e.ClusterID, e.ID, e.Type, e.Severity, e.MachineID, []byte(body), e.CreatedAt).Scan(&seq)
	return seq, err
}

func (p *Postgres) ListHelixEvents(ctx context.Context, clusterID string, after int64, limit int) ([]HelixEvent, error) {
	rows, err := p.DB.QueryContext(ctx, `
SELECT seq, id, type, severity, machine_id, body, created_at FROM helix_events
WHERE cluster_id=$1 AND seq>$2 ORDER BY seq LIMIT $3`, clusterID, after, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []HelixEvent{}
	for rows.Next() {
		e := HelixEvent{ClusterID: clusterID}
		var body []byte
		if err := rows.Scan(&e.Seq, &e.ID, &e.Type, &e.Severity, &e.MachineID, &body, &e.CreatedAt); err != nil {
			return nil, err
		}
		e.Body = body
		out = append(out, e)
	}
	return out, rows.Err()
}

func (p *Postgres) LatestHelixEventSeq(ctx context.Context, clusterID string) (int64, error) {
	var seq sql.NullInt64
	err := p.DB.QueryRowContext(ctx, `SELECT max(seq) FROM helix_events WHERE cluster_id=$1`, clusterID).Scan(&seq)
	return seq.Int64, err
}

func (p *Postgres) PruneHelixEvents(ctx context.Context, clusterID string, before time.Time) error {
	_, err := p.DB.ExecContext(ctx, `DELETE FROM helix_events WHERE cluster_id=$1 AND created_at<$2`, clusterID, before)
	return err
}
