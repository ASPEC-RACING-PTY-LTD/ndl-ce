package backup

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// TestRepeatedCyclesPlateau runs many capture+upload cycles and asserts the
// goroutine count reaches a plateau and staging space is reclaimed. This guards
// against the historical failure where the backup path leaked goroutines and
// grew unbounded.
func TestRepeatedCyclesPlateau(t *testing.T) {
	e := testEngine(t, smallCfg())
	target := NewMemTarget()
	src := t.TempDir()
	writeFile(t, filepath.Join(src, "f.bin"), randBytes(41, 1<<20), 0o644)

	settle := func() {
		runtime.GC()
		time.Sleep(50 * time.Millisecond)
		runtime.GC()
	}

	// Warm up one cycle so lazy initialization is not counted.
	runOneCycle(t, e, target, src, 0)
	settle()
	baseline := runtime.NumGoroutine()

	const cycles = 25
	for i := 1; i <= cycles; i++ {
		runOneCycle(t, e, target, src, i)
	}
	settle()
	after := runtime.NumGoroutine()
	if after > baseline+3 {
		t.Fatalf("goroutine count did not plateau: baseline=%d after=%d", baseline, after)
	}

	// No temporary staging files should remain anywhere in the repository.
	assertNoTempFiles(t, e.Repo().root)

	var before, afterMem runtime.MemStats
	runtime.ReadMemStats(&before)
	fdBefore := openFDCount(t)
	for i := 0; i < 8; i++ {
		runOneCycle(t, e, target, src, 100+i)
	}
	runtime.GC()
	runtime.ReadMemStats(&afterMem)
	fdAfter := openFDCount(t)
	if fdAfter > fdBefore+16 {
		t.Fatalf("open file descriptors grew: before=%d after=%d", fdBefore, fdAfter)
	}
	if afterMem.HeapAlloc > before.HeapAlloc+32<<20 {
		t.Fatalf("heap alloc grew without bound: before=%d after=%d", before.HeapAlloc, afterMem.HeapAlloc)
	}
}

func openFDCount(t *testing.T) int {
	t.Helper()
	var lim unix.Rlimit
	if err := unix.Getrlimit(unix.RLIMIT_NOFILE, &lim); err != nil {
		t.Skip(err)
	}
	n := 0
	max := int(lim.Cur)
	if max > 4096 {
		max = 4096
	}
	for fd := 0; fd < max; fd++ {
		var stat unix.Stat_t
		if err := unix.Fstat(fd, &stat); err == nil {
			n++
		}
	}
	return n
}

func runOneCycle(t *testing.T, e *Engine, target *MemTarget, src string, i int) {
	t.Helper()
	// Change the file each cycle so real work happens.
	writeFile(t, filepath.Join(src, "f.bin"), randBytes(int64(1000+i), 1<<20), 0o644)
	_, state, err := e.Capture(context.Background(), CaptureOptions{Source: src, WorkloadID: "wl", WorkloadName: "app"})
	if err != nil {
		t.Fatal(err)
	}
	q := NewUploadQueue(e.Repo(), target, 3)
	if err := q.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := q.EnqueueBackup(state); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 10*time.Second, func() bool { return q.PendingJobs() == 0 })
	q.Stop()
}

func assertNoTempFiles(t *testing.T, root string) {
	t.Helper()
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && filepath.Ext(path) == ".tmp" {
			return fmt.Errorf("leftover temp file: %s", path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
