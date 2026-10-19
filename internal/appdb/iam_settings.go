package appdb

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"sort"
	"sync"
	"time"
)

// AutomationCreators says who may create and edit automation flows: people
// holding MinRole or a role ranked above it, or, when Roles is set, people
// holding one of those roles.
type AutomationCreators struct {
	MinRole string   `json:"min_role,omitempty"`
	Roles   []string `json:"roles,omitempty"`
}

// IAMSettings are cluster-wide identity settings.
type IAMSettings struct {
	ClusterID string
	// RootUserID is the account that set the appliance up. Only it may
	// manage itself; it is changed only from the host.
	RootUserID string
	Creators   AutomationCreators
	// CreatorsLocked is set once a SuperAdmin saves Creators. Admins may
	// still change them if CreatorsAdminsMayEdit.
	CreatorsLocked        bool
	CreatorsAdminsMayEdit bool
	// ApprovalsAccess gives roles approvals.view / approvals.decide.
	ApprovalsAccess map[string][]string
	UpdatedBy       string
	UpdatedAt       time.Time
}

// DefaultIAMSettings are used until settings are saved.
func DefaultIAMSettings(clusterID string) IAMSettings {
	return IAMSettings{ClusterID: clusterID, Creators: AutomationCreators{MinRole: "admin"}, ApprovalsAccess: map[string][]string{}}
}

// Notification is one message for one person.
type Notification struct {
	ID        string
	ClusterID string
	UserID    string
	Kind      string
	Title     string
	Body      string
	Link      string
	CreatedAt time.Time
	ReadAt    *time.Time
}

// NotificationsKept is how many notifications a person keeps; older ones go.
const NotificationsKept = 500

type iamMem struct {
	mu       sync.Mutex
	settings map[string]IAMSettings
	notes    map[string]Notification
}

func copyIAMSettings(s IAMSettings) IAMSettings {
	s.Creators.Roles = append([]string(nil), s.Creators.Roles...)
	access := make(map[string][]string, len(s.ApprovalsAccess))
	for k, v := range s.ApprovalsAccess {
		access[k] = append([]string(nil), v...)
	}
	s.ApprovalsAccess = access
	return s
}

func (m *Memory) GetIAMSettings(_ context.Context, clusterID string) (IAMSettings, error) {
	m.iam.mu.Lock()
	defer m.iam.mu.Unlock()
	if s, ok := m.iam.settings[clusterID]; ok {
		return copyIAMSettings(s), nil
	}
	return DefaultIAMSettings(clusterID), nil
}

func (m *Memory) PutIAMSettings(_ context.Context, s IAMSettings) error {
	m.iam.mu.Lock()
	defer m.iam.mu.Unlock()
	if m.iam.settings == nil {
		m.iam.settings = map[string]IAMSettings{}
	}
	s.UpdatedAt = time.Now().UTC()
	m.iam.settings[s.ClusterID] = copyIAMSettings(s)
	return nil
}

func (m *Memory) CreateNotification(_ context.Context, n Notification) error {
	m.iam.mu.Lock()
	defer m.iam.mu.Unlock()
	if m.iam.notes == nil {
		m.iam.notes = map[string]Notification{}
	}
	if n.CreatedAt.IsZero() {
		n.CreatedAt = time.Now().UTC()
	}
	m.iam.notes[n.ID] = n
	mine := m.notesOf(n.ClusterID, n.UserID)
	for i := NotificationsKept; i < len(mine); i++ {
		delete(m.iam.notes, mine[i].ID)
	}
	return nil
}

// notesOf lists a person's notifications, newest first. Callers hold mu.
func (m *Memory) notesOf(clusterID, userID string) []Notification {
	var out []Notification
	for _, n := range m.iam.notes {
		if n.ClusterID == clusterID && n.UserID == userID {
			out = append(out, n)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].CreatedAt.After(out[j].CreatedAt)
		}
		return out[i].ID > out[j].ID
	})
	return out
}

func (m *Memory) ListNotifications(_ context.Context, clusterID, userID string, unreadOnly bool, limit int) ([]Notification, error) {
	m.iam.mu.Lock()
	defer m.iam.mu.Unlock()
	var out []Notification
	for _, n := range m.notesOf(clusterID, userID) {
		if unreadOnly && n.ReadAt != nil {
			continue
		}
		out = append(out, n)
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out, nil
}

func (m *Memory) CountUnreadNotifications(_ context.Context, clusterID, userID string) (int, error) {
	m.iam.mu.Lock()
	defer m.iam.mu.Unlock()
	n := 0
	for _, note := range m.notesOf(clusterID, userID) {
		if note.ReadAt == nil {
			n++
		}
	}
	return n, nil
}

func (m *Memory) MarkNotificationsRead(_ context.Context, clusterID, userID string, ids []string, at time.Time) error {
	m.iam.mu.Lock()
	defer m.iam.mu.Unlock()
	want := map[string]bool{}
	for _, id := range ids {
		want[id] = true
	}
	for id, n := range m.iam.notes {
		if n.ClusterID != clusterID || n.UserID != userID || n.ReadAt != nil || (len(ids) > 0 && !want[id]) {
			continue
		}
		t := at
		n.ReadAt = &t
		m.iam.notes[id] = n
	}
	return nil
}

func (p *Postgres) GetIAMSettings(ctx context.Context, clusterID string) (IAMSettings, error) {
	out := DefaultIAMSettings(clusterID)
	var creators, access []byte
	var root sql.NullString
	err := p.DB.QueryRowContext(ctx, `
SELECT root_user_id::text, automation_creators, creators_locked, creators_admins_may_edit, approvals_access, updated_by, updated_at
FROM iam_settings WHERE cluster_id=$1`, clusterID).Scan(
		&root, &creators, &out.CreatorsLocked, &out.CreatorsAdminsMayEdit, &access, &out.UpdatedBy, &out.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return out, nil
	}
	if err != nil {
		return out, err
	}
	out.RootUserID = root.String
	if len(creators) > 0 {
		out.Creators = AutomationCreators{}
		if err := json.Unmarshal(creators, &out.Creators); err != nil {
			return out, err
		}
	}
	if len(access) > 0 {
		if err := json.Unmarshal(access, &out.ApprovalsAccess); err != nil {
			return out, err
		}
	}
	if out.ApprovalsAccess == nil {
		out.ApprovalsAccess = map[string][]string{}
	}
	return out, nil
}

func (p *Postgres) PutIAMSettings(ctx context.Context, s IAMSettings) error {
	creators, _ := json.Marshal(s.Creators)
	if s.ApprovalsAccess == nil {
		s.ApprovalsAccess = map[string][]string{}
	}
	access, _ := json.Marshal(s.ApprovalsAccess)
	var root any
	if s.RootUserID != "" {
		root = s.RootUserID
	}
	_, err := p.DB.ExecContext(ctx, `
INSERT INTO iam_settings (cluster_id, root_user_id, automation_creators, creators_locked, creators_admins_may_edit, approvals_access, updated_by, updated_at)
VALUES ($1, $2, $3::jsonb, $4, $5, $6::jsonb, $7, now())
ON CONFLICT (cluster_id) DO UPDATE SET root_user_id=EXCLUDED.root_user_id, automation_creators=EXCLUDED.automation_creators,
    creators_locked=EXCLUDED.creators_locked, creators_admins_may_edit=EXCLUDED.creators_admins_may_edit,
    approvals_access=EXCLUDED.approvals_access, updated_by=EXCLUDED.updated_by, updated_at=now()`,
		s.ClusterID, root, string(creators), s.CreatorsLocked, s.CreatorsAdminsMayEdit, string(access), s.UpdatedBy)
	return err
}

func (p *Postgres) CreateNotification(ctx context.Context, n Notification) error {
	if n.CreatedAt.IsZero() {
		n.CreatedAt = time.Now().UTC()
	}
	if _, err := p.DB.ExecContext(ctx, `
INSERT INTO notifications (id, cluster_id, user_id, kind, title, body, link, created_at) VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`,
		n.ID, n.ClusterID, n.UserID, n.Kind, n.Title, n.Body, n.Link, n.CreatedAt); err != nil {
		return err
	}
	_, err := p.DB.ExecContext(ctx, `
DELETE FROM notifications WHERE user_id=$1 AND id NOT IN (
    SELECT id FROM notifications WHERE user_id=$1 ORDER BY created_at DESC, id DESC LIMIT $2)`, n.UserID, NotificationsKept)
	return err
}

func (p *Postgres) ListNotifications(ctx context.Context, clusterID, userID string, unreadOnly bool, limit int) ([]Notification, error) {
	if !ValidUUID(userID) {
		return nil, nil
	}
	if limit <= 0 || limit > NotificationsKept {
		limit = NotificationsKept
	}
	rows, err := p.DB.QueryContext(ctx, `
SELECT id::text, cluster_id::text, user_id::text, kind, title, body, link, created_at, read_at
FROM notifications WHERE cluster_id=$1 AND user_id=$2 AND (NOT $3 OR read_at IS NULL)
ORDER BY created_at DESC, id DESC LIMIT $4`, clusterID, userID, unreadOnly, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Notification
	for rows.Next() {
		var n Notification
		var read sql.NullTime
		if err := rows.Scan(&n.ID, &n.ClusterID, &n.UserID, &n.Kind, &n.Title, &n.Body, &n.Link, &n.CreatedAt, &read); err != nil {
			return nil, err
		}
		if read.Valid {
			t := read.Time
			n.ReadAt = &t
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

func (p *Postgres) CountUnreadNotifications(ctx context.Context, clusterID, userID string) (int, error) {
	if !ValidUUID(userID) {
		return 0, nil
	}
	var n int
	err := p.DB.QueryRowContext(ctx, `SELECT count(*) FROM notifications WHERE cluster_id=$1 AND user_id=$2 AND read_at IS NULL`, clusterID, userID).Scan(&n)
	return n, err
}

func (p *Postgres) MarkNotificationsRead(ctx context.Context, clusterID, userID string, ids []string, at time.Time) error {
	if !ValidUUID(userID) {
		return nil
	}
	if len(ids) == 0 {
		_, err := p.DB.ExecContext(ctx, `UPDATE notifications SET read_at=$3 WHERE cluster_id=$1 AND user_id=$2 AND read_at IS NULL`, clusterID, userID, at)
		return err
	}
	valid := make([]string, 0, len(ids))
	for _, id := range ids {
		if ValidUUID(id) {
			valid = append(valid, id)
		}
	}
	if len(valid) == 0 {
		return nil
	}
	list, _ := json.Marshal(valid)
	_, err := p.DB.ExecContext(ctx, `
UPDATE notifications SET read_at=$3 WHERE cluster_id=$1 AND user_id=$2 AND read_at IS NULL
  AND id::text IN (SELECT jsonb_array_elements_text($4::jsonb))`, clusterID, userID, at, string(list))
	return err
}
