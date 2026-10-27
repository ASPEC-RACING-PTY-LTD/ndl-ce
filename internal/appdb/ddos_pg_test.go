package appdb

import (
	"context"
	"database/sql"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/no-dal/ndl-ce/internal/store"
	"github.com/no-dal/ndl-ce/migrations"
)

// Run with NODAL_TEST_PG_EMPTY pointing at an empty database: it applies
// every migration, then saves DDoS and edge forwarding settings and records
// an attack.
func TestDDoSOnPostgres(t *testing.T) {
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
	if err := store.Apply(ctx, store.SQLDB{DB: db}, files); err != nil {
		t.Fatal(err)
	}
	cluster := uuid.NewString()
	if _, err := db.ExecContext(ctx, `DELETE FROM clusters`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO clusters (id) VALUES ($1)`, cluster); err != nil {
		t.Fatal(err)
	}
	p := &Postgres{DB: db}
	if body, err := p.DDoSConfig(ctx, cluster); err != nil || body != nil {
		t.Fatalf("unsaved config = %s, %v", body, err)
	}
	for _, body := range []string{`{"mode":"monitor"}`, `{"mode":"enforce","targets":[{"id":"t1"}]}`} {
		if err := p.PutDDoSConfig(ctx, cluster, []byte(body), "u1"); err != nil {
			t.Fatal(err)
		}
	}
	if body, err := p.DDoSConfig(ctx, cluster); err != nil || string(body) != `{"mode": "enforce", "targets": [{"id": "t1"}]}` {
		t.Fatalf("config = %s, %v", body, err)
	}
	if body, err := p.EdgeConfig(ctx, cluster); err != nil || body != nil {
		t.Fatalf("unsaved edge config = %s, %v", body, err)
	}
	if err := p.PutEdgeConfig(ctx, cluster, []byte(`{"forwards":[{"id":"f1"}]}`), "u1"); err != nil {
		t.Fatal(err)
	}
	if body, err := p.EdgeConfig(ctx, cluster); err != nil || string(body) != `{"forwards": [{"id": "f1"}]}` {
		t.Fatalf("edge config = %s, %v", body, err)
	}
	if err := p.PutClaudeAgentConfig(ctx, cluster, []byte(`{"workload_id":"w1"}`), "u1"); err != nil {
		t.Fatal(err)
	}
	if body, err := p.ClaudeAgentConfig(ctx, cluster); err != nil || string(body) != `{"workload_id": "w1"}` {
		t.Fatalf("claude agent config = %s, %v", body, err)
	}
	started := time.Now().UTC().Truncate(time.Second)
	ev := DDoSEvent{ID: uuid.NewString(), ClusterID: cluster, RuleID: "auto:gs:x", Target: "JvS", StartedAt: started, PeakPPS: 5000, TopReason: "query"}
	if err := p.PutDDoSEvent(ctx, ev); err != nil {
		t.Fatal(err)
	}
	end := started.Add(time.Minute)
	ev.EndedAt, ev.PeakPPS, ev.DroppedPackets, ev.Sources = &end, 9000, 123456, 40
	if err := p.PutDDoSEvent(ctx, ev); err != nil {
		t.Fatal(err)
	}
	got, err := p.ListDDoSEvents(ctx, cluster, 10)
	if err != nil || len(got) != 1 {
		t.Fatalf("events = %+v, %v", got, err)
	}
	if g := got[0]; g.PeakPPS != 9000 || g.DroppedPackets != 123456 || g.Sources != 40 || g.EndedAt == nil || !g.EndedAt.Equal(end) || g.TopReason != "query" {
		t.Fatalf("event = %+v", g)
	}
	if err := p.PutDDoSEvent(ctx, DDoSEvent{ID: "not-a-uuid", ClusterID: cluster}); err == nil {
		t.Fatal("a non-UUID event id was stored")
	}
}
