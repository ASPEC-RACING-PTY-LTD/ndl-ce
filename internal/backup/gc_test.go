package backup

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestSharedChunkSurvivesRestorePointDeletion(t *testing.T) {
	e := testEngine(t, smallCfg())
	shared := randBytes(11, 1<<20)

	srcA := t.TempDir()
	writeFile(t, filepath.Join(srcA, "base.img"), shared, 0o644)
	mA, sA, err := e.Capture(context.Background(), CaptureOptions{Source: srcA, WorkloadID: "a", WorkloadName: "wlA"})
	if err != nil {
		t.Fatal(err)
	}

	srcB := t.TempDir()
	writeFile(t, filepath.Join(srcB, "base.img"), shared, 0o644)
	mB, _, err := e.Capture(context.Background(), CaptureOptions{Source: srcB, WorkloadID: "b", WorkloadName: "wlB"})
	if err != nil {
		t.Fatal(err)
	}

	// Delete workload A's restore point, then GC. B still references the shared
	// chunks, so nothing may be reclaimed and B must still restore.
	if err := e.Repo().DeleteRestorePoint(sA.Namespace, sA.BackupID); err != nil {
		t.Fatal(err)
	}
	stats, err := e.Repo().CollectGarbage()
	if err != nil {
		t.Fatal(err)
	}
	if stats.PacksDeleted != 0 {
		t.Fatalf("shared chunks must not be collected while B references them; deleted %d packs", stats.PacksDeleted)
	}
	dst := t.TempDir()
	if err := e.Repo().Restore(context.Background(), mB, RestoreOptions{Dest: dst}); err != nil {
		t.Fatalf("B must still restore after A deletion+GC: %v", err)
	}
	_ = mA

	// Now delete B too and GC: with no references left, packs are reclaimed.
	if err := e.Repo().DeleteRestorePoint(mB.Namespace, mB.BackupID); err != nil {
		t.Fatal(err)
	}
	stats2, err := e.Repo().CollectGarbage()
	if err != nil {
		t.Fatal(err)
	}
	if stats2.PacksDeleted == 0 {
		t.Fatalf("expected packs to be reclaimed once unreferenced")
	}
	size, _ := e.Repo().SizeOnDisk()
	if size != 0 {
		t.Fatalf("expected empty repository after full GC, size=%d", size)
	}
}

func TestRetentionSelectExpired(t *testing.T) {
	day := int64(24 * time.Hour)
	base := time.Date(2026, 9, 12, 3, 0, 0, 0, time.UTC).UnixNano()
	var states []PointState
	for i := 0; i < 5; i++ {
		states = append(states, PointState{
			BackupID:    "b" + itoa(i),
			CreatedAtNS: base - int64(i)*day,
		})
	}
	// Keep 2 daily buckets: the two newest distinct days survive.
	expired := RetentionPolicy{Daily: 2}.SelectExpired(states)
	if len(expired) != 3 {
		t.Fatalf("expected 3 expired with Daily=2 over 5 days, got %d", len(expired))
	}
	for _, s := range expired {
		if s.BackupID == "b0" || s.BackupID == "b1" {
			t.Fatalf("newest two days must be kept, but %s expired", s.BackupID)
		}
	}
}
