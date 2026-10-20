package appdb

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// UISettings are cluster-wide appearance and search settings. Both are
// JSON documents shaped and validated by the HTTP layer.
type UISettings struct {
	ClusterID  string
	Appearance json.RawMessage
	Search     json.RawMessage
	UpdatedBy  string
	UpdatedAt  time.Time
}

// Search scopes: primary covers names and basic details of everything and
// is always on; deep covers bulky sources such as file names.
const (
	SearchScopePrimary = "primary"
	SearchScopeDeep    = "deep"
)

// SearchDoc is one searchable thing. Permission is the grant a reader
// needs to see it; Link is where the result opens.
type SearchDoc struct {
	ClusterID  string
	Kind       string
	RefID      string
	Scope      string
	Title      string
	Subtitle   string
	Body       string
	Link       string
	NodeID     string
	Status     string
	Permission string
	UpdatedAt  time.Time
	// Rank is set by SearchDocs.
	Rank float64
}

// SearchQuery asks for documents matching Text, optionally limited to some
// kinds and to the grants the reader holds (nil Allowed allows all).
type SearchQuery struct {
	ClusterID string
	Text      string
	Kinds     []string
	Status    string
	NodeID    string
	Allowed   func(permission string) bool
	Limit     int
}

// Search job statuses.
const (
	SearchJobRunning  = "running"
	SearchJobDone     = "done"
	SearchJobFailed   = "failed"
	SearchJobCanceled = "canceled"
)

// SearchJob is one indexing run. Cursor is where it carries on after a
// restart.
type SearchJob struct {
	ID         string
	ClusterID  string
	Scope      string
	Status     string
	Total      int64
	Done       int64
	Cursor     json.RawMessage
	Error      string
	StartedBy  string
	StartedAt  time.Time
	UpdatedAt  time.Time
	FinishedAt *time.Time
}

type uiMem struct {
	mu        sync.Mutex
	settings  map[string]UISettings
	delivered map[string]bool
	wiped     map[string]time.Time
	docs      map[string]SearchDoc
	jobs      map[string]SearchJob
}

func docKey(d SearchDoc) string { return d.ClusterID + "\x00" + d.Kind + "\x00" + d.RefID }

func (m *Memory) GetUISettings(_ context.Context, clusterID string) (UISettings, error) {
	m.ui.mu.Lock()
	defer m.ui.mu.Unlock()
	if s, ok := m.ui.settings[clusterID]; ok {
		return s, nil
	}
	return UISettings{ClusterID: clusterID, Appearance: json.RawMessage(`{}`), Search: json.RawMessage(`{}`)}, nil
}

func (m *Memory) PutUISettings(_ context.Context, s UISettings) error {
	m.ui.mu.Lock()
	defer m.ui.mu.Unlock()
	if m.ui.settings == nil {
		m.ui.settings = map[string]UISettings{}
	}
	s.UpdatedAt = time.Now().UTC()
	s.Appearance = append(json.RawMessage(nil), s.Appearance...)
	s.Search = append(json.RawMessage(nil), s.Search...)
	m.ui.settings[s.ClusterID] = s
	return nil
}

func (m *Memory) UpdateNoticeDelivered(_ context.Context, clusterID, noticeID string) (bool, error) {
	m.ui.mu.Lock()
	defer m.ui.mu.Unlock()
	return m.ui.delivered[clusterID+"\x00"+noticeID], nil
}

func (m *Memory) MarkUpdateNoticeDelivered(_ context.Context, clusterID, noticeID string) error {
	m.ui.mu.Lock()
	defer m.ui.mu.Unlock()
	if m.ui.delivered == nil {
		m.ui.delivered = map[string]bool{}
	}
	m.ui.delivered[clusterID+"\x00"+noticeID] = true
	return nil
}

func (m *Memory) CheckpointWiped(_ context.Context, opID string) (*time.Time, error) {
	m.ui.mu.Lock()
	defer m.ui.mu.Unlock()
	if at, ok := m.ui.wiped[opID]; ok {
		return &at, nil
	}
	return nil, nil
}

func (m *Memory) MarkCheckpointWiped(_ context.Context, opID, _ string, at time.Time) error {
	m.ui.mu.Lock()
	defer m.ui.mu.Unlock()
	if m.ui.wiped == nil {
		m.ui.wiped = map[string]time.Time{}
	}
	m.ui.wiped[opID] = at
	return nil
}

func (m *Memory) UpsertSearchDocs(_ context.Context, docs []SearchDoc) error {
	m.ui.mu.Lock()
	defer m.ui.mu.Unlock()
	if m.ui.docs == nil {
		m.ui.docs = map[string]SearchDoc{}
	}
	for _, d := range docs {
		if d.UpdatedAt.IsZero() {
			d.UpdatedAt = time.Now().UTC()
		}
		if d.Scope == "" {
			d.Scope = SearchScopePrimary
		}
		m.ui.docs[docKey(d)] = d
	}
	return nil
}

func (m *Memory) DeleteStaleSearchDocs(_ context.Context, clusterID, scope string, kinds []string, before time.Time) error {
	m.ui.mu.Lock()
	defer m.ui.mu.Unlock()
	for k, d := range m.ui.docs {
		if d.ClusterID == clusterID && d.Scope == scope && (len(kinds) == 0 || containsStr(kinds, d.Kind)) && d.UpdatedAt.Before(before) {
			delete(m.ui.docs, k)
		}
	}
	return nil
}

func (m *Memory) DeleteSearchDocs(_ context.Context, clusterID, scope string) error {
	m.ui.mu.Lock()
	defer m.ui.mu.Unlock()
	for k, d := range m.ui.docs {
		if d.ClusterID == clusterID && d.Scope == scope {
			delete(m.ui.docs, k)
		}
	}
	return nil
}

func containsStr(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

// memRank scores a document: a title prefix beats a title match, which
// beats a match in the other fields.
func memRank(d SearchDoc, words []string) float64 {
	title := strings.ToLower(d.Title)
	other := strings.ToLower(d.Subtitle + " " + d.Body + " " + d.Kind)
	score := 0.0
	for _, w := range words {
		switch {
		case strings.HasPrefix(title, w):
			score += 3
		case strings.Contains(title, w):
			score += 2
		case strings.Contains(other, w):
			score += 1
		default:
			return 0
		}
	}
	return score
}

func (m *Memory) SearchDocs(_ context.Context, q SearchQuery) ([]SearchDoc, error) {
	words := strings.Fields(strings.ToLower(q.Text))
	m.ui.mu.Lock()
	var out []SearchDoc
	for _, d := range m.ui.docs {
		if d.ClusterID != q.ClusterID || (len(q.Kinds) > 0 && !containsStr(q.Kinds, d.Kind)) {
			continue
		}
		if (q.Status != "" && !strings.EqualFold(d.Status, q.Status)) || (q.NodeID != "" && d.NodeID != q.NodeID) {
			continue
		}
		r := 1.0
		if len(words) > 0 {
			if r = memRank(d, words); r == 0 {
				continue
			}
		}
		d.Rank = r
		out = append(out, d)
	}
	m.ui.mu.Unlock()
	return finishSearch(out, q), nil
}

// finishSearch drops what the reader may not see, orders by rank then
// title, and applies the limit.
func finishSearch(in []SearchDoc, q SearchQuery) []SearchDoc {
	out := in[:0]
	for _, d := range in {
		if q.Allowed == nil || d.Permission == "" || q.Allowed(d.Permission) {
			out = append(out, d)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Rank != out[j].Rank {
			return out[i].Rank > out[j].Rank
		}
		return strings.ToLower(out[i].Title) < strings.ToLower(out[j].Title)
	})
	limit := q.Limit
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	if len(out) > limit {
		out = out[:limit]
	}
	return out
}

func (m *Memory) CountSearchDocs(_ context.Context, clusterID string) (map[string]int, error) {
	m.ui.mu.Lock()
	defer m.ui.mu.Unlock()
	out := map[string]int{}
	for _, d := range m.ui.docs {
		if d.ClusterID == clusterID {
			out[d.Scope]++
		}
	}
	return out, nil
}

func (m *Memory) CreateSearchJob(_ context.Context, j SearchJob) error {
	m.ui.mu.Lock()
	defer m.ui.mu.Unlock()
	if m.ui.jobs == nil {
		m.ui.jobs = map[string]SearchJob{}
	}
	if j.StartedAt.IsZero() {
		j.StartedAt = time.Now().UTC()
	}
	j.UpdatedAt = j.StartedAt
	m.ui.jobs[j.ID] = j
	return nil
}

func (m *Memory) UpdateSearchJob(_ context.Context, j SearchJob) error {
	m.ui.mu.Lock()
	defer m.ui.mu.Unlock()
	if _, ok := m.ui.jobs[j.ID]; !ok {
		return sql.ErrNoRows
	}
	j.UpdatedAt = time.Now().UTC()
	m.ui.jobs[j.ID] = j
	return nil
}

func (m *Memory) LatestSearchJob(_ context.Context, clusterID string) (*SearchJob, error) {
	m.ui.mu.Lock()
	defer m.ui.mu.Unlock()
	var best *SearchJob
	for _, j := range m.ui.jobs {
		if j.ClusterID != clusterID {
			continue
		}
		if best == nil || j.StartedAt.After(best.StartedAt) {
			jj := j
			best = &jj
		}
	}
	return best, nil
}

// ---- Postgres ----

func (p *Postgres) GetUISettings(ctx context.Context, clusterID string) (UISettings, error) {
	out := UISettings{ClusterID: clusterID, Appearance: json.RawMessage(`{}`), Search: json.RawMessage(`{}`)}
	if !ValidUUID(clusterID) {
		return out, nil
	}
	var appearance, search []byte
	err := p.DB.QueryRowContext(ctx, `SELECT appearance::text, search::text, updated_by, updated_at FROM ui_settings WHERE cluster_id=$1`, clusterID).
		Scan(&appearance, &search, &out.UpdatedBy, &out.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return out, nil
	}
	if err != nil {
		return out, err
	}
	out.Appearance, out.Search = appearance, search
	return out, nil
}

func (p *Postgres) PutUISettings(ctx context.Context, s UISettings) error {
	if len(s.Appearance) == 0 {
		s.Appearance = json.RawMessage(`{}`)
	}
	if len(s.Search) == 0 {
		s.Search = json.RawMessage(`{}`)
	}
	_, err := p.DB.ExecContext(ctx, `
INSERT INTO ui_settings (cluster_id, appearance, search, updated_by, updated_at)
VALUES ($1, $2::jsonb, $3::jsonb, $4, now())
ON CONFLICT (cluster_id) DO UPDATE SET appearance=EXCLUDED.appearance, search=EXCLUDED.search,
  updated_by=EXCLUDED.updated_by, updated_at=now()`, s.ClusterID, string(s.Appearance), string(s.Search), s.UpdatedBy)
	return err
}

func (p *Postgres) UpdateNoticeDelivered(ctx context.Context, clusterID, noticeID string) (bool, error) {
	var n int
	err := p.DB.QueryRowContext(ctx, `SELECT count(*) FROM update_notices_delivered WHERE cluster_id=$1 AND notice_id=$2`, clusterID, noticeID).Scan(&n)
	return n > 0, err
}

func (p *Postgres) MarkUpdateNoticeDelivered(ctx context.Context, clusterID, noticeID string) error {
	_, err := p.DB.ExecContext(ctx, `INSERT INTO update_notices_delivered (cluster_id, notice_id) VALUES ($1, $2) ON CONFLICT DO NOTHING`, clusterID, noticeID)
	return err
}

func (p *Postgres) CheckpointWiped(ctx context.Context, opID string) (*time.Time, error) {
	if !ValidUUID(opID) {
		return nil, nil
	}
	var at sql.NullTime
	err := p.DB.QueryRowContext(ctx, `SELECT checkpoint_wiped_at FROM update_operations WHERE id=$1`, opID).Scan(&at)
	if errors.Is(err, sql.ErrNoRows) || !at.Valid {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &at.Time, nil
}

func (p *Postgres) MarkCheckpointWiped(ctx context.Context, opID, by string, at time.Time) error {
	_, err := p.DB.ExecContext(ctx, `UPDATE update_operations SET checkpoint_wiped_at=$2, checkpoint_wiped_by=$3 WHERE id=$1 AND checkpoint_wiped_at IS NULL`, opID, at, by)
	return err
}

func (p *Postgres) UpsertSearchDocs(ctx context.Context, docs []SearchDoc) error {
	if len(docs) == 0 {
		return nil
	}
	tx, err := p.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	stmt, err := tx.PrepareContext(ctx, `
INSERT INTO search_documents (cluster_id, kind, ref_id, scope, title, subtitle, body, link, node_id, status, permission, updated_at)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)
ON CONFLICT (cluster_id, kind, ref_id) DO UPDATE SET scope=EXCLUDED.scope, title=EXCLUDED.title, subtitle=EXCLUDED.subtitle,
  body=EXCLUDED.body, link=EXCLUDED.link, node_id=EXCLUDED.node_id, status=EXCLUDED.status, permission=EXCLUDED.permission,
  updated_at=EXCLUDED.updated_at`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, d := range docs {
		if d.UpdatedAt.IsZero() {
			d.UpdatedAt = time.Now().UTC()
		}
		if d.Scope == "" {
			d.Scope = SearchScopePrimary
		}
		if _, err := stmt.ExecContext(ctx, d.ClusterID, d.Kind, d.RefID, d.Scope, d.Title, d.Subtitle, d.Body, d.Link, d.NodeID, d.Status, d.Permission, d.UpdatedAt); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (p *Postgres) DeleteStaleSearchDocs(ctx context.Context, clusterID, scope string, kinds []string, before time.Time) error {
	q := `DELETE FROM search_documents WHERE cluster_id=$1 AND scope=$2 AND updated_at < $3`
	args := []any{clusterID, scope, before}
	if len(kinds) > 0 {
		q += ` AND kind = ANY($4)`
		args = append(args, kinds)
	}
	_, err := p.DB.ExecContext(ctx, q, args...)
	return err
}

func (p *Postgres) DeleteSearchDocs(ctx context.Context, clusterID, scope string) error {
	_, err := p.DB.ExecContext(ctx, `DELETE FROM search_documents WHERE cluster_id=$1 AND scope=$2`, clusterID, scope)
	return err
}

// likeEscape escapes LIKE wildcards in user text.
func likeEscape(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}

func (p *Postgres) SearchDocs(ctx context.Context, q SearchQuery) ([]SearchDoc, error) {
	text := strings.TrimSpace(q.Text)
	args := []any{q.ClusterID}
	where := []string{"cluster_id=$1"}
	rank := "1"
	if text != "" {
		// Every word must match: as a full-text prefix or inside the title.
		var terms []string
		for _, w := range strings.Fields(strings.ToLower(text)) {
			clean := strings.Map(func(r rune) rune {
				if r == '\'' || r == ':' || r == '&' || r == '|' || r == '!' || r == '(' || r == ')' || r == '\\' || r == '*' || r == '<' {
					return -1
				}
				return r
			}, w)
			if clean != "" {
				terms = append(terms, clean)
			}
		}
		if len(terms) > 0 {
			parts := make([]string, len(terms))
			for i, t := range terms {
				parts[i] = "'" + t + "':*"
			}
			args = append(args, strings.Join(parts, " & "), "%"+likeEscape(strings.ToLower(text))+"%")
			where = append(where, `(tsv @@ to_tsquery('simple', $2) OR lower(title) LIKE $3 OR lower(subtitle) LIKE $3)`)
			rank = `ts_rank(tsv, to_tsquery('simple', $2)) + CASE WHEN lower(title) LIKE $3 THEN 1 ELSE 0 END`
		}
	}
	if len(q.Kinds) > 0 {
		args = append(args, q.Kinds)
		where = append(where, "kind = ANY($"+strconv.Itoa(len(args))+")")
	}
	if q.Status != "" {
		args = append(args, strings.ToLower(q.Status))
		where = append(where, "lower(status) = $"+strconv.Itoa(len(args)))
	}
	if q.NodeID != "" {
		args = append(args, q.NodeID)
		where = append(where, "node_id = $"+strconv.Itoa(len(args)))
	}
	// Read a few more than asked: some rows may be dropped for permissions.
	limit := q.Limit
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	args = append(args, limit*4)
	rows, err := p.DB.QueryContext(ctx, `
SELECT cluster_id::text, kind, ref_id, scope, title, subtitle, body, link, node_id, status, permission, updated_at, `+rank+`
FROM search_documents WHERE `+strings.Join(where, " AND ")+`
ORDER BY 13 DESC, lower(title) LIMIT $`+strconv.Itoa(len(args)), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SearchDoc
	for rows.Next() {
		var d SearchDoc
		if err := rows.Scan(&d.ClusterID, &d.Kind, &d.RefID, &d.Scope, &d.Title, &d.Subtitle, &d.Body, &d.Link, &d.NodeID, &d.Status, &d.Permission, &d.UpdatedAt, &d.Rank); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return finishSearch(out, q), nil
}

func (p *Postgres) CountSearchDocs(ctx context.Context, clusterID string) (map[string]int, error) {
	rows, err := p.DB.QueryContext(ctx, `SELECT scope, count(*) FROM search_documents WHERE cluster_id=$1 GROUP BY scope`, clusterID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var scope string
		var n int
		if err := rows.Scan(&scope, &n); err != nil {
			return nil, err
		}
		out[scope] = n
	}
	return out, rows.Err()
}

func (p *Postgres) CreateSearchJob(ctx context.Context, j SearchJob) error {
	if len(j.Cursor) == 0 {
		j.Cursor = json.RawMessage(`{}`)
	}
	if j.StartedAt.IsZero() {
		j.StartedAt = time.Now().UTC()
	}
	_, err := p.DB.ExecContext(ctx, `
INSERT INTO search_jobs (id, cluster_id, scope, status, total, done, cursor, error, started_by, started_at, updated_at)
VALUES ($1,$2,$3,$4,$5,$6,$7::jsonb,$8,$9,$10,$10)`, j.ID, j.ClusterID, j.Scope, j.Status, j.Total, j.Done, string(j.Cursor), j.Error, j.StartedBy, j.StartedAt)
	return err
}

func (p *Postgres) UpdateSearchJob(ctx context.Context, j SearchJob) error {
	if len(j.Cursor) == 0 {
		j.Cursor = json.RawMessage(`{}`)
	}
	_, err := p.DB.ExecContext(ctx, `
UPDATE search_jobs SET status=$2, total=$3, done=$4, cursor=$5::jsonb, error=$6, updated_at=now(), finished_at=$7 WHERE id=$1`,
		j.ID, j.Status, j.Total, j.Done, string(j.Cursor), j.Error, j.FinishedAt)
	return err
}

func (p *Postgres) LatestSearchJob(ctx context.Context, clusterID string) (*SearchJob, error) {
	var j SearchJob
	var cursor []byte
	var finished sql.NullTime
	err := p.DB.QueryRowContext(ctx, `
SELECT id::text, cluster_id::text, scope, status, total, done, cursor::text, error, started_by, started_at, updated_at, finished_at
FROM search_jobs WHERE cluster_id=$1 ORDER BY started_at DESC LIMIT 1`, clusterID).
		Scan(&j.ID, &j.ClusterID, &j.Scope, &j.Status, &j.Total, &j.Done, &cursor, &j.Error, &j.StartedBy, &j.StartedAt, &j.UpdatedAt, &finished)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	j.Cursor = cursor
	if finished.Valid {
		j.FinishedAt = &finished.Time
	}
	return &j, nil
}
