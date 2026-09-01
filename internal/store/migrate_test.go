package store

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"testing/fstest"
)

type memDB struct {
	stmts []string
}

func (m *memDB) ExecContext(_ context.Context, query string, _ ...any) error {
	m.stmts = append(m.stmts, query)
	return nil
}

func TestListAndApply(t *testing.T) {
	fsys := fstest.MapFS{
		"migrations/0002_later.sql": {Data: []byte("SELECT 2;")},
		"migrations/0001_init.sql":  {Data: []byte("SELECT 1;")},
		"migrations/readme.txt":     {Data: []byte("ignore")},
	}
	files, err := List(fsys, "migrations")
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 2 || files[0].Name != "0001_init.sql" {
		t.Fatalf("files=%v", files)
	}
	db := &memDB{}
	if err := Apply(context.Background(), db, files); err != nil {
		t.Fatal(err)
	}
	if len(db.stmts) != 2 {
		t.Fatalf("stmts=%d", len(db.stmts))
	}
}

func TestApplyRejectsEmpty(t *testing.T) {
	err := Apply(context.Background(), &memDB{}, []File{{Name: "x.sql", SQL: "   "}})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestListMissingDir(t *testing.T) {
	_, err := List(fstest.MapFS{}, "migrations")
	if err == nil {
		t.Fatal("expected error")
	}
	if _, ok := err.(*fs.PathError); !ok && err != fs.ErrNotExist {
		t.Fatalf("unexpected error %v", err)
	}
}

func TestRepoHasNumberedPhase1SQL(t *testing.T) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("caller")
	}
	dir := filepath.Join(filepath.Dir(file), "..", "..", "migrations")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sql") {
			continue
		}
		if !strings.HasPrefix(e.Name(), "0001_") && !regexp.MustCompile(`^[0-9]{4}_`).MatchString(e.Name()) {
			t.Fatalf("unexpected migration name %s", e.Name())
		}
		found = true
	}
	if !found {
		t.Fatal("expected Phase 1 SQL migrations")
	}
}
