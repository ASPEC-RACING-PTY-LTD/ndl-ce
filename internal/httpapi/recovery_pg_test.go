package httpapi

import (
	"context"
	"database/sql"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/no-dal/ndl-ce/internal/appdb"
	"github.com/no-dal/ndl-ce/internal/store"
	"github.com/no-dal/ndl-ce/migrations"
)

// Run with NODAL_TEST_RESTORE_PG_OLD and NODAL_TEST_RESTORE_PG_FRESH set to
// two empty, disposable databases. The old one is filled and dumped; the
// fresh one, like a new install, is replaced by the dump.
func TestRestoreDatabaseReplacesAFreshInstallWithTheDump(t *testing.T) {
	oldDSN, freshDSN := os.Getenv("NODAL_TEST_RESTORE_PG_OLD"), os.Getenv("NODAL_TEST_RESTORE_PG_FRESH")
	if oldDSN == "" || freshDSN == "" {
		t.Skip("NODAL_TEST_RESTORE_PG_OLD/FRESH not set")
	}
	ctx := context.Background()
	migrate := func(dsn string) *appdb.Postgres {
		db, err := sql.Open("pgx", dsn)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = db.Close() })
		files, _ := store.List(migrations.FS, ".")
		if err := store.Apply(ctx, store.SQLDB{DB: db}, files); err != nil {
			t.Fatal(err)
		}
		return &appdb.Postgres{DB: db}
	}
	old := migrate(oldDSN)
	clusterID := uuid.NewString()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(old.CreateCluster(ctx, appdb.Cluster{ID: clusterID, Name: "lost"}))
	userID := uuid.NewString()
	must(old.CreateUser(ctx, appdb.User{ID: userID, ClusterID: clusterID, Username: "owner", PasswordHash: "x", Kind: appdb.UserKindPerson, CreatedAt: time.Now()}))
	oldNode := uuid.NewString()
	must(old.UpsertNode(ctx, appdb.Node{ID: oldNode, ClusterID: clusterID, Name: "local", Role: "control"}))
	poolID, netID := uuid.NewString(), uuid.NewString()
	must(old.CreateStoragePool(ctx, appdb.StoragePool{ID: poolID, ClusterID: clusterID, NodeID: oldNode, Name: "local", BackendType: "directory", Status: "available", RootPath: "/old/pool"}))
	must(old.CreateNetwork(ctx, appdb.Network{ID: netID, ClusterID: clusterID, NodeID: oldNode, Name: "lan", Kind: "isolated", Status: "available", BridgeName: "ndlold0"}))
	wlID := uuid.NewString()
	must(old.CreateWorkload(ctx, appdb.Workload{ID: wlID, ClusterID: clusterID, NodeID: oldNode, OwnerNodeID: oldNode, DesiredNodeID: oldNode, Name: "app", Kind: "system-container", DesiredPower: "running", Status: "running"}))
	targetID := uuid.NewString()
	must(old.CreateBackupTarget(ctx, appdb.BackupTarget{ID: targetID, ClusterID: clusterID, Name: "r2", Kind: "r2", Bucket: "ndl", Endpoint: "https://old.example", Username: "old-key", Status: "available"}, "old-secret", "enc-key"))

	s := &Server{DSN: oldDSN}
	dump, err := s.platformDump(ctx)
	if err != nil {
		t.Fatal(err)
	}

	// The fresh install has its own cluster, which recovery replaces.
	fresh := migrate(freshDSN)
	must(fresh.CreateCluster(ctx, appdb.Cluster{ID: uuid.NewString(), Name: "fresh"}))

	s = &Server{DSN: freshDSN}
	st, done, err := s.restoreDatabase(ctx, dump)
	if err != nil {
		t.Fatal(err)
	}
	defer done()
	c, err := st.GetCluster(ctx)
	if err != nil || c == nil || c.ID != clusterID {
		t.Fatalf("restored cluster %+v %v", c, err)
	}
	newNode := uuid.NewString()
	power, err := st.RemapRecoveredHost(ctx, clusterID, appdb.HostRemap{
		Node: appdb.Node{ID: newNode, Name: "local", Hostname: "new-host"}, Pools: map[string]string{poolID: "/new/pool"}, Networks: map[string]string{netID: "ndlnew0"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if power[wlID] != "running" {
		t.Fatalf("power %v", power)
	}
	wl, _ := st.GetWorkload(ctx, clusterID, wlID)
	if wl.NodeID != newNode || wl.DesiredPower != "stopped" {
		t.Fatalf("workload %+v", wl)
	}
	pools, _ := st.ListStoragePools(ctx, clusterID)
	nets, _ := st.ListNetworks(ctx, clusterID)
	if pools[0].RootPath != "/new/pool" || pools[0].NodeID != newNode || nets[0].BridgeName != "ndlnew0" {
		t.Fatalf("pool %+v net %+v", pools[0], nets[0])
	}
	must(st.ReplaceBackupTargetConnection(ctx, clusterID, targetID, "https://new.example", "auto", "new-key", "new-secret"))
	pass, enc, _ := st.BackupCredentials(ctx, clusterID, targetID)
	tgt, _ := st.GetBackupTarget(ctx, clusterID, targetID)
	if pass != "new-secret" || enc != "enc-key" || tgt.Username != "new-key" || tgt.Endpoint != "https://new.example" {
		t.Fatalf("target %+v %q %q", tgt, pass, enc)
	}

	// A failing load leaves the database as it was.
	s2 := &Server{DSN: freshDSN}
	if _, _, err := s2.restoreDatabase(ctx, []byte("not a dump")); err == nil {
		t.Fatal("a bad dump must fail")
	}
	if c, _ := st.GetCluster(ctx); c == nil || c.ID != clusterID {
		t.Fatal("a failed restore must change nothing")
	}
}
