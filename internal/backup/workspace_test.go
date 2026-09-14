package backup

import (
	"context"
	"errors"
	"math"
	"path/filepath"
	"testing"
)

func TestWorkspaceCeilingBlocksNewData(t *testing.T) {
	root := t.TempDir()
	keys := testKeys(t)
	repo, err := OpenRepository(root, keys)
	if err != nil {
		t.Fatal(err)
	}
	// First capture with a generous engine populates the repository.
	e1 := NewEngine(repo, smallCfg())
	src := t.TempDir()
	writeFile(t, filepath.Join(src, "f.bin"), randBytes(31, 512<<10), 0o644)
	if _, _, err := e1.Capture(context.Background(), CaptureOptions{Source: src, WorkloadID: "wl", WorkloadName: "app"}); err != nil {
		t.Fatal(err)
	}

	// Reopen with a tiny ceiling: a further capture must refuse before writing.
	repo2, err := OpenRepository(root, keys)
	if err != nil {
		t.Fatal(err)
	}
	cfg := smallCfg()
	cfg.MaxLocalBytes = 1
	e2 := NewEngine(repo2, cfg)
	src2 := t.TempDir()
	writeFile(t, filepath.Join(src2, "g.bin"), randBytes(32, 512<<10), 0o644)
	_, _, err = e2.Capture(context.Background(), CaptureOptions{Source: src2, WorkloadID: "wl2", WorkloadName: "app2"})
	if !errors.Is(err, ErrWorkspaceFull) {
		t.Fatalf("expected ErrWorkspaceFull, got %v", err)
	}
}

func TestMinHostFreeReserveBlocks(t *testing.T) {
	repo, err := OpenRepository(t.TempDir(), testKeys(t))
	if err != nil {
		t.Fatal(err)
	}
	cfg := smallCfg()
	cfg.MinHostFreeBytes = math.MaxInt64 // no host can satisfy this
	e := NewEngine(repo, cfg)
	src := t.TempDir()
	writeFile(t, filepath.Join(src, "f.bin"), []byte("data"), 0o644)
	_, _, err = e.Capture(context.Background(), CaptureOptions{Source: src, WorkloadID: "wl", WorkloadName: "app"})
	if !errors.Is(err, ErrWorkspaceFull) {
		t.Fatalf("expected ErrWorkspaceFull for free-space reserve, got %v", err)
	}
}
