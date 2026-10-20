package appdb

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

// IPAllowEntry is one address or range a user may sign in from.
type IPAllowEntry struct {
	CIDR    string    `json:"cidr"`
	Note    string    `json:"note,omitempty"`
	AddedBy string    `json:"added_by,omitempty"`
	AddedAt time.Time `json:"added_at"`
}

// AccountProfile is the self-service part of an account: contact emails and
// the account-level IP allowlist. The username stays the identity; the
// display name lives on User and is a UI label only.
type AccountProfile struct {
	UserID          string
	ClusterID       string
	Email           string
	EmailSecondary  string
	EmailVerifiedAt *time.Time
	IPAllowlistOn   bool
	IPAllowlist     []IPAllowEntry
	// Bio and AvatarURL are the social profile other people see. AvatarURL
	// is used when no picture was uploaded.
	Bio       string
	AvatarURL string
	UpdatedAt time.Time
}

// UserAvatar is a profile picture, stored as the uploaded image.
type UserAvatar struct {
	UserID      string
	ClusterID   string
	ContentType string
	Data        []byte
	UpdatedAt   time.Time
}

// Trusted proxy kinds: which header carries the real client address.
const (
	ProxyKindCloudflared = "cloudflared"
	ProxyKindGeneric     = "generic"
)

// TrustedProxy is a proxy whose client-address headers are believed. Manual
// rows are added by an admin; Detected rows only record that an admin turned
// a detected proxy off (ID is the detected key).
type TrustedProxy struct {
	ID        string
	ClusterID string
	CIDR      string
	Kind      string
	Note      string
	Detected  bool
	Disabled  bool
	CreatedAt time.Time
}

// ErrUsernameTaken is returned when a rename collides with another account.
var ErrUsernameTaken = errors.New("that username is already taken")

type accountMem struct {
	mu       sync.Mutex
	profiles map[string]AccountProfile
	avatars  map[string]UserAvatar
	proxies  map[string][]TrustedProxy
}

func copyProfile(p AccountProfile) AccountProfile {
	p.IPAllowlist = append([]IPAllowEntry(nil), p.IPAllowlist...)
	return p
}

func (m *Memory) GetAccountProfile(_ context.Context, userID string) (*AccountProfile, error) {
	m.account.mu.Lock()
	defer m.account.mu.Unlock()
	p, ok := m.account.profiles[userID]
	if !ok {
		return nil, nil
	}
	out := copyProfile(p)
	return &out, nil
}

func (m *Memory) UpsertAccountProfile(_ context.Context, p AccountProfile) error {
	if p.UserID == "" {
		return fmt.Errorf("user id is required")
	}
	m.account.mu.Lock()
	defer m.account.mu.Unlock()
	if m.account.profiles == nil {
		m.account.profiles = map[string]AccountProfile{}
	}
	m.account.profiles[p.UserID] = copyProfile(p)
	return nil
}

func (m *Memory) GetUserAvatar(_ context.Context, userID string) (*UserAvatar, error) {
	m.account.mu.Lock()
	defer m.account.mu.Unlock()
	a, ok := m.account.avatars[userID]
	if !ok {
		return nil, nil
	}
	a.Data = append([]byte(nil), a.Data...)
	return &a, nil
}

func (m *Memory) SetUserAvatar(_ context.Context, a UserAvatar) error {
	m.account.mu.Lock()
	defer m.account.mu.Unlock()
	if m.account.avatars == nil {
		m.account.avatars = map[string]UserAvatar{}
	}
	a.Data = append([]byte(nil), a.Data...)
	m.account.avatars[a.UserID] = a
	return nil
}

func (m *Memory) DeleteUserAvatar(_ context.Context, userID string) error {
	m.account.mu.Lock()
	defer m.account.mu.Unlock()
	delete(m.account.avatars, userID)
	return nil
}

func (m *Memory) RenameUser(_ context.Context, clusterID, userID, username string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	cur, ok := m.users[userID]
	if !ok || cur.ClusterID != clusterID {
		return fmt.Errorf("user not found")
	}
	for id, u := range m.users {
		if id != userID && u.ClusterID == clusterID && strings.EqualFold(u.Username, username) {
			return ErrUsernameTaken
		}
	}
	cur.Username = username
	m.users[userID] = cur
	return nil
}

func (m *Memory) ListTrustedProxies(_ context.Context, clusterID string) ([]TrustedProxy, error) {
	m.account.mu.Lock()
	defer m.account.mu.Unlock()
	return append([]TrustedProxy(nil), m.account.proxies[clusterID]...), nil
}

func (m *Memory) ReplaceTrustedProxies(_ context.Context, clusterID string, items []TrustedProxy) error {
	m.account.mu.Lock()
	defer m.account.mu.Unlock()
	if m.account.proxies == nil {
		m.account.proxies = map[string][]TrustedProxy{}
	}
	m.account.proxies[clusterID] = append([]TrustedProxy(nil), items...)
	return nil
}

func (p *Postgres) GetAccountProfile(ctx context.Context, userID string) (*AccountProfile, error) {
	if !ValidUUID(userID) {
		return nil, nil
	}
	var out AccountProfile
	var verified sql.NullTime
	var list []byte
	err := p.DB.QueryRowContext(ctx, `
SELECT user_id::text, cluster_id::text, email, email_secondary, email_verified_at, ip_allowlist_on, ip_allowlist, bio, avatar_url, updated_at
FROM account_profiles WHERE user_id=$1`, userID).Scan(
		&out.UserID, &out.ClusterID, &out.Email, &out.EmailSecondary, &verified, &out.IPAllowlistOn, &list, &out.Bio, &out.AvatarURL, &out.UpdatedAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	if verified.Valid {
		out.EmailVerifiedAt = &verified.Time
	}
	if len(list) > 0 {
		if err := json.Unmarshal(list, &out.IPAllowlist); err != nil {
			return nil, err
		}
	}
	return &out, nil
}

func (p *Postgres) UpsertAccountProfile(ctx context.Context, a AccountProfile) error {
	list := a.IPAllowlist
	if list == nil {
		list = []IPAllowEntry{}
	}
	raw, err := json.Marshal(list)
	if err != nil {
		return err
	}
	if a.UpdatedAt.IsZero() {
		a.UpdatedAt = time.Now().UTC()
	}
	_, err = p.DB.ExecContext(ctx, `
INSERT INTO account_profiles (user_id, cluster_id, email, email_secondary, email_verified_at, ip_allowlist_on, ip_allowlist, bio, avatar_url, updated_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
ON CONFLICT (user_id) DO UPDATE SET
  email=EXCLUDED.email, email_secondary=EXCLUDED.email_secondary, email_verified_at=EXCLUDED.email_verified_at,
  ip_allowlist_on=EXCLUDED.ip_allowlist_on, ip_allowlist=EXCLUDED.ip_allowlist, bio=EXCLUDED.bio, avatar_url=EXCLUDED.avatar_url,
  updated_at=EXCLUDED.updated_at`,
		a.UserID, a.ClusterID, a.Email, a.EmailSecondary, a.EmailVerifiedAt, a.IPAllowlistOn, raw, a.Bio, a.AvatarURL, a.UpdatedAt)
	return err
}

func (p *Postgres) GetUserAvatar(ctx context.Context, userID string) (*UserAvatar, error) {
	if !ValidUUID(userID) {
		return nil, nil
	}
	var a UserAvatar
	err := p.DB.QueryRowContext(ctx, `
SELECT user_id::text, cluster_id::text, content_type, data, updated_at FROM user_avatars WHERE user_id=$1`, userID).Scan(
		&a.UserID, &a.ClusterID, &a.ContentType, &a.Data, &a.UpdatedAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &a, nil
}

func (p *Postgres) SetUserAvatar(ctx context.Context, a UserAvatar) error {
	if a.UpdatedAt.IsZero() {
		a.UpdatedAt = time.Now().UTC()
	}
	_, err := p.DB.ExecContext(ctx, `
INSERT INTO user_avatars (user_id, cluster_id, content_type, data, updated_at) VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (user_id) DO UPDATE SET content_type=EXCLUDED.content_type, data=EXCLUDED.data, updated_at=EXCLUDED.updated_at`,
		a.UserID, a.ClusterID, a.ContentType, a.Data, a.UpdatedAt)
	return err
}

func (p *Postgres) DeleteUserAvatar(ctx context.Context, userID string) error {
	if !ValidUUID(userID) {
		return nil
	}
	_, err := p.DB.ExecContext(ctx, `DELETE FROM user_avatars WHERE user_id=$1`, userID)
	return err
}

func (p *Postgres) RenameUser(ctx context.Context, clusterID, userID, username string) error {
	var taken bool
	if err := p.DB.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM users WHERE cluster_id=$1 AND lower(username)=lower($2) AND id::text<>$3)`,
		clusterID, username, userID).Scan(&taken); err != nil {
		return err
	}
	if taken {
		return ErrUsernameTaken
	}
	res, err := p.DB.ExecContext(ctx, `UPDATE users SET username=$3 WHERE id=$1 AND cluster_id=$2`, userID, clusterID, username)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("user not found")
	}
	return nil
}

func (p *Postgres) ListTrustedProxies(ctx context.Context, clusterID string) ([]TrustedProxy, error) {
	rows, err := p.DB.QueryContext(ctx, `
SELECT id, cluster_id::text, cidr, kind, note, detected, disabled, created_at
FROM trusted_proxies WHERE cluster_id=$1 ORDER BY created_at, id`, clusterID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []TrustedProxy
	for rows.Next() {
		var t TrustedProxy
		if err := rows.Scan(&t.ID, &t.ClusterID, &t.CIDR, &t.Kind, &t.Note, &t.Detected, &t.Disabled, &t.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (p *Postgres) ReplaceTrustedProxies(ctx context.Context, clusterID string, items []TrustedProxy) error {
	tx, err := p.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `DELETE FROM trusted_proxies WHERE cluster_id=$1`, clusterID); err != nil {
		return err
	}
	for _, t := range items {
		if t.CreatedAt.IsZero() {
			t.CreatedAt = time.Now().UTC()
		}
		if _, err := tx.ExecContext(ctx, `
INSERT INTO trusted_proxies (id, cluster_id, cidr, kind, note, detected, disabled, created_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`, t.ID, clusterID, t.CIDR, t.Kind, t.Note, t.Detected, t.Disabled, t.CreatedAt); err != nil {
			return err
		}
	}
	return tx.Commit()
}
