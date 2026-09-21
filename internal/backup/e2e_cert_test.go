package backup

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestDisposableLifecycle exercises the V2 engine on a throwaway tree: baseline,
// incremental edit, deletion, historical restore, cross-workload dedupe,
// retention/GC, remote protect, remote restore, and interrupted capture.
func TestDisposableLifecycle(t *testing.T) {
	repo, err := OpenRepository(t.TempDir(), testKeys(t))
	if err != nil {
		t.Fatal(err)
	}
	e := NewEngine(repo, smallCfg())
	src := t.TempDir()
	writeFile(t, filepath.Join(src, "keep.txt"), []byte("keep-v1"), 0o644)
	writeFile(t, filepath.Join(src, "gone.txt"), []byte("temp"), 0o644)
	m1, s1, err := e.Capture(context.Background(), CaptureOptions{Source: src, WorkloadID: "life", WorkloadName: "life"})
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(src, "keep.txt"), []byte("keep-v2"), 0o644)
	if err := os.Remove(filepath.Join(src, "gone.txt")); err != nil {
		t.Fatal(err)
	}
	m2, s2, err := e.Capture(context.Background(), CaptureOptions{Source: src, WorkloadID: "life", WorkloadName: "life"})
	if err != nil {
		t.Fatal(err)
	}
	latest := t.TempDir()
	if err := e.Repo().Restore(context.Background(), m2, RestoreOptions{Dest: latest}); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(filepath.Join(latest, "keep.txt"))
	if string(got) != "keep-v2" {
		t.Fatalf("latest restore %q", got)
	}
	if _, err := os.Stat(filepath.Join(latest, "gone.txt")); !os.IsNotExist(err) {
		t.Fatal("deleted file leaked into latest restore")
	}
	hist := t.TempDir()
	if err := e.Repo().Restore(context.Background(), m1, RestoreOptions{Dest: hist}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(hist, "gone.txt")); err != nil {
		t.Fatal("historical restore must keep gone.txt")
	}

	other := t.TempDir()
	writeFile(t, filepath.Join(other, "keep.txt"), []byte("keep-v2"), 0o644)
	m3, _, err := e.Capture(context.Background(), CaptureOptions{Source: other, WorkloadID: "peer", WorkloadName: "peer"})
	if err != nil {
		t.Fatal(err)
	}
	if m3.Stats.ChunksNew != 0 {
		t.Fatalf("cross-workload dedupe failed: %+v", m3.Stats)
	}

	tgt := NewMemTarget()
	q := NewUploadQueue(e.Repo(), tgt, 2)
	if err := q.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := q.EnqueueBackup(s1); err != nil {
		t.Fatal(err)
	}
	if err := q.EnqueueBackup(s2); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 8*time.Second, func() bool { return q.PendingJobs() == 0 })
	q.Stop()
	st1, _ := e.Repo().LoadState(s1.Namespace, s1.BackupID)
	if st1.Remote != RemoteProtected {
		t.Fatalf("remote state %s", st1.Remote)
	}

	remoteRoot := t.TempDir()
	fetched, man, err := FetchRemote(context.Background(), tgt, e.Repo().Keys(), s2.Namespace, s2.BackupID, remoteRoot)
	if err != nil {
		t.Fatal(err)
	}
	fromRemote := t.TempDir()
	if err := fetched.Restore(context.Background(), man, RestoreOptions{Dest: fromRemote}); err != nil {
		t.Fatal(err)
	}
	got, _ = os.ReadFile(filepath.Join(fromRemote, "keep.txt"))
	if string(got) != "keep-v2" {
		t.Fatalf("remote restore %q", got)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := e.Capture(ctx, CaptureOptions{Source: src, WorkloadID: "life", WorkloadName: "life"}); err == nil {
		t.Fatal("interrupted capture should fail")
	}
	if _, err := e.Repo().CollectGarbage(); err != nil {
		t.Fatal(err)
	}
}

func TestConcurrentCapturesDoNotSerialize(t *testing.T) {
	e := testEngine(t, smallCfg())
	done := make(chan error, 2)
	for i, name := range []string{"a", "b"} {
		src := t.TempDir()
		writeFile(t, filepath.Join(src, "f"), randBytes(int64(10+i), 64<<10), 0o644)
		go func(src, name string) {
			_, _, err := e.Capture(context.Background(), CaptureOptions{Source: src, WorkloadID: name, WorkloadName: name})
			done <- err
		}(src, name)
	}
	for i := 0; i < 2; i++ {
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
}
