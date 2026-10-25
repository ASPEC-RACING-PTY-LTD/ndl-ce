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
// every migration, then records, decides and forgets a backup approval.
func TestBackupApprovalsOnPostgres(t *testing.T) {
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
	cluster, wl := uuid.NewString(), uuid.NewString()
	if err := p.PutBackupApproval(ctx, BackupApproval{ClusterID: cluster, WorkloadID: wl, State: BackupApprovalPending}); err != nil {
		t.Fatal(err)
	}
	got, err := p.BackupApprovals(ctx, cluster)
	if err != nil || got[wl].State != BackupApprovalPending || got[wl].DecidedAt != nil {
		t.Fatalf("pending: %v %+v", err, got)
	}
	now := time.Now().UTC()
	if err := p.PutBackupApproval(ctx, BackupApproval{ClusterID: cluster, WorkloadID: wl, State: BackupApprovalDenied, DecidedBy: "u1", DecidedAt: &now}); err != nil {
		t.Fatal(err)
	}
	got, _ = p.BackupApprovals(ctx, cluster)
	if a := got[wl]; a.State != BackupApprovalDenied || a.DecidedBy != "u1" || a.DecidedAt == nil || len(got) != 1 {
		t.Fatalf("decided: %+v", got)
	}
	if err := p.DeleteBackupApproval(ctx, cluster, wl); err != nil {
		t.Fatal(err)
	}
	if got, _ = p.BackupApprovals(ctx, cluster); len(got) != 0 {
		t.Fatalf("forgotten: %+v", got)
	}
}
