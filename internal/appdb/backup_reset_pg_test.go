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
// every migration, then checks the queued run flag, clearing a policy's
// history and the full backup reset.
func TestBackupQueueHistoryAndResetOnPostgres(t *testing.T) {
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
	p := &Postgres{DB: db}
	cluster, target := uuid.NewString(), uuid.NewString()
	if _, err := db.ExecContext(ctx, `INSERT INTO clusters (id, name) VALUES ($1, 'bk')`, cluster); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO backup_targets (id, cluster_id, name, kind, locator, status) VALUES ($1,$2,'r2','local','/tmp/x','available')`, target, cluster); err != nil {
		t.Fatal(err)
	}
	pol := BackupPolicy{ID: uuid.NewString(), ClusterID: cluster, Name: "p", Scope: BackupScopeAll, TargetID: target, Schedule: BackupNightly, KeepDaily: 1, CreatedAt: time.Now()}
	if err := p.CreateBackupPolicy(ctx, pol); err != nil {
		t.Fatal(err)
	}
	if err := p.SetBackupPolicyRunQueued(ctx, cluster, pol.ID, true); err != nil {
		t.Fatal(err)
	}
	pol.Name = "renamed"
	if err := p.UpdateBackupPolicy(ctx, pol); err != nil {
		t.Fatal(err)
	}
	if got, _ := p.GetBackupPolicy(ctx, cluster, pol.ID); got == nil || !got.RunQueued || got.Name != "renamed" {
		t.Fatalf("an edit must keep the queued run: %+v", got)
	}
	if list, _ := p.ListBackupPolicies(ctx, cluster); len(list) != 1 || !list[0].RunQueued {
		t.Fatalf("listed policies must carry the queued run: %+v", list)
	}
	now := time.Now().UTC()
	run := func(status string) BackupRun {
		r := BackupRun{ID: uuid.NewString(), ClusterID: cluster, PolicyID: pol.ID, TargetID: target, WorkloadID: uuid.NewString(), Status: status, StartedAt: now}
		if err := p.CreateBackupRun(ctx, r); err != nil {
			t.Fatal(err)
		}
		return r
	}
	plain, withPoint, running := run(BackupSucceeded), run(BackupSucceeded), run(BackupRunning)
	if err := p.CreateBackupArtifact(ctx, BackupArtifact{ClusterID: cluster, RunID: withPoint.ID, WorkloadID: withPoint.WorkloadID, ChecksumSHA256: "x", Locator: "l", Format: "ndl-cab"}); err != nil {
		t.Fatal(err)
	}
	removed, kept, err := p.DeleteBackupRunsWithoutArtifacts(ctx, cluster, pol.ID)
	if err != nil || removed != 1 || kept != 2 {
		t.Fatalf("history: removed %d kept %d err %v", removed, kept, err)
	}
	if r, _ := p.GetBackupRun(ctx, cluster, plain.ID); r != nil {
		t.Fatal("the run without a restore point must be gone")
	}
	for _, id := range []string{withPoint.ID, running.ID} {
		if r, _ := p.GetBackupRun(ctx, cluster, id); r == nil {
			t.Fatalf("run %s must stay", id)
		}
	}
	if err := p.ResetBackupRecords(ctx, cluster); err != nil {
		t.Fatal(err)
	}
	runs, _ := p.ListBackupRuns(ctx, cluster)
	arts, _ := p.ListBackupArtifacts(ctx, cluster)
	if len(runs) != 0 || len(arts) != 0 {
		t.Fatalf("reset left %d runs and %d restore points", len(runs), len(arts))
	}
	got, _ := p.GetBackupPolicy(ctx, cluster, pol.ID)
	if got == nil || got.RunQueued {
		t.Fatalf("the policy must stay with its queue dropped: %+v", got)
	}
	var targets int
	_ = db.QueryRowContext(ctx, `SELECT count(*) FROM backup_targets WHERE cluster_id=$1`, cluster).Scan(&targets)
	if targets != 1 {
		t.Fatal("the target must stay")
	}
}
