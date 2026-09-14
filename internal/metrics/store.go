package metrics

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

// Store is a restart-safe, bounded SQLite time series on the agent.
type Store struct {
	db        *sql.DB
	mu        sync.Mutex
	maxRows   int
	retention time.Duration
}

const schema = `
CREATE TABLE IF NOT EXISTS samples (
	name TEXT NOT NULL,
	ts INTEGER NOT NULL,
	value REAL NOT NULL,
	PRIMARY KEY (name, ts)
);
CREATE INDEX IF NOT EXISTS samples_ts ON samples(ts);
CREATE TABLE IF NOT EXISTS samples_1h (
	name TEXT NOT NULL,
	bucket INTEGER NOT NULL,
	sum REAL NOT NULL,
	min REAL NOT NULL,
	max REAL NOT NULL,
	count INTEGER NOT NULL,
	PRIMARY KEY (name, bucket)
);
CREATE INDEX IF NOT EXISTS samples_1h_bucket ON samples_1h(bucket);
`

// Open creates or reopens an agent metrics database at path.
func Open(path string) (*Store, error) {
	if strings.TrimSpace(path) == "" {
		return nil, errors.New("metrics: empty path")
	}
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o750); err != nil {
			return nil, fmt.Errorf("metrics: mkdir: %w", err)
		}
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("metrics: open: %w", err)
	}
	db.SetMaxOpenConns(1)
	for _, pragma := range []string{
		"PRAGMA busy_timeout = 5000",
		"PRAGMA journal_mode = WAL",
		"PRAGMA synchronous = NORMAL",
		"PRAGMA foreign_keys = ON",
	} {
		if _, err := db.Exec(pragma); err != nil {
			db.Close()
			return nil, fmt.Errorf("metrics: %s: %w", pragma, err)
		}
	}
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("metrics: schema: %w", err)
	}
	return &Store{db: db, maxRows: DefaultMaxRows, retention: Retention}, nil
}

// Close releases the database file.
func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	_, _ = s.db.Exec("PRAGMA wal_checkpoint(TRUNCATE)")
	err := s.db.Close()
	s.db = nil
	return err
}

// Record inserts one real sample and prunes expired rows.
func (s *Store) Record(name string, ts time.Time, value float64) error {
	if s == nil || s.db == nil {
		return errors.New("metrics: store closed")
	}
	if name == "" {
		return errors.New("metrics: empty name")
	}
	ts = ts.UTC()
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("metrics: begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.Exec(
		`INSERT OR REPLACE INTO samples (name, ts, value) VALUES (?, ?, ?)`,
		name, ts.UnixMicro(), value,
	); err != nil {
		return fmt.Errorf("metrics: insert: %w", err)
	}
	bucket := ts.Truncate(time.Hour).Unix()
	if _, err := tx.Exec(
		`INSERT INTO samples_1h (name, bucket, sum, min, max, count) VALUES (?, ?, ?, ?, ?, 1)
		 ON CONFLICT(name, bucket) DO UPDATE SET
			sum = samples_1h.sum + excluded.sum,
			min = MIN(samples_1h.min, excluded.min),
			max = MAX(samples_1h.max, excluded.max),
			count = samples_1h.count + 1`,
		name, bucket, value, value, value,
	); err != nil {
		return fmt.Errorf("metrics: downsample: %w", err)
	}
	if err := pruneTx(tx, time.Now().UTC(), s.retention, s.maxRows); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("metrics: commit: %w", err)
	}
	return nil
}

// Query returns only stored rows in [from, to]. It never invents points.
func (s *Store) Query(names []string, from, to time.Time) (QueryResult, error) {
	empty := QueryResult{Status: StatusCollecting, Series: []Series{}}
	if s == nil || s.db == nil {
		return QueryResult{Status: StatusUnavailable, Series: []Series{}}, errors.New("metrics: store closed")
	}
	from = from.UTC()
	to = to.UTC()
	if to.IsZero() {
		to = time.Now().UTC()
	}
	if !from.IsZero() && from.After(to) {
		return emptyWithNames(names), nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	useHourly := !from.IsZero() && to.Sub(from) > DownsampleAfter
	byName, order, err := s.queryLocked(names, from, to, useHourly)
	if err != nil {
		return empty, err
	}

	now := time.Now().UTC()
	var series []Series
	if len(names) == 0 {
		if len(order) == 0 {
			return QueryResult{Status: StatusCollecting, Series: []Series{}}, nil
		}
		for _, name := range order {
			series = append(series, makeSeries(name, byName[name], to, now))
		}
	} else {
		series = make([]Series, 0, len(names))
		for _, name := range names {
			pts := byName[name]
			if pts == nil {
				pts = []Point{}
			}
			series = append(series, makeSeries(name, pts, to, now))
		}
	}
	return QueryResult{Status: rollup(series), Series: series}, nil
}

// QueryWindow returns recorded series plus KnownNames padded as collecting.
func (s *Store) QueryWindow(from, to time.Time) (QueryResult, error) {
	res, err := s.Query(nil, from, to)
	if err != nil {
		return res, err
	}
	have := map[string]bool{}
	for _, ser := range res.Series {
		have[ser.Name] = true
	}
	for _, name := range KnownNames {
		if have[name] {
			continue
		}
		res.Series = append(res.Series, Series{
			Name: name, Status: StatusCollecting, Unit: unitFor(name), Points: []Point{},
		})
	}
	if len(res.Series) == 0 {
		return QueryResult{Status: StatusCollecting, Series: []Series{}}, nil
	}
	res.Status = rollup(res.Series)
	return res, nil
}

func (s *Store) queryLocked(names []string, from, to time.Time, hourly bool) (map[string][]Point, []string, error) {
	byName := map[string][]Point{}
	var order []string
	var (
		rows *sql.Rows
		err  error
	)
	if hourly {
		fromBucket := from.Truncate(time.Hour).Unix()
		toBucket := to.Unix()
		if len(names) == 0 {
			rows, err = s.db.Query(
				`SELECT name, bucket, sum, count FROM samples_1h WHERE bucket >= ? AND bucket <= ? AND count > 0 ORDER BY name, bucket`,
				fromBucket, toBucket,
			)
		} else {
			placeholders := make([]string, len(names))
			args := make([]any, 0, len(names)+2)
			for i, n := range names {
				placeholders[i] = "?"
				args = append(args, n)
			}
			args = append(args, fromBucket, toBucket)
			q := `SELECT name, bucket, sum, count FROM samples_1h WHERE name IN (` +
				strings.Join(placeholders, ",") +
				`) AND bucket >= ? AND bucket <= ? AND count > 0 ORDER BY name, bucket`
			rows, err = s.db.Query(q, args...)
		}
		if err != nil {
			return nil, nil, fmt.Errorf("metrics: query hourly: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var name string
			var bucket int64
			var sum float64
			var count int
			if err := rows.Scan(&name, &bucket, &sum, &count); err != nil {
				return nil, nil, fmt.Errorf("metrics: scan hourly: %w", err)
			}
			if count <= 0 {
				continue
			}
			if _, ok := byName[name]; !ok {
				order = append(order, name)
			}
			byName[name] = append(byName[name], Point{
				Time:  time.Unix(bucket, 0).UTC(),
				Value: sum / float64(count),
			})
		}
		return byName, order, rows.Err()
	}

	fromMicro := int64(0)
	if !from.IsZero() {
		fromMicro = from.UnixMicro()
	}
	toMicro := to.UnixMicro()
	if len(names) == 0 {
		rows, err = s.db.Query(
			`SELECT name, ts, value FROM samples WHERE ts >= ? AND ts <= ? ORDER BY name, ts`,
			fromMicro, toMicro,
		)
	} else {
		placeholders := make([]string, len(names))
		args := make([]any, 0, len(names)+2)
		for i, n := range names {
			placeholders[i] = "?"
			args = append(args, n)
		}
		args = append(args, fromMicro, toMicro)
		q := `SELECT name, ts, value FROM samples WHERE name IN (` +
			strings.Join(placeholders, ",") +
			`) AND ts >= ? AND ts <= ? ORDER BY name, ts`
		rows, err = s.db.Query(q, args...)
	}
	if err != nil {
		return nil, nil, fmt.Errorf("metrics: query: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		var ts int64
		var value float64
		if err := rows.Scan(&name, &ts, &value); err != nil {
			return nil, nil, fmt.Errorf("metrics: scan: %w", err)
		}
		if _, ok := byName[name]; !ok {
			order = append(order, name)
		}
		byName[name] = append(byName[name], Point{
			Time:  time.UnixMicro(ts).UTC(),
			Value: value,
		})
	}
	return byName, order, rows.Err()
}

// Prune deletes samples older than Retention relative to now, then applies the row cap.
func (s *Store) Prune(now time.Time) error {
	if s == nil || s.db == nil {
		return errors.New("metrics: store closed")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("metrics: begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := pruneTx(tx, now.UTC(), s.retention, s.maxRows); err != nil {
		return err
	}
	return tx.Commit()
}

func pruneTx(tx *sql.Tx, now time.Time, retention time.Duration, maxRows int) error {
	if retention <= 0 {
		retention = Retention
	}
	cutoff := now.Add(-retention).UnixMicro()
	if _, err := tx.Exec(`DELETE FROM samples WHERE ts < ?`, cutoff); err != nil {
		return fmt.Errorf("metrics: prune age: %w", err)
	}
	hourCutoff := now.Add(-HourlyRetention).Unix()
	if _, err := tx.Exec(`DELETE FROM samples_1h WHERE bucket < ?`, hourCutoff); err != nil {
		return fmt.Errorf("metrics: prune hourly: %w", err)
	}
	if maxRows <= 0 {
		maxRows = DefaultMaxRows
	}
	var n int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM samples`).Scan(&n); err != nil {
		return fmt.Errorf("metrics: count: %w", err)
	}
	if n <= maxRows {
		return nil
	}
	extra := n - maxRows
	if _, err := tx.Exec(
		`DELETE FROM samples WHERE rowid IN (
			SELECT rowid FROM samples ORDER BY ts ASC, rowid ASC LIMIT ?
		)`,
		extra,
	); err != nil {
		return fmt.Errorf("metrics: prune cap: %w", err)
	}
	return nil
}

func makeSeries(name string, points []Point, to, now time.Time) Series {
	if points == nil {
		points = []Point{}
	}
	s := Series{
		Name:   name,
		Unit:   unitFor(name),
		Points: points,
		Status: emptyStatus(name, points),
	}
	if len(points) == 0 {
		return s
	}
	s.Status = StatusAvailable
	ref := to
	if ref.IsZero() {
		ref = now
	}
	last := points[len(points)-1].Time
	if now.Sub(ref) <= StaleAfter && ref.Sub(last) > StaleAfter {
		s.Status = StatusStale
	}
	return s
}

func emptyStatus(name string, points []Point) Status {
	if len(points) > 0 {
		return StatusAvailable
	}
	if knownName(name) {
		return StatusCollecting
	}
	return StatusUnavailable
}

func emptyWithNames(names []string) QueryResult {
	if len(names) == 0 {
		return QueryResult{Status: StatusCollecting, Series: []Series{}}
	}
	series := make([]Series, 0, len(names))
	for _, name := range names {
		series = append(series, Series{
			Name:   name,
			Status: emptyStatus(name, nil),
			Unit:   unitFor(name),
			Points: []Point{},
		})
	}
	return QueryResult{Status: rollup(series), Series: series}
}

func rollup(series []Series) Status {
	if len(series) == 0 {
		return StatusCollecting
	}
	var hasAvail, hasStale, hasColl, hasUnavail bool
	for _, s := range series {
		switch s.Status {
		case StatusAvailable:
			hasAvail = true
		case StatusStale:
			hasStale = true
		case StatusCollecting:
			hasColl = true
		case StatusUnavailable:
			hasUnavail = true
		}
	}
	switch {
	case hasAvail:
		return StatusAvailable
	case hasStale:
		return StatusStale
	case hasColl:
		return StatusCollecting
	case hasUnavail:
		return StatusUnavailable
	default:
		return StatusCollecting
	}
}
