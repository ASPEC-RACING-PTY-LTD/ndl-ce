package appdb

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"testing"

	"github.com/google/uuid"
	_ "github.com/jackc/pgx/v5/stdlib"
)

// Run with NODAL_TEST_PG pointing at a migrated test database.
func TestUIPrefsMergeOnPostgres(t *testing.T) {
	dsn := os.Getenv("NODAL_TEST_PG")
	if dsn == "" {
		t.Skip("NODAL_TEST_PG not set")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	p := &Postgres{DB: db}
	var cluster string
	if err := db.QueryRowContext(ctx, `SELECT id::text FROM clusters LIMIT 1`).Scan(&cluster); err != nil {
		cluster = uuid.NewString()
		if _, err := db.ExecContext(ctx, `INSERT INTO clusters (id, name) VALUES ($1, $2)`, cluster, "prefs-test"); err != nil {
			t.Fatal(err)
		}
	}
	uid := uuid.NewString()
	if _, err := db.ExecContext(ctx, `INSERT INTO users (id, cluster_id, username, password_hash) VALUES ($1,$2,$3,'x')`, uid, cluster, "prefs-"+uid[:8]); err != nil {
		t.Fatal(err)
	}
	defer db.ExecContext(ctx, `DELETE FROM users WHERE id=$1`, uid) //nolint:errcheck

	// Creating the row through a merge.
	if err := p.MergeUserUIPrefs(ctx, uid, cluster, map[string]json.RawMessage{"theme": json.RawMessage(`{"mode":"dark"}`)}, nil); err != nil {
		t.Fatal(err)
	}
	// Saving the other preferences keeps interface preferences.
	pref, err := p.GetUserPrefs(ctx, uid)
	if err != nil || pref == nil || string(pref.UI["theme"]) != `{"mode": "dark"}` {
		t.Fatalf("%+v %v", pref, err)
	}
	pref.WorkloadSort = "name"
	if err := p.UpsertUserPrefs(ctx, *pref); err != nil {
		t.Fatal(err)
	}
	if err := p.MergeUserUIPrefs(ctx, uid, cluster, map[string]json.RawMessage{"nav.order": json.RawMessage(`{"Compute":["files"]}`)}, nil); err != nil {
		t.Fatal(err)
	}
	if err := p.MergeUserUIPrefs(ctx, uid, cluster, nil, []string{"theme"}); err != nil {
		t.Fatal(err)
	}
	pref, _ = p.GetUserPrefs(ctx, uid)
	if pref.WorkloadSort != "name" || pref.UI["theme"] != nil || pref.UI["nav.order"] == nil {
		t.Fatalf("%+v", pref)
	}
}
