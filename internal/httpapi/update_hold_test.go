package httpapi

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/no-dal/ndl-ce/internal/appdb"
)

func TestUpdatePausesRunningBackupsAndRefusesNewOnes(t *testing.T) {
	s, _, _ := testServer(t)
	ctx, untrack, err := s.trackBackup(context.Background(), "wl-1")
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		<-ctx.Done()
		untrack()
	}()
	if n := s.suspendBackups("a platform apply is in progress", 5*time.Second); n != 1 {
		t.Fatalf("one running backup must be paused, got %d", n)
	}
	if !s.backupSuspended(ctx) {
		t.Fatal("the running backup must see that it was paused")
	}
	if _, _, err := s.trackBackup(context.Background(), "wl-2"); !errors.Is(err, errBackupSuspended) {
		t.Fatalf("new backups must wait for the update: %v", err)
	}
	s.resumeBackups()
	_, done, err := s.trackBackup(context.Background(), "wl-2")
	if err != nil {
		t.Fatalf("backups must run again after the update: %v", err)
	}
	done()
}

func TestPausedBackupsResumeOnceAfterTheUpdate(t *testing.T) {
	s, mem, _ := testServer(t)
	cluster, _ := mem.GetCluster(context.Background())
	run := appdb.BackupRun{
		ID: uuid.NewString(), ClusterID: cluster.ID, WorkloadID: uuid.NewString(), TargetID: uuid.NewString(),
		Status: appdb.BackupInterrupted, Error: backupSuspendedError, StartedAt: s.now(),
	}
	if err := mem.CreateBackupRun(context.Background(), run); err != nil {
		t.Fatal(err)
	}
	s.applyPreparing.Store(true)
	if n := s.ResumeSuspendedBackups(context.Background(), cluster.ID); n != 0 {
		t.Fatal("nothing may resume while the update is still in progress")
	}
	s.applyPreparing.Store(false)
	if n := s.ResumeSuspendedBackups(context.Background(), cluster.ID); n != 1 {
		t.Fatalf("the paused backup must run again, got %d", n)
	}
	got, _ := mem.GetBackupRun(context.Background(), cluster.ID, run.ID)
	if got == nil || got.Error != backupResumedError {
		t.Fatalf("the paused run must be marked resumed so it runs once: %+v", got)
	}
	if n := s.ResumeSuspendedBackups(context.Background(), cluster.ID); n != 0 {
		t.Fatal("a resumed backup must not run again")
	}
}
