package gameserver

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"time"

	"modernc.org/sqlite"
)

// A game keeps its SQLite database (Garry's Mod's sv.db) open and
// writes to it while it runs. Copying the file byte by byte can catch a
// write half done and save a database that will not open. Backups copy
// SQLite files through SQLite itself instead, which always yields a
// consistent database.
//
// The copy first runs in small steps. Each step holds a read lock for a
// few milliseconds, so the game's writes are barely held up. When the
// game changes the database between steps SQLite starts the copy again,
// so a busy database may never finish that way: after
// sqliteSnapshotBudget the rest is copied in one pass, which holds the
// read lock for one full copy (about a second per 100 MB) while the
// game's writes wait.

var sqliteMagic = []byte("SQLite format 3\x00")

const (
	sqliteStepPages      = 256 // about 1 MiB with 4 KiB pages
	sqliteStepPause      = 3 * time.Millisecond
	sqliteSnapshotBudget = 10 * time.Second
)

// isSQLite reports whether the file at path is an SQLite database.
func isSQLite(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	head := make([]byte, len(sqliteMagic))
	if _, err := io.ReadFull(f, head); err != nil {
		return false
	}
	return bytes.Equal(head, sqliteMagic)
}

func sqliteURI(path, query string) string {
	return "file:" + (&url.URL{Path: path}).EscapedPath() + "?" + query
}

type sqliteBackuper interface {
	NewBackup(dstURI string) (*sqlite.Backup, error)
}

// snapshotSQLite writes a consistent copy of the database at src to dst.
// src is opened read-only: the game's database is never changed.
func snapshotSQLite(ctx context.Context, src, dst string) error {
	_ = os.Remove(dst)
	db, err := sql.Open("sqlite", sqliteURI(src, "mode=ro&_pragma=busy_timeout(5000)"))
	if err != nil {
		return err
	}
	defer db.Close()
	conn, err := db.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	err = conn.Raw(func(dc any) error {
		b, ok := dc.(sqliteBackuper)
		if !ok {
			return errors.New("the SQLite driver cannot take backups")
		}
		bk, err := b.NewBackup(sqliteURI(dst, "mode=rwc"))
		if err != nil {
			return err
		}
		deadline := time.Now().Add(sqliteSnapshotBudget)
		for {
			more, err := bk.Step(sqliteStepPages)
			if err != nil {
				_ = bk.Finish()
				return err
			}
			if !more {
				return bk.Finish()
			}
			if cerr := ctx.Err(); cerr != nil {
				_ = bk.Finish()
				return cerr
			}
			if time.Now().After(deadline) {
				if _, err := bk.Step(-1); err != nil {
					_ = bk.Finish()
					return err
				}
				return bk.Finish()
			}
			time.Sleep(sqliteStepPause)
		}
	})
	if err != nil {
		_ = os.Remove(dst)
		return err
	}
	if err := checkSQLite(ctx, dst); err != nil {
		_ = os.Remove(dst)
		return err
	}
	return nil
}

// checkSQLite confirms a copy opens and passes SQLite's own check.
func checkSQLite(ctx context.Context, path string) error {
	db, err := sql.Open("sqlite", sqliteURI(path, "mode=ro"))
	if err != nil {
		return err
	}
	defer db.Close()
	var res string
	if err := db.QueryRowContext(ctx, "PRAGMA quick_check").Scan(&res); err != nil {
		return err
	}
	if res != "ok" {
		return fmt.Errorf("the copied database failed its check: %s", res)
	}
	return nil
}
