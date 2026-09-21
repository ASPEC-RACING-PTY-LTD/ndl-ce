package backuphost

import (
	"context"
	"encoding/json"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/no-dal/ndl-ce/internal/backup"
)

func TestHostHonorsCaptureConcurrencyAndPerWorkloadLock(t *testing.T) {
	h, err := Open(Options{Root: t.TempDir(), Settings: Settings{
		MaxLocalBytes: 1 << 30, MinHostFreeBytes: 1, CaptureConcurrency: 2,
	}})
	if err != nil {
		t.Fatal(err)
	}
	var peak atomic.Int32
	var wg sync.WaitGroup
	errCh := make(chan error, 2)
	for i, name := range []string{"wl-a", "wl-b"} {
		src := t.TempDir()
		mustWrite(t, filepath.Join(src, "f"), []byte{byte('a' + i)})
		wg.Add(1)
		go func(name, src string) {
			defer wg.Done()
			_, err := h.Handle(context.Background(), ActionCapture, src, mustJSON(Request{
				WorkloadID: name, WorkloadName: name, CaptureMode: backup.CaptureModeFull,
				Settings: Settings{CaptureConcurrency: 2, MaxLocalBytes: 1 << 30, MinHostFreeBytes: 1},
			}))
			h.mu.Lock()
			if n := int32(len(h.busy)); n > peak.Load() {
				peak.Store(n)
			}
			h.mu.Unlock()
			errCh <- err
		}(name, src)
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		if err != nil {
			t.Fatal(err)
		}
	}

	src := t.TempDir()
	mustWrite(t, filepath.Join(src, "f"), []byte("x"))
	h.mu.Lock()
	h.busy["locked"] = struct{}{}
	h.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 80*time.Millisecond)
	defer cancel()
	_, err = h.Handle(ctx, ActionCapture, src, mustJSON(Request{WorkloadID: "locked", WorkloadName: "locked"}))
	if err == nil {
		t.Fatal("duplicate workload capture must be rejected")
	}
}

func TestHostDoesNotClaimAppConsistencyWithoutHook(t *testing.T) {
	src := t.TempDir()
	mustWrite(t, filepath.Join(src, "f"), []byte("x"))
	h, err := Open(Options{Root: t.TempDir(), Settings: Settings{MaxLocalBytes: 1 << 20, MinHostFreeBytes: 1}})
	if err != nil {
		t.Fatal(err)
	}
	res, err := h.Handle(context.Background(), ActionCapture, src, mustJSON(Request{
		WorkloadID: "ct", WorkloadName: "ct", Unit: "nodal-ct@ct.service", CaptureMode: backup.CaptureModeFull,
	}))
	if err != nil {
		t.Fatal(err)
	}
	var out Result
	if err := json.Unmarshal([]byte(res.Extra), &out); err != nil {
		t.Fatal(err)
	}
	if out.Consistency != backup.ConsistencyCrash {
		t.Fatalf("consistency %s", out.Consistency)
	}
	if out.ConsistencyInfo.HookRan {
		t.Fatal("hook must not have run")
	}
}
