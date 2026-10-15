package appdb

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	_ "github.com/jackc/pgx/v5/stdlib"
)

// Run with NODAL_TEST_PG pointing at a migrated test database.
func TestCloudflareAndExtensionsOnPostgres(t *testing.T) {
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
	// The schema allows one cluster; reuse it when the database has one.
	var cluster string
	created := false
	if err := db.QueryRowContext(ctx, `SELECT id::text FROM clusters LIMIT 1`).Scan(&cluster); err != nil {
		cluster, created = uuid.NewString(), true
		if _, err := db.ExecContext(ctx, `INSERT INTO clusters (id, name) VALUES ($1, $2)`, cluster, "cf-test"); err != nil {
			t.Fatal(err)
		}
	}
	wid, node := uuid.NewString(), uuid.NewString()
	if _, err := db.ExecContext(ctx, `INSERT INTO nodes (id, cluster_id) VALUES ($1,$2)`, node, cluster); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO workloads (id, cluster_id, node_id, owner_node_id, desired_node_id, name, kind, status, image_pin)
VALUES ($1,$2,$3,$3,$3,$4,'system-container','stopped','debian/13/amd64/default')`, wid, cluster, node, "wl-"+wid[:8]); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	cred := CloudflareCredential{ID: uuid.NewString(), ClusterID: cluster, Name: "racing-" + wid[:8], TokenLast4: "0001", Status: "active",
		Scope: json.RawMessage(`[{"account":{"id":"a","name":"A"}}]`), LastVerifiedAt: &now, CreatedBy: "admin"}
	if err := p.PutCloudflareCredential(ctx, cred); err != nil {
		t.Fatal(err)
	}
	dup := cred
	dup.ID = uuid.NewString()
	if err := p.PutCloudflareCredential(ctx, dup); err != ErrCloudflareNameTaken {
		t.Fatalf("duplicate name: %v", err)
	}
	if err := p.PutCloudflareToken(ctx, cluster, cred.ID, "ndlcf1:sealed"); err != nil {
		t.Fatal(err)
	}
	if got, err := p.GetCloudflareToken(ctx, cluster, cred.ID); err != nil || got != "ndlcf1:sealed" {
		t.Fatal(got, err)
	}
	got, err := p.GetCloudflareCredential(ctx, cluster, cred.ID)
	if err != nil || got == nil || got.TokenLast4 != "0001" || got.LastVerifiedAt == nil || string(got.Scope) == "[]" {
		t.Fatalf("credential %+v %v", got, err)
	}
	route := CloudflareRoute{ID: uuid.NewString(), ClusterID: cluster, AccountID: "a", TunnelID: "t1", Hostname: "Shop.Example.com",
		Rule: json.RawMessage(`{"hostname":"shop.example.com","service":"http://10.0.0.1:80"}`), Enabled: false, WorkloadID: wid, OriginIP: "10.0.0.1"}
	if err := p.PutCloudflareRoute(ctx, route); err != nil {
		t.Fatal(err)
	}
	route.ID, route.Enabled = uuid.NewString(), true
	if err := p.PutCloudflareRoute(ctx, route); err != nil {
		t.Fatal(err)
	}
	routes, _ := p.ListCloudflareRoutes(ctx, cluster)
	if len(routes) != 1 || routes[0].Hostname != "shop.example.com" || !routes[0].Enabled || routes[0].WorkloadID != wid {
		t.Fatalf("routes upsert by tunnel+hostname: %+v", routes)
	}
	if err := p.PutCloudflareWorkloadPref(ctx, CloudflareWorkloadPref{WorkloadID: wid, ClusterID: cluster, AccountID: "a", TunnelID: "t1"}); err != nil {
		t.Fatal(err)
	}
	if pref, _ := p.GetCloudflareWorkloadPref(ctx, cluster, wid); pref == nil || pref.TunnelID != "t1" {
		t.Fatalf("pref %+v", pref)
	}
	if err := p.PutCloudflareConnector(ctx, CloudflareConnector{ID: uuid.NewString(), ClusterID: cluster, AccountID: "a", TunnelID: "t1", MachineID: "host", Unit: "cloudflared-ndl-x.service"}); err != nil {
		t.Fatal(err)
	}
	if conns, _ := p.ListCloudflareConnectors(ctx, cluster); len(conns) != 1 {
		t.Fatalf("connectors %+v", conns)
	}
	if err := p.PutWorkloadExtension(ctx, WorkloadExtension{WorkloadID: wid, ClusterID: cluster, ExtraID: "docker", Requested: true, State: ExtensionFailed, Message: "apt said no"}); err != nil {
		t.Fatal(err)
	}
	if err := p.PutWorkloadExtension(ctx, WorkloadExtension{WorkloadID: wid, ClusterID: cluster, ExtraID: "docker", Requested: true, State: ExtensionInstalled, Version: "26.1.5"}); err != nil {
		t.Fatal(err)
	}
	exts, _ := p.ListWorkloadExtensions(ctx, cluster, wid)
	if len(exts) != 1 || exts[0].State != ExtensionInstalled || exts[0].Version != "26.1.5" || exts[0].Message != "" {
		t.Fatalf("extensions %+v", exts)
	}
	// Deleting the credential removes its sealed token; deleting the workload clears links.
	if err := p.DeleteCloudflareCredential(ctx, cluster, cred.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := p.GetCloudflareToken(ctx, cluster, cred.ID); err == nil {
		t.Fatal("the sealed token must go with its credential")
	}
	if _, err := db.ExecContext(ctx, `DELETE FROM workloads WHERE id=$1`, wid); err != nil {
		t.Fatal(err)
	}
	routes, _ = p.ListCloudflareRoutes(ctx, cluster)
	if len(routes) != 1 || routes[0].WorkloadID != "" {
		t.Fatalf("a deleted workload's routes stay, unlinked: %+v", routes)
	}
	_, _ = db.ExecContext(ctx, `DELETE FROM cloudflare_routes WHERE cluster_id=$1`, cluster)
	_, _ = db.ExecContext(ctx, `DELETE FROM cloudflare_connectors WHERE cluster_id=$1`, cluster)
	_, _ = db.ExecContext(ctx, `DELETE FROM nodes WHERE id=$1`, node)
	if created {
		_, _ = db.ExecContext(ctx, `DELETE FROM clusters WHERE id=$1`, cluster)
	}
}
