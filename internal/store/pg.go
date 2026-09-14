package store

import (
	"context"
	"database/sql"
)

// SQLDB adapts database/sql to the migration runner.
type SQLDB struct {
	DB *sql.DB
}

// ExecContext runs SQL.
func (s SQLDB) ExecContext(ctx context.Context, query string, args ...any) error {
	_, err := s.DB.ExecContext(ctx, query, args...)
	return err
}

// QueryApplied reads schema_migrations.
func (s SQLDB) QueryApplied(ctx context.Context) (map[string]struct{}, error) {
	if _, err := s.DB.ExecContext(ctx, `
CREATE TABLE IF NOT EXISTS schema_migrations (
    name text PRIMARY KEY,
    applied_at timestamptz NOT NULL DEFAULT now()
)`); err != nil {
		return nil, err
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT name FROM schema_migrations`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]struct{}{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		out[name] = struct{}{}
	}
	return out, rows.Err()
}

// RecordApplied inserts a migration name.
func (s SQLDB) RecordApplied(ctx context.Context, name string) error {
	_, err := s.DB.ExecContext(ctx, `INSERT INTO schema_migrations (name) VALUES ($1) ON CONFLICT DO NOTHING`, name)
	return err
}
