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

// Apply runs every file in order. Tracking of applied migrations is
// Phase 1. Phase 0 ships no product tables.
func Apply(ctx context.Context, db DB, files []File) error {
	for _, f := range files {
		if strings.TrimSpace(f.SQL) == "" {
			return fmt.Errorf("migration %s is empty", f.Name)
		}
		if err := db.ExecContext(ctx, f.SQL); err != nil {
			return fmt.Errorf("migration %s: %w", f.Name, err)
		}
	}
	return nil
}
