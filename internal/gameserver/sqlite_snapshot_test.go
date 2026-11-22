package gameserver

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func openTestDB(t *testing.T, path string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", sqliteURI(path, "_pragma=busy_timeout(5000)&_pragma=journal_mode(delete)"))
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	return db
}

func TestSQLiteFilesAreCopiedConsistently(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "garrysmod"), 0o755); err != nil {
		t.Fatal(err)
	}
	dbPath := filepath.Join(root, "garrysmod", "sv.db")
	db := openTestDB(t, dbPath)
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE players (id INTEGER PRIMARY KEY, name TEXT, blob TEXT)`); err != nil {
		t.Fatal(err)
	}
	pad := fmt.Sprintf("%0512d", 0)
	tx, _ := db.Begin()
	for i := 0; i < 20000; i++ {
		if _, err := tx.Exec(`INSERT INTO players (name, blob) VALUES (?, ?)`, fmt.Sprint("p", i), pad); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	// The "game" keeps writing while the backup runs; none of its
	// writes may fail.
	var writes, failed atomic.Int64
	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			if _, err := db.Exec(`UPDATE players SET name = name || 'x' WHERE id = ?`, 1+writes.Load()%20000); err != nil {
				failed.Add(1)
			}
			writes.Add(1)
			time.Sleep(20 * time.Millisecond)
		}
	}()

	dest := filepath.Join(t.TempDir(), "b.tar.gz")
	scope := BackupScope{Include: []string{"garrysmod/sv.db"}}
	err := archiveDir(context.Background(), root, dest, scope, "")
	close(stop)
	wg.Wait()
	if err != nil {
		t.Fatal(err)
	}
	if failed.Load() != 0 {
		t.Fatalf("%d of the game's writes failed during the backup", failed.Load())
	}
	if _, err := os.Stat(dest + ".sqlite-snapshot"); !os.IsNotExist(err) {
		t.Fatal("the temporary snapshot was left behind")
	}

	out := t.TempDir()
	if err := restoreArchive(out, dest, nil); err != nil {
		t.Fatal(err)
	}
	restored := filepath.Join(out, "garrysmod", "sv.db")
	if err := checkSQLite(context.Background(), restored); err != nil {
		t.Fatal(err)
	}
	rdb := openTestDB(t, restored)
	defer rdb.Close()
	var n int
	if err := rdb.QueryRow(`SELECT COUNT(*) FROM players`).Scan(&n); err != nil || n != 20000 {
		t.Fatalf("restored database has %d rows (%v)", n, err)
	}
}

func TestNonSQLiteFilesAreCopiedAsIs(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "sv.db")
	if err := os.WriteFile(p, []byte("not a database"), 0o644); err != nil {
		t.Fatal(err)
	}
	if isSQLite(p) {
		t.Fatal("plain file taken for SQLite")
	}
}
