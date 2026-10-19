package backuphost

import (
	"context"
	"errors"
	"testing"
	"time"
)

// A clean-up that is still running when a move starts used to fail the move
// at once, and one queued just after the idle check could run during the
// copy. Quiesce waits for the clean-up and then holds the repository still.
func TestQuiesceWaitsForCleanupAndBlocksCaptures(t *testing.T) {
	h, err := Open(Options{Root: t.TempDir(), Settings: Settings{MaxLocalBytes: 1 << 20, MinHostFreeBytes: 1}})
	if err != nil {
		t.Fatal(err)
	}
	h.gcMu.Lock() // a clean-up is running
	got := make(chan error, 1)
	var release func()
	go func() {
		var err error
		release, err = h.Quiesce(context.Background())
		got <- err
	}()
	select {
	case <-got:
		t.Fatal("Quiesce returned while a clean-up was still running")
	case <-time.After(50 * time.Millisecond):
	}
	h.gcMu.Unlock() // the clean-up ends
	select {
	case err := <-got:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Quiesce did not return after the clean-up ended")
	}
	if err := h.acquireCapture(context.Background(), "wl"); err == nil {
		t.Fatal("a capture started while the repository was held still")
	}
	if h.gcMu.TryLock() {
		t.Fatal("a clean-up could start while the repository was held still")
	}
	release()
	release() // safe to call twice
	if err := h.acquireCapture(context.Background(), "wl"); err != nil {
		t.Fatalf("capture after release: %v", err)
	}
	h.releaseCapture("wl")
}

func TestQuiesceRefusesWhileACaptureRuns(t *testing.T) {
	h, err := Open(Options{Root: t.TempDir(), Settings: Settings{MaxLocalBytes: 1 << 20, MinHostFreeBytes: 1}})
	if err != nil {
		t.Fatal(err)
	}
	if err := h.acquireCapture(context.Background(), "wl"); err != nil {
		t.Fatal(err)
	}
	if _, err := h.Quiesce(context.Background()); !errors.Is(err, ErrNotIdle) {
		t.Fatalf("Quiesce during a capture: %v", err)
	}
	h.releaseCapture("wl")
}

func TestQuiesceGivesUpWhenTheCleanupOutlastsTheDeadline(t *testing.T) {
	h, err := Open(Options{Root: t.TempDir(), Settings: Settings{MaxLocalBytes: 1 << 20, MinHostFreeBytes: 1}})
	if err != nil {
		t.Fatal(err)
	}
	h.gcMu.Lock()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if _, err := h.Quiesce(ctx); !errors.Is(err, ErrNotIdle) {
		t.Fatalf("Quiesce past its deadline: %v", err)
	}
	h.gcMu.Unlock()
	// The abandoned wait must not keep the lock.
	time.Sleep(20 * time.Millisecond)
	if !h.gcMu.TryLock() {
		t.Fatal("the GC lock leaked after a timed-out Quiesce")
	}
	h.gcMu.Unlock()
}

func TestGCDoesNotRunOnAClosedHost(t *testing.T) {
	h, err := Open(Options{Root: t.TempDir(), Settings: Settings{MaxLocalBytes: 1 << 20, MinHostFreeBytes: 1}})
	if err != nil {
		t.Fatal(err)
	}
	h.Close()
	if rep := h.runGC(context.Background(), "late"); rep.Error == "" {
		t.Fatal("GC ran on a closed repository")
	}
}
