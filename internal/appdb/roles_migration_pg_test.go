package appdb

import (
	"context"
	"database/sql"
	"os"
	"sort"
	"strings"
	"testing"

	"github.com/google/uuid"
	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/no-dal/ndl-ce/internal/store"
	"github.com/no-dal/ndl-ce/migrations"
)

// Run with NODAL_TEST_PG_EMPTY pointing at an empty database: it applies
// the migrations before 0076, seeds roles with the old ids, then checks
// 0076 and 0077 and that running 0076 again changes nothing.
func TestRoleRenameMigrationOnPostgres(t *testing.T) {
	dsn := os.Getenv("NODAL_TEST_PG_EMPTY")
	if dsn == "" {
		t.Skip("NODAL_TEST_PG_EMPTY not set")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	files, err := store.List(migrations.FS, ".")
	if err != nil {
		t.Fatal(err)
	}
	var before, after []store.File
	var rename store.File
	for _, f := range files {
		if f.Name < "0076" {
			before = append(before, f)
		} else {
			after = append(after, f)
		}
		if strings.HasPrefix(f.Name, "0076") {
			rename = f
		}
	}
	if err := store.Apply(ctx, store.SQLDB{DB: db}, before); err != nil {
		t.Fatal(err)
	}

	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.ExecContext(ctx, q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	cluster := uuid.NewString()
	exec(`INSERT INTO clusters (id, name) VALUES ($1, 'mig')`, cluster)
	role := func(name, title string, builtin bool, rank int) string {
		id := uuid.NewString()
		exec(`INSERT INTO roles (id, cluster_id, name, permissions, title, rank, builtin, login) VALUES ($1,$2,$3,'{events.read}',$4,$5,$6,true)`,
			id, cluster, name, title, rank, builtin)
		return id
	}
	ids := map[string]string{
		"admin":      role("admin", "Owner", true, 0),
		"operator":   role("operator", "Admin", true, 1),
		"automation": role("automation", "Automation", true, 2),
		"viewer":     role("viewer", "User", true, 3),
		// Custom roles in the way of the new ids, or named like an old one.
		"user":  role("user", "Users team", false, 4),
		"owner": role("owner", "", false, 5),
		// A custom role whose id differs from its name, and one that matches.
		"support": role("support", "Help desk", false, 6),
		"ops":     role("ops", "Ops", false, 7),
	}
	user := func(name string, roles ...string) string {
		id := uuid.NewString()
		exec(`INSERT INTO users (id, cluster_id, username, password_hash) VALUES ($1,$2,$3,'x')`, id, cluster, name)
		for _, r := range roles {
			exec(`INSERT INTO role_bindings (id, cluster_id, user_id, role_id) VALUES ($1,$2,$3,$4)`, uuid.NewString(), cluster, id, ids[r])
		}
		return id
	}
	alice := user("alice", "admin")
	bob := user("bob", "operator", "support")
	carol := user("carol", "viewer", "user")
	group := uuid.NewString()
	exec(`INSERT INTO groups (id, cluster_id, name) VALUES ($1,$2,'oncall')`, group, cluster)
	exec(`INSERT INTO group_members (group_id, user_id) VALUES ($1,$2)`, group, carol)
	exec(`INSERT INTO group_role_bindings (group_id, role_id) VALUES ($1,$2), ($1,$3)`, group, ids["operator"], ids["support"])
	flowAlice, flowBob := uuid.NewString(), uuid.NewString()
	exec(`INSERT INTO automation_flows (id, cluster_id, name, created_by) VALUES ($1,$2,'a','alice'), ($3,$2,'b','bob')`, flowAlice, cluster, flowBob)
	flowOn := uuid.NewString()
	exec(`INSERT INTO automation_flows (id, cluster_id, name, created_by, enabled) VALUES ($1,$2,'on','alice',true)`, flowOn, cluster)

	if err := store.Apply(ctx, store.SQLDB{DB: db}, after); err != nil {
		t.Fatal(err)
	}

	snapshot := func() string {
		t.Helper()
		rows, err := db.QueryContext(ctx, `SELECT id::text, name, title, rank, builtin FROM roles WHERE cluster_id=$1 ORDER BY id`, cluster)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		var b strings.Builder
		for rows.Next() {
			var id, name, title string
			var rank int
			var builtin bool
			if err := rows.Scan(&id, &name, &title, &rank, &builtin); err != nil {
				t.Fatal(err)
			}
			b.WriteString(id + " " + name + " " + title + " ")
			if builtin {
				b.WriteString("builtin")
			}
			b.WriteString("\n")
		}
		return b.String()
	}
	nameOf := func(id string) (string, string) {
		t.Helper()
		var name, title string
		if err := db.QueryRowContext(ctx, `SELECT name, title FROM roles WHERE id=$1`, id).Scan(&name, &title); err != nil {
			t.Fatalf("role %s: %v", id, err)
		}
		return name, title
	}
	// Built-ins renamed in place: same row ids.
	for old, want := range map[string][2]string{
		"admin": {"superadmin", "SuperAdmin"}, "operator": {"admin", "Admin"}, "viewer": {"user", "User"}, "automation": {"automation", "Automation"},
		"user": {"users-team", "Users team"}, "owner": {"owner-custom", "owner"}, "support": {"help-desk", "Help desk"}, "ops": {"ops", "Ops"},
	} {
		if name, title := nameOf(ids[old]); name != want[0] || title != want[1] {
			t.Fatalf("%s became %s (%s), want %v", old, name, title, want)
		}
	}
	userRoles := func(id string) []string {
		t.Helper()
		rows, err := db.QueryContext(ctx, `SELECT r.name FROM role_bindings b JOIN roles r ON r.id=b.role_id WHERE b.user_id=$1`, id)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		var out []string
		for rows.Next() {
			var n string
			_ = rows.Scan(&n)
			out = append(out, n)
		}
		sort.Strings(out)
		return out
	}
	if got := strings.Join(userRoles(alice), ","); got != "superadmin" {
		t.Fatalf("alice %s", got)
	}
	if got := strings.Join(userRoles(bob), ","); got != "admin,help-desk" {
		t.Fatalf("bob %s", got)
	}
	if got := strings.Join(userRoles(carol), ","); got != "user,users-team" {
		t.Fatalf("carol %s", got)
	}
	var groupRoles string
	if err := db.QueryRowContext(ctx, `SELECT string_agg(r.name, ',' ORDER BY r.name) FROM group_role_bindings g JOIN roles r ON r.id=g.role_id WHERE g.group_id=$1`, group).Scan(&groupRoles); err != nil || groupRoles != "admin,help-desk" {
		t.Fatalf("group roles %q %v", groupRoles, err)
	}
	var renames int
	_ = db.QueryRowContext(ctx, `SELECT count(*) FROM audit_events WHERE cluster_id=$1 AND action='role.rename'`, cluster).Scan(&renames)
	// Moved aside: user, owner. Renamed to match: users-team, help-desk.
	// Reported, not guessed: owner-custom (its name is a reserved id).
	if renames != 5 {
		t.Fatalf("rename audit lines %d", renames)
	}
	// 0077: root backfilled to the oldest SuperAdmin; flows by people
	// below SuperAdmin are tainted; existing versions stay approved.
	var root string
	if err := db.QueryRowContext(ctx, `SELECT root_user_id::text FROM iam_settings WHERE cluster_id=$1`, cluster).Scan(&root); err != nil || root != alice {
		t.Fatalf("root %q %v", root, err)
	}
	var taintA, taintB bool
	_ = db.QueryRowContext(ctx, `SELECT draft_tainted FROM automation_flows WHERE id=$1`, flowAlice).Scan(&taintA)
	_ = db.QueryRowContext(ctx, `SELECT draft_tainted FROM automation_flows WHERE id=$1`, flowBob).Scan(&taintB)
	if taintA || !taintB {
		t.Fatalf("taint alice=%v bob=%v", taintA, taintB)
	}

	// 0077 turns off every flow that was on, saying why.
	var onEnabled bool
	var onReason string
	_ = db.QueryRowContext(ctx, `SELECT enabled, disabled_reason FROM automation_flows WHERE id=$1`, flowOn).Scan(&onEnabled, &onReason)
	if onEnabled || !strings.Contains(onReason, "Turned off by the update") {
		t.Fatalf("flow left on by 0077: enabled=%v reason=%q", onEnabled, onReason)
	}

	// 0078: appearance and search settings, search index and templates.
	pg := &Postgres{DB: db}
	if err := pg.PutUISettings(ctx, UISettings{ClusterID: cluster, Appearance: []byte(`{"nav_layout":"classic"}`), Search: []byte(`{}`), UpdatedBy: "alice"}); err != nil {
		t.Fatal(err)
	}
	if got, err := pg.GetUISettings(ctx, cluster); err != nil || !strings.Contains(string(got.Appearance), "classic") {
		t.Fatalf("ui settings %s %v", got.Appearance, err)
	}
	if err := pg.UpsertSearchDocs(ctx, []SearchDoc{{ClusterID: cluster, Kind: "user", RefID: alice, Scope: "primary", Title: "alice", Body: "superadmin", Link: "/users", Permission: "users.read"}}); err != nil {
		t.Fatal(err)
	}
	docs, err := pg.SearchDocs(ctx, SearchQuery{ClusterID: cluster, Text: "alic", Allowed: func(p string) bool { return p == "users.read" }, Limit: 10})
	if err != nil || len(docs) != 1 {
		t.Fatalf("search %v %v", docs, err)
	}
	if docs, _ := pg.SearchDocs(ctx, SearchQuery{ClusterID: cluster, Text: "alic", Allowed: func(p string) bool { return p == "events.read" }, Limit: 10}); len(docs) != 0 {
		t.Fatalf("search ignored access: %v", docs)
	}
	tmpl := InstanceTemplate{ID: uuid.NewString(), ClusterID: cluster, Kind: "oci", Name: "app", Spec: []byte(`{"image":"nginx"}`)}
	if err := pg.CreateInstanceTemplate(ctx, tmpl); err != nil {
		t.Fatal(err)
	}
	if list, err := pg.ListInstanceTemplates(ctx, cluster); err != nil || len(list) != 1 || !strings.Contains(string(list[0].Spec), "nginx") {
		t.Fatalf("templates %v %v", list, err)
	}
	if ok, err := pg.DeleteInstanceTemplate(ctx, cluster, tmpl.ID); err != nil || !ok {
		t.Fatalf("delete template %v %v", ok, err)
	}

	// Running 0076 again changes nothing.
	snap := snapshot()
	if _, err := db.ExecContext(ctx, rename.SQL); err != nil {
		t.Fatalf("re-run: %v", err)
	}
	if again := snapshot(); again != snap {
		t.Fatalf("re-run changed roles:\n%s\nvs\n%s", snap, again)
	}

	// The store reads the new ids.
	p := &Postgres{DB: db}
	if n, err := p.CountAdmins(ctx, cluster); err != nil || n != 1 {
		t.Fatalf("CountAdmins %d %v", n, err)
	}
	if err := p.EnsureRoles(ctx, cluster, map[string][]string{"superadmin": {"*"}, "admin": {"events.read"}, "user": {"events.read"}, "automation": {"*"}}); err != nil {
		t.Fatal(err)
	}
	var count int
	_ = db.QueryRowContext(ctx, `SELECT count(*) FROM roles WHERE cluster_id=$1 AND builtin`, cluster).Scan(&count)
	if count != 4 {
		t.Fatalf("EnsureRoles added duplicates: %d built-in roles", count)
	}
}

// A leftover old id fails the migration, which then rolls back.
func TestRoleRenameMigrationStrayCheck(t *testing.T) {
	dsn := os.Getenv("NODAL_TEST_PG_EMPTY")
	if dsn == "" {
		t.Skip("NODAL_TEST_PG_EMPTY not set")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	files, _ := store.List(migrations.FS, ".")
	var rename string
	for _, f := range files {
		if strings.HasPrefix(f.Name, "0076") {
			rename = f.SQL
		}
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	// Run after TestRoleRenameMigrationOnPostgres: the one cluster is
	// migrated, so the loop skips it and a leftover "viewer" must be caught.
	var cluster string
	if err := tx.QueryRowContext(ctx, `SELECT id::text FROM clusters LIMIT 1`).Scan(&cluster); err != nil {
		t.Skip("no cluster: run the whole file")
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO roles (id, cluster_id, name, permissions, title, rank, builtin, login) VALUES ($1,$2,'viewer','{}','User',9,false,true)`, uuid.NewString(), cluster); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(ctx, rename); err == nil || !strings.Contains(err.Error(), "old id") {
		t.Fatalf("stray old id not caught: %v", err)
	}
}
