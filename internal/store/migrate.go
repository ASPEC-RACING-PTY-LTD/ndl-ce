package store

import (
	"context"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strings"
)

// DB is the migration executor. Phase 0 has no product schema.
type DB interface {
	ExecContext(ctx context.Context, query string, args ...any) error
}

// File is one numbered SQL migration.
type File struct {
	Name string
	SQL  string
}

// List returns SQL files in lexical order from dir.
func List(fsys fs.FS, dir string) ([]File, error) {
	entries, err := fs.ReadDir(fsys, dir)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if !strings.HasSuffix(e.Name(), ".sql") {
			continue
		}
		names = append(names, e.Name())
	}
	sort.Strings(names)
	out := make([]File, 0, len(names))
	for _, name := range names {
		b, err := fs.ReadFile(fsys, path.Join(dir, name))
		if err != nil {
			return nil, err
		}
		out = append(out, File{Name: name, SQL: string(b)})
	}
	return out, nil
}

// AppliedReader looks up recorded migrations.
type AppliedReader interface {
	QueryApplied(ctx context.Context) (map[string]struct{}, error)
}

// Apply runs every file in order. If db implements AppliedReader,
// already-recorded names are skipped and then recorded.
func Apply(ctx context.Context, db DB, files []File) error {
	applied := map[string]struct{}{}
	if r, ok := db.(AppliedReader); ok {
		got, err := r.QueryApplied(ctx)
		if err != nil {
			return err
		}
		applied = got
	}
	for _, f := range files {
		if strings.TrimSpace(f.SQL) == "" {
			return fmt.Errorf("migration %s is empty", f.Name)
		}
		if _, ok := applied[f.Name]; ok {
			continue
		}
		if err := db.ExecContext(ctx, f.SQL); err != nil {
			return fmt.Errorf("migration %s: %w", f.Name, err)
		}
		if rec, ok := db.(interface {
			RecordApplied(ctx context.Context, name string) error
		}); ok {
			if err := rec.RecordApplied(ctx, f.Name); err != nil {
				return err
			}
		}
	}
	return nil
}
