package backup

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/no-dal/ndl-ce/internal/backup/cdc"
)

// TestLargeIncrementalChangedDataScale is an optional heavier measurement
// (about 256 MiB baseline). Skip with -short so CI stays fast.
func TestLargeIncrementalChangedDataScale(t *testing.T) {
	if testing.Short() {
		t.Skip("large incremental measurement is skipped in short mode")
	}
	repo, err := OpenRepository(t.TempDir(), testKeys(t))
	if err != nil {
		t.Fatal(err)
	}
	e := NewEngine(repo, Config{ChunkConfig: cdc.DefaultConfig(), PackTarget: DefaultPackTarget})
	src := t.TempDir()
	writeFile(t, filepath.Join(src, "os/base.img"), randBytes(2001, 192<<20), 0o644)
	writeFile(t, filepath.Join(src, "app/data.db"), randBytes(2002, 64<<20), 0o644)

	m1, _, err := e.Capture(context.Background(), CaptureOptions{Source: src, WorkloadID: "large", WorkloadName: "large"})
	if err != nil {
		t.Fatal(err)
	}
	db := randBytes(2002, 64<<20)
	copy(db[8<<20:], randBytes(2003, 1<<20))
	if err := os.WriteFile(filepath.Join(src, "app/data.db"), db, 0o644); err != nil {
		t.Fatal(err)
	}
	m2, _, err := e.Capture(context.Background(), CaptureOptions{Source: src, WorkloadID: "large", WorkloadName: "large"})
	if err != nil {
		t.Fatal(err)
	}
	fmt.Printf("[large-baseline] logical=%s physicalNew=%s bytesRead=%s\n", human(m1.Stats.LogicalBytes), human(m1.Stats.PhysicalNewData), human(m1.Stats.BytesRead))
	fmt.Printf("[large-incremental] logical=%s physicalNew=%s bytesRead=%s reused=%d new=%d\n",
		human(m2.Stats.LogicalBytes), human(m2.Stats.PhysicalNewData), human(m2.Stats.BytesRead), m2.Stats.ChunksReused, m2.Stats.ChunksNew)
	if m2.Stats.PhysicalNewData > 16<<20 {
		t.Fatalf("a 1MiB edit stored %s of new data; expected changed-data scale", human(m2.Stats.PhysicalNewData))
	}
	if m2.Stats.BytesRead > 80<<20 {
		t.Fatalf("incremental reread too much: %s", human(m2.Stats.BytesRead))
	}
}
