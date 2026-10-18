package appdb

import (
	"context"
	"errors"
	"sort"
	"time"
)

// API tokens are hard-deleted when they are deleted or expire; nothing keeps
// a list of dead tokens. Their history (creation, usage per source IP, end)
// lives in the audit log, written before the row goes.

// TokenUsage is one source IP that used a token.
type TokenUsage struct {
	IP      string
	FirstAt time.Time
	LastAt  time.Time
	Count   int
}

func (p *Postgres) ListExpiredTokens(ctx context.Context, now time.Time) ([]APIToken, error) {
	rows, err := p.DB.QueryContext(ctx, apiTokenSelect+` WHERE expires_at IS NOT NULL AND expires_at <= $1`, now)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []APIToken
	for rows.Next() {
		t, err := scanAPIToken(rows)
		if err != nil {
			return nil, err
		}
		if t != nil {
			out = append(out, *t)
		}
	}
	return out, rows.Err()
}

func (p *Postgres) TouchTokenUsage(ctx context.Context, tokenID, ip string, at time.Time) error {
	_, err := p.DB.ExecContext(ctx, `INSERT INTO api_token_usage (token_id, ip, first_at, last_at, count) VALUES ($1,$2,$3,$3,1)
		ON CONFLICT (token_id, ip) DO UPDATE SET last_at=EXCLUDED.last_at, count=api_token_usage.count+1`, tokenID, ip, at)
	return err
}

func (p *Postgres) ListTokenUsage(ctx context.Context, tokenID string) ([]TokenUsage, error) {
	rows, err := p.DB.QueryContext(ctx, `SELECT ip, first_at, last_at, count FROM api_token_usage WHERE token_id=$1 ORDER BY last_at DESC`, tokenID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []TokenUsage
	for rows.Next() {
		var u TokenUsage
		if err := rows.Scan(&u.IP, &u.FirstAt, &u.LastAt, &u.Count); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

func (p *Postgres) RollToken(ctx context.Context, clusterID, id, hash, prefix string) error {
	res, err := p.DB.ExecContext(ctx, `UPDATE api_tokens SET token_hash=$3, prefix=$4 WHERE id=$1 AND cluster_id=$2`, id, clusterID, hash, prefix)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return errors.New("token not found")
	}
	return nil
}

func (p *Postgres) UpdateToken(ctx context.Context, clusterID, id, name string, expires *time.Time) error {
	res, err := p.DB.ExecContext(ctx, `UPDATE api_tokens SET name=COALESCE(NULLIF($3, ''), name), expires_at=$4 WHERE id=$1 AND cluster_id=$2`, id, clusterID, name, expires)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return errors.New("token not found")
	}
	return nil
}

func (m *Memory) ListExpiredTokens(_ context.Context, now time.Time) ([]APIToken, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []APIToken
	for _, t := range m.tokens {
		if t.ExpiresAt != nil && !t.ExpiresAt.After(now) {
			out = append(out, t)
		}
	}
	return out, nil
}

func (m *Memory) TouchTokenUsage(_ context.Context, tokenID, ip string, at time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.tokenUsage == nil {
		m.tokenUsage = map[string]map[string]*TokenUsage{}
	}
	byIP := m.tokenUsage[tokenID]
	if byIP == nil {
		byIP = map[string]*TokenUsage{}
		m.tokenUsage[tokenID] = byIP
	}
	u := byIP[ip]
	if u == nil {
		u = &TokenUsage{IP: ip, FirstAt: at}
		byIP[ip] = u
	}
	u.LastAt = at
	u.Count++
	return nil
}

func (m *Memory) ListTokenUsage(_ context.Context, tokenID string) ([]TokenUsage, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []TokenUsage
	for _, u := range m.tokenUsage[tokenID] {
		out = append(out, *u)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].LastAt.After(out[j].LastAt) })
	return out, nil
}

func (m *Memory) RollToken(_ context.Context, clusterID, id, hash, prefix string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for k, t := range m.tokens {
		if t.ID == id && t.ClusterID == clusterID {
			delete(m.tokens, k)
			t.TokenHash, t.Prefix = hash, prefix
			m.tokens[hash] = t
			return nil
		}
	}
	return errors.New("token not found")
}

func (m *Memory) UpdateToken(_ context.Context, clusterID, id, name string, expires *time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for k, t := range m.tokens {
		if t.ID == id && t.ClusterID == clusterID {
			if name != "" {
				t.Name = name
			}
			t.ExpiresAt = expires
			m.tokens[k] = t
			return nil
		}
	}
	return errors.New("token not found")
}
