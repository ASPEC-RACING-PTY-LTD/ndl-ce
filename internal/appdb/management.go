package appdb

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

func (p *Postgres) ListUsers(ctx context.Context, clusterID string) ([]User, error) {
	rows, err := p.DB.QueryContext(ctx, userSelect+` WHERE cluster_id=$1 ORDER BY username`, clusterID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []User
	for rows.Next() {
		var u User
		var disabled, lastLogin sql.NullTime
		if err := rows.Scan(&u.ID, &u.ClusterID, &u.Username, &u.PasswordHash, &u.Kind, &u.DisplayName, &u.CreatedAt, &disabled, &u.MFARequired, &lastLogin); err != nil {
			return nil, err
		}
		u.PasswordHash = ""
		if u.Kind == "" {
			u.Kind = UserKindPerson
		}
		if disabled.Valid {
			u.DisabledAt = &disabled.Time
		}
		if lastLogin.Valid {
			u.LastLoginAt = &lastLogin.Time
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

func (p *Postgres) UpdateUser(ctx context.Context, u User) error {
	_, err := p.DB.ExecContext(ctx, `
UPDATE users
SET display_name=$2, disabled_at=$3, mfa_required=$4
WHERE id=$1 AND cluster_id=$5`, u.ID, u.DisplayName, u.DisabledAt, u.MFARequired, u.ClusterID)
	return err
}

func (p *Postgres) DeleteUser(ctx context.Context, clusterID, userID string) error {
	res, err := p.DB.ExecContext(ctx, `DELETE FROM users WHERE id=$1 AND cluster_id=$2`, userID, clusterID)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return errors.New("user not found")
	}
	return nil
}

func (p *Postgres) TouchLastLogin(ctx context.Context, userID string, at time.Time) error {
	_, err := p.DB.ExecContext(ctx, `UPDATE users SET last_login_at=$2 WHERE id=$1`, userID, at)
	return err
}

func (p *Postgres) RevokeUserTokens(ctx context.Context, userID string) error {
	_, err := p.DB.ExecContext(ctx, `UPDATE api_tokens SET revoked_at=now() WHERE user_id=$1 AND revoked_at IS NULL`, userID)
	return err
}

func (p *Postgres) TouchTokenLastUsed(ctx context.Context, tokenID string, at time.Time) error {
	_, err := p.DB.ExecContext(ctx, `UPDATE api_tokens SET last_used_at=$2 WHERE id=$1`, tokenID, at)
	return err
}

func (p *Postgres) SetClusterMFARequired(ctx context.Context, clusterID string, required bool) error {
	_, err := p.DB.ExecContext(ctx, `UPDATE clusters SET mfa_required=$2 WHERE id=$1`, clusterID, required)
	return err
}

func (m *Memory) ListUsers(_ context.Context, clusterID string) ([]User, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]User, 0)
	for _, u := range m.users {
		if u.ClusterID != clusterID {
			continue
		}
		cp := u
		cp.PasswordHash = ""
		out = append(out, cp)
	}
	return out, nil
}

func (m *Memory) UpdateUser(_ context.Context, u User) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	cur, ok := m.users[u.ID]
	if !ok || cur.ClusterID != u.ClusterID {
		return fmt.Errorf("user not found")
	}
	cur.DisplayName = u.DisplayName
	cur.DisabledAt = u.DisabledAt
	cur.MFARequired = u.MFARequired
	m.users[u.ID] = cur
	return nil
}

func (m *Memory) DeleteUser(_ context.Context, clusterID, userID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	u, ok := m.users[userID]
	if !ok || u.ClusterID != clusterID {
		return fmt.Errorf("user not found")
	}
	delete(m.users, userID)
	delete(m.binds, userID)
	delete(m.userPrefs, userID)
	delete(m.mfaMethods, userID)
	delete(m.mfaSecrets, userID)
	delete(m.mfaRecovery, userID)
	for k, s := range m.sess {
		if s.UserID == userID {
			delete(m.sess, k)
		}
	}
	for k, t := range m.tokens {
		if t.UserID == userID {
			delete(m.tokens, k)
		}
	}
	for gid, members := range m.groupMembers {
		kept := members[:0]
		for _, id := range members {
			if id != userID {
				kept = append(kept, id)
			}
		}
		m.groupMembers[gid] = kept
	}
	for id, sp := range m.servicePrincipals {
		if sp.UserID == userID {
			delete(m.servicePrincipals, id)
		}
	}
	return nil
}

func (m *Memory) TouchLastLogin(_ context.Context, userID string, at time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	u, ok := m.users[userID]
	if !ok {
		return fmt.Errorf("user not found")
	}
	u.LastLoginAt = &at
	m.users[userID] = u
	return nil
}

func (m *Memory) CountEnabledAdmins(_ context.Context, clusterID string) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for uid, roles := range m.binds {
		u, ok := m.users[uid]
		if !ok || u.ClusterID != clusterID || u.DisabledAt != nil {
			continue
		}
		for _, r := range roles {
			if r == "admin" {
				n++
				break
			}
		}
	}
	return n, nil
}

func (m *Memory) RevokeUserTokens(_ context.Context, userID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now().UTC()
	for k, t := range m.tokens {
		if t.UserID == userID && t.RevokedAt == nil {
			t.RevokedAt = &now
			m.tokens[k] = t
		}
	}
	return nil
}

func (m *Memory) TouchTokenLastUsed(_ context.Context, tokenID string, at time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for k, t := range m.tokens {
		if t.ID == tokenID {
			t.LastUsedAt = &at
			m.tokens[k] = t
			return nil
		}
	}
	return nil
}

func (m *Memory) SetClusterMFARequired(_ context.Context, clusterID string, required bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.cluster == nil || m.cluster.ID != clusterID {
		return fmt.Errorf("cluster not found")
	}
	m.cluster.MFARequired = required
	return nil
}
