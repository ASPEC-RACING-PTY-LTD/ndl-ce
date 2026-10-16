package backup

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// A second target's queue on the same repository must never upload, or mark
// protected, a restore point queued for the first target.
func TestUploadJobsStayWithTheirTarget(t *testing.T) {
	e := testEngine(t, smallCfg())
	src := t.TempDir()
	writeFile(t, filepath.Join(src, "f.bin"), randBytes(31, 512<<10), 0o644)
	_, state, err := e.Capture(context.Background(), CaptureOptions{Source: src, WorkloadID: "wl", WorkloadName: "app"})
	if err != nil {
		t.Fatal(err)
	}

	stale := NewMemTarget()
	other := NewUploadQueue(e.Repo(), stale, 2)
	other.SetTargetKey("stale-target")
	if err := other.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer other.Stop()

	live := NewMemTarget()
	live.Delay = 50 * time.Millisecond
	q := NewUploadQueue(e.Repo(), live, 2)
	q.SetTargetKey("live-target")
	if err := q.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer q.Stop()
	if err := q.EnqueueBackup(state); err != nil {
		t.Fatal(err)
	}

	waitFor(t, 10*time.Second, func() bool {
		ps, _ := e.Repo().LoadState(state.Namespace, state.BackupID)
		return ps != nil && ps.Remote == RemoteProtected
	})
	if stale.PutCount() != 0 {
		t.Fatalf("the other target received %d objects", stale.PutCount())
	}
	if live.PutCount() == 0 {
		t.Fatal("the live target received nothing")
	}
}

// Jobs queued before targets had their own directory are taken over by the
// queue that starts, and Discard removes a deleted target's jobs and index.
func TestUploadQueueAdoptsLegacyJobsAndDiscards(t *testing.T) {
	e := testEngine(t, smallCfg())
	src := t.TempDir()
	writeFile(t, filepath.Join(src, "f.bin"), randBytes(32, 512<<10), 0o644)
	_, state, err := e.Capture(context.Background(), CaptureOptions{Source: src, WorkloadID: "wl", WorkloadName: "app"})
	if err != nil {
		t.Fatal(err)
	}
	legacy := NewUploadQueue(e.Repo(), NewMemTarget(), 1)
	if err := legacy.EnqueueBackup(state); err != nil {
		t.Fatal(err)
	}

	target := NewMemTarget()
	q := NewUploadQueue(e.Repo(), target, 2)
	q.SetTargetKey("r2")
	if err := q.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 10*time.Second, func() bool {
		ps, _ := e.Repo().LoadState(state.Namespace, state.BackupID)
		return ps != nil && ps.Remote == RemoteProtected
	})
	if len(e.Repo().LoadUploaded("r2")) == 0 {
		t.Fatal("expected an uploaded-pack index for the target")
	}
	dir := q.queueDir()
	q.Discard()
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("queue directory still present: %v", err)
	}
	if len(e.Repo().LoadUploaded("r2")) != 0 {
		t.Fatal("uploaded-pack index still present")
	}
}
