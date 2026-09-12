package backup

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/no-dal/ndl-ce/internal/backup/cdc"
)

// TestDemoIncrementalNumbers backs up a sizable workload, changes a small part,
// and reports the real instrumentation so the incremental/dedup behaviour can
// be seen from measured numbers rather than claims. It asserts the central
// outcome: a small change costs roughly the changed data, not another full
// upload.
func TestDemoIncrementalNumbers(t *testing.T) {
	repo, err := OpenRepository(t.TempDir(), testKeys(t))
	if err != nil {
		t.Fatal(err)
	}
	e := NewEngine(repo, Config{ChunkConfig: cdc.DefaultConfig(), PackTarget: DefaultPackTarget})

	src := t.TempDir()
	// A ~20 MiB workload spread over several files.
	writeFile(t, filepath.Join(src, "os/base.img"), randBytes(1001, 16<<20), 0o644)
	writeFile(t, filepath.Join(src, "app/data.db"), randBytes(1002, 4<<20), 0o644)
	writeFile(t, filepath.Join(src, "app/config.yaml"), []byte("version: 1\n"), 0o644)

	m1, _, err := e.Capture(context.Background(), CaptureOptions{Source: src, WorkloadID: "demo", WorkloadName: "SoundDock"})
	if err != nil {
		t.Fatal(err)
	}

	// Change ~200 KiB in the middle of the database file and rewrite the tiny
	// config. base.img is untouched.
	db := randBytes(1002, 4<<20)
	patch := randBytes(1003, 200<<10)
	copy(db[1<<20:], patch)
	if err := os.WriteFile(filepath.Join(src, "app/data.db"), db, 0o644); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(src, "app/config.yaml"), []byte("version: 2\n"), 0o644)

	m2, _, err := e.Capture(context.Background(), CaptureOptions{Source: src, WorkloadID: "demo", WorkloadName: "SoundDock"})
	if err != nil {
		t.Fatal(err)
	}

	report := func(tag string, m *Manifest) {
		s := m.Stats
		dedup := 0.0
		if s.ChunksTotal > 0 {
			dedup = float64(s.ChunksReused) / float64(s.ChunksTotal) * 100
		}
		fmt.Printf("[%s] files scanned=%d unchanged=%d changed=%d | bytesRead=%s logical=%s | chunks total=%d new=%d reused=%d (%.1f%% dedup) | physicalNew=%s packs=%d dur=%.0fms\n",
			tag, s.FilesScanned, s.FilesUnchanged, s.FilesChanged,
			human(s.BytesRead), human(s.LogicalBytes),
			s.ChunksTotal, s.ChunksNew, s.ChunksReused, dedup,
			human(s.PhysicalNewData), s.PacksCommitted, float64(s.DurationNanos)/1e6)
	}
	report("baseline", m1)
	report("incremental", m2)

	if m2.Stats.PhysicalNewData > m1.Stats.PhysicalNewData/4 {
		t.Fatalf("incremental stored too much new data: %d vs baseline %d", m2.Stats.PhysicalNewData, m1.Stats.PhysicalNewData)
	}
	// base.img (16 MiB) must not be reread on the incremental backup.
	if m2.Stats.BytesRead >= 16<<20 {
		t.Fatalf("incremental reread too much: %d bytes", m2.Stats.BytesRead)
	}
}

func human(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%dB", n)
	}
	div, exp := int64(unit), 0
	for x := n / unit; x >= unit; x /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f%ciB", float64(n)/float64(div), "KMGTPE"[exp])
}
