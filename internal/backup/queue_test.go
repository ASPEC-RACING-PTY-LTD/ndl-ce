package backup

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func waitFor(t *testing.T, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("condition not met within %s", timeout)
}

func TestUploadDecoupledFromCapture(t *testing.T) {
	e := testEngine(t, smallCfg())
	target := NewMemTarget()
	target.Delay = 400 * time.Millisecond // slow "remote"
	q := NewUploadQueue(e.Repo(), target, 1)
	if err := q.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer q.Stop()

	srcA := t.TempDir()
	writeFile(t, filepath.Join(srcA, "a.bin"), randBytes(21, 512<<10), 0o644)
	_, sA, err := e.Capture(context.Background(), CaptureOptions{Source: srcA, WorkloadID: "a", WorkloadName: "wlA"})
	if err != nil {
		t.Fatal(err)
	}
	if err := q.EnqueueBackup(sA); err != nil {
		t.Fatal(err)
	}

	// While A's slow upload is in flight, capturing B locally must not block.
	startB := time.Now()
	srcB := t.TempDir()
	writeFile(t, filepath.Join(srcB, "b.bin"), randBytes(22, 512<<10), 0o644)
	_, sB, err := e.Capture(context.Background(), CaptureOptions{Source: srcB, WorkloadID: "b", WorkloadName: "wlB"})
	if err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(startB); elapsed > 300*time.Millisecond {
		t.Fatalf("capture B took %s; it appears to have waited on A's upload", elapsed)
	}
	// A should still be uploading (slow target) at this point.
	if target.PutCount() > 0 {
		t.Fatalf("A upload completed before B captured; pipeline is serialized")
	}
	if err := q.EnqueueBackup(sB); err != nil {
		t.Fatal(err)
	}

	// Both should eventually become remotely protected.
	waitFor(t, 10*time.Second, func() bool {
		psA, _ := e.Repo().LoadState(sA.Namespace, sA.BackupID)
		psB, _ := e.Repo().LoadState(sB.Namespace, sB.BackupID)
		return psA != nil && psB != nil && psA.Remote == RemoteProtected && psB.Remote == RemoteProtected
	})
	if q.PendingJobs() != 0 {
		t.Fatalf("expected all jobs drained, %d pending", q.PendingJobs())
	}
}

func TestUploadRestartRecovery(t *testing.T) {
	e := testEngine(t, smallCfg())
	target := NewMemTarget()

	src := t.TempDir()
	writeFile(t, filepath.Join(src, "f.bin"), randBytes(23, 512<<10), 0o644)
	_, state, err := e.Capture(context.Background(), CaptureOptions{Source: src, WorkloadID: "wl", WorkloadName: "app"})
	if err != nil {
		t.Fatal(err)
	}

	// Enqueue with a queue that is never started: jobs are persisted to disk.
	q1 := NewUploadQueue(e.Repo(), target, 2)
	if err := q1.EnqueueBackup(state); err != nil {
		t.Fatal(err)
	}
	if q1.PendingJobs() == 0 {
		t.Fatal("expected persisted jobs")
	}

	// A fresh queue (simulating a process restart) must pick up and finish them.
	q2 := NewUploadQueue(e.Repo(), target, 2)
	if err := q2.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer q2.Stop()
	waitFor(t, 10*time.Second, func() bool {
		ps, _ := e.Repo().LoadState(state.Namespace, state.BackupID)
		return ps != nil && ps.Remote == RemoteProtected
	})
}

func TestUploadRetriesTransientFailures(t *testing.T) {
	e := testEngine(t, smallCfg())
	target := NewMemTarget()

	src := t.TempDir()
	writeFile(t, filepath.Join(src, "f.bin"), randBytes(24, 256<<10), 0o644)
	_, state, err := e.Capture(context.Background(), CaptureOptions{Source: src, WorkloadID: "wl", WorkloadName: "app"})
	if err != nil {
		t.Fatal(err)
	}
	// Force the pack upload to fail a few times before succeeding.
	for _, packID := range state.Packs {
		target.FailFor(objectKeyPack(packID), 3)
	}
	q := NewUploadQueue(e.Repo(), target, 2)
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
}
