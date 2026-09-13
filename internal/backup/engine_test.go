package backup

import (
	"bytes"
	"context"
	"math/rand"
	"os"
	"path/filepath"
	"testing"

	"github.com/no-dal/ndl-ce/internal/backup/cdc"
)

func testKeys(t *testing.T) *Keys {
	t.Helper()
	master := bytes.Repeat([]byte{0x42}, 32)
	k, err := NewKeys(master)
	if err != nil {
		t.Fatalf("keys: %v", err)
	}
	return k
}

func testEngine(t *testing.T, cfg Config) *Engine {
	t.Helper()
	repo, err := OpenRepository(t.TempDir(), testKeys(t))
	if err != nil {
		t.Fatalf("open repo: %v", err)
	}
	return NewEngine(repo, cfg)
}

func writeFile(t *testing.T, path string, data []byte, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, mode); err != nil {
		t.Fatal(err)
	}
}

func randBytes(seed int64, n int) []byte {
	r := rand.New(rand.NewSource(seed))
	b := make([]byte, n)
	r.Read(b)
	return b
}

// smallCfg keeps chunks tiny so modest test files still produce many chunks and
// exercise dedup and realignment.
func smallCfg() Config {
	return Config{ChunkConfig: cdc.Config{Min: 1 << 10, Avg: 4 << 10, Max: 16 << 10}, PackTarget: 64 << 10}
}

func TestCaptureRestoreRoundTrip(t *testing.T) {
	e := testEngine(t, smallCfg())
	src := t.TempDir()
	writeFile(t, filepath.Join(src, "hello.txt"), []byte("hello world"), 0o640)
	writeFile(t, filepath.Join(src, "sub/data.bin"), randBytes(1, 300<<10), 0o600)
	if err := os.Symlink("hello.txt", filepath.Join(src, "link")); err != nil {
		t.Fatal(err)
	}

	man, state, err := e.Capture(context.Background(), CaptureOptions{
		Source: src, WorkloadID: "wl-1", WorkloadName: "SoundDock",
	})
	if err != nil {
		t.Fatalf("capture: %v", err)
	}
	if !state.LocalComplete || state.Remote != RemoteNone {
		t.Fatalf("unexpected state: %+v", state)
	}

	dst := t.TempDir()
	if err := e.Repo().Restore(context.Background(), man, RestoreOptions{Dest: dst}); err != nil {
		t.Fatalf("restore: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(dst, "hello.txt"))
	if err != nil || string(got) != "hello world" {
		t.Fatalf("hello.txt mismatch: %q %v", got, err)
	}
	gotBin, _ := os.ReadFile(filepath.Join(dst, "sub/data.bin"))
	if !bytes.Equal(gotBin, randBytes(1, 300<<10)) {
		t.Fatalf("sub/data.bin content mismatch")
	}
	link, err := os.Readlink(filepath.Join(dst, "link"))
	if err != nil || link != "hello.txt" {
		t.Fatalf("symlink mismatch: %q %v", link, err)
	}
	info, _ := os.Stat(filepath.Join(dst, "hello.txt"))
	if info.Mode().Perm() != 0o640 {
		t.Fatalf("mode not preserved: %v", info.Mode())
	}
}

func TestIncrementalReusesUnchangedFiles(t *testing.T) {
	e := testEngine(t, smallCfg())
	src := t.TempDir()
	big := randBytes(2, 2<<20)
	writeFile(t, filepath.Join(src, "big.bin"), big, 0o644)
	writeFile(t, filepath.Join(src, "small.txt"), []byte("v1"), 0o644)

	m1, _, err := e.Capture(context.Background(), CaptureOptions{Source: src, WorkloadID: "wl", WorkloadName: "app"})
	if err != nil {
		t.Fatal(err)
	}
	if m1.Stats.ChunksNew == 0 {
		t.Fatal("first backup should create chunks")
	}

	// Change only the small file. big.bin is untouched.
	writeFile(t, filepath.Join(src, "small.txt"), []byte("v2 changed"), 0o644)
	m2, _, err := e.Capture(context.Background(), CaptureOptions{Source: src, WorkloadID: "wl", WorkloadName: "app"})
	if err != nil {
		t.Fatal(err)
	}
	if m2.Stats.FilesUnchanged < 1 {
		t.Fatalf("expected big.bin reported unchanged via cache, stats=%+v", m2.Stats)
	}
	// The metadata cache must avoid rereading big.bin entirely.
	if m2.Stats.BytesRead >= int64(len(big)) {
		t.Fatalf("second backup reread too much (%d bytes); cache not effective", m2.Stats.BytesRead)
	}
	if m2.Stats.PhysicalNewData >= m1.Stats.PhysicalNewData {
		t.Fatalf("second backup stored as much new data as the first: %d vs %d", m2.Stats.PhysicalNewData, m1.Stats.PhysicalNewData)
	}
}

func TestMiddleEditRealignsChunks(t *testing.T) {
	e := testEngine(t, smallCfg())
	src := t.TempDir()
	data := randBytes(3, 3<<20)
	writeFile(t, filepath.Join(src, "f.bin"), data, 0o644)
	if _, _, err := e.Capture(context.Background(), CaptureOptions{Source: src, WorkloadID: "wl", WorkloadName: "app"}); err != nil {
		t.Fatal(err)
	}

	// Insert bytes in the middle and force a reread by rewriting the file.
	edited := make([]byte, 0, len(data)+64)
	mid := len(data) / 2
	edited = append(edited, data[:mid]...)
	edited = append(edited, randBytes(77, 64)...)
	edited = append(edited, data[mid:]...)
	writeFile(t, filepath.Join(src, "f.bin"), edited, 0o644)

	m2, _, err := e.Capture(context.Background(), CaptureOptions{Source: src, WorkloadID: "wl", WorkloadName: "app"})
	if err != nil {
		t.Fatal(err)
	}
	// Most chunks around the untouched regions should be reused thanks to CDC.
	if m2.Stats.ChunksReused == 0 || m2.Stats.ChunksReused < m2.Stats.ChunksNew {
		t.Fatalf("expected substantial chunk reuse after middle edit, stats=%+v", m2.Stats)
	}
}

func TestCrossWorkloadDedup(t *testing.T) {
	e := testEngine(t, smallCfg())
	shared := randBytes(4, 1<<20)

	srcA := t.TempDir()
	writeFile(t, filepath.Join(srcA, "os/base.img"), shared, 0o644)
	if _, _, err := e.Capture(context.Background(), CaptureOptions{Source: srcA, WorkloadID: "a", WorkloadName: "debianA"}); err != nil {
		t.Fatal(err)
	}

	srcB := t.TempDir()
	writeFile(t, filepath.Join(srcB, "os/base.img"), shared, 0o644)
	m2, _, err := e.Capture(context.Background(), CaptureOptions{Source: srcB, WorkloadID: "b", WorkloadName: "debianB"})
	if err != nil {
		t.Fatal(err)
	}
	if m2.Stats.ChunksNew != 0 {
		t.Fatalf("identical data across workloads should be fully deduped, got %d new chunks", m2.Stats.ChunksNew)
	}
}

func TestDeletionAndHistoricalRestore(t *testing.T) {
	e := testEngine(t, smallCfg())
	src := t.TempDir()
	writeFile(t, filepath.Join(src, "keep.txt"), []byte("keep"), 0o644)
	writeFile(t, filepath.Join(src, "gone.txt"), []byte("temporary"), 0o644)
	m1, _, err := e.Capture(context.Background(), CaptureOptions{Source: src, WorkloadID: "wl", WorkloadName: "app"})
	if err != nil {
		t.Fatal(err)
	}

	if err := os.Remove(filepath.Join(src, "gone.txt")); err != nil {
		t.Fatal(err)
	}
	m2, _, err := e.Capture(context.Background(), CaptureOptions{Source: src, WorkloadID: "wl", WorkloadName: "app"})
	if err != nil {
		t.Fatal(err)
	}

	latest := t.TempDir()
	if err := e.Repo().Restore(context.Background(), m2, RestoreOptions{Dest: latest}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(latest, "gone.txt")); !os.IsNotExist(err) {
		t.Fatalf("deleted file must be absent in latest restore")
	}

	earlier := t.TempDir()
	if err := e.Repo().Restore(context.Background(), m1, RestoreOptions{Dest: earlier}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(earlier, "gone.txt")); err != nil {
		t.Fatalf("earlier restore point must still contain the file: %v", err)
	}
}

func TestTamperedChunkFailsAuthentication(t *testing.T) {
	e := testEngine(t, smallCfg())
	src := t.TempDir()
	writeFile(t, filepath.Join(src, "f.bin"), randBytes(5, 200<<10), 0o644)
	man, _, err := e.Capture(context.Background(), CaptureOptions{Source: src, WorkloadID: "wl", WorkloadName: "app"})
	if err != nil {
		t.Fatal(err)
	}
	// Corrupt a pack file on disk.
	packs, _ := e.Repo().packList()
	if len(packs) == 0 {
		t.Fatal("expected a pack")
	}
	packPath := filepath.Join(e.Repo().packDir(), packs[0]+".pack")
	raw, _ := os.ReadFile(packPath)
	raw[len(raw)/2] ^= 0xff
	if err := os.WriteFile(packPath, raw, 0o640); err != nil {
		t.Fatal(err)
	}
	dst := t.TempDir()
	if err := e.Repo().Restore(context.Background(), man, RestoreOptions{Dest: dst}); err == nil {
		t.Fatalf("restore of a tampered pack must fail authentication")
	}
}
