package diskguard

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const tib = int64(1) << 40

func TestEvaluateUsesPercentAndFreeFloors(t *testing.T) {
	p := DefaultPolicy()
	cases := []struct {
		total, avail int64
		want         Level
	}{
		{868 * gib, 396 * gib, LevelOK},       // the host after cleanup
		{868 * gib, 100 * gib, LevelWarning},  // 88% used
		{868 * gib, 60 * gib, LevelCritical},  // 93% used
		{868 * gib, 10 * gib, LevelEmergency}, // 99% used
		{868 * gib, 0, LevelEmergency},        // the outage
		{4 * tib, 300 * gib, LevelOK},         // big disk, plenty left
		{4 * tib, 150 * gib, LevelWarning},    // big disk, under the 200 GiB cap
		{4 * tib, 50 * gib, LevelCritical},    // big disk, under the 75 GiB cap
		{4 * tib, 10 * gib, LevelEmergency},   // big disk, under the 20 GiB cap
		{20 * gib, 15 * gib, LevelOK},         // small disk, room left
		{20 * gib, 8 * gib, LevelWarning},     // small disk, under the 10 GiB floor
		{20 * gib, 1 * gib, LevelEmergency},   // small disk, under the 2 GiB floor
	}
	for _, c := range cases {
		if got := p.Evaluate(c.total, c.avail); got != c.want {
			t.Errorf("Evaluate(%d GiB, %d GiB) = %s, want %s", c.total/gib, c.avail/gib, got, c.want)
		}
	}
}

// fakeDisk is a guard over a temp dir with a controllable free size.
func fakeDisk(t *testing.T, total, avail int64) (*Guard, string, *atomic.Int64) {
	t.Helper()
	root := t.TempDir()
	free := new(atomic.Int64)
	free.Store(avail)
	g := New(filepath.Join(root, "reserve", "ballast"), Watch{Path: root, Role: "data"}, Watch{Path: root, Role: "postgresql"})
	g.stat = func(p string) (statInfo, error) {
		if strings.HasPrefix(p, root) {
			return statInfo{device: 1, total: total, avail: free.Load()}, nil
		}
		return statInfo{device: 2, total: 4 * tib, avail: 3 * tib}, nil
	}
	g.allocate = func(p string, size int64) error {
		free.Add(-size)
		return os.WriteFile(p, []byte("reserve"), 0o600)
	}
	return g, root, free
}

func TestAllowRefusesBulkWritesOnlyOnTheFullDisk(t *testing.T) {
	g, root, free := fakeDisk(t, 868*gib, 300*gib)
	if err := g.Allow("Backup", filepath.Join(root, "backup-staging", "new"), 0); err != nil {
		t.Fatalf("plenty of space: %v", err)
	}
	if err := g.Allow("Backup", filepath.Join(root, "x"), 290*gib); err == nil {
		t.Fatal("a write that would push the disk past critical must be refused")
	}
	free.Store(60 * gib)
	err := g.Allow("Backup", filepath.Join(root, "backup-staging"), 0)
	var full ErrDiskFull
	if !errors.As(err, &full) || full.Level != LevelCritical || !strings.Contains(err.Error(), "Free space under Storage") {
		t.Fatalf("critical disk must refuse with guidance: %v", err)
	}
	if err := g.Allow("Copy to HDD pool", "/mnt/hdd-pool/volume", 0); err != nil {
		t.Fatalf("a separate disk must not be gated by the full one: %v", err)
	}
	if err := g.Allow("Install", "", 0); err == nil {
		t.Fatal("an op without a destination is judged by every watched disk")
	}
	if fs := g.Status().Filesystems; len(fs) != 1 || len(fs[0].Roles) != 2 {
		t.Fatalf("paths on one filesystem must be reported once: %+v", fs)
	}
}

func TestReserveIsHeldThenReleasedInAnEmergency(t *testing.T) {
	g, _, free := fakeDisk(t, 868*gib, 300*gib)
	st := g.Refresh()
	if !st.ReserveHeld {
		t.Fatalf("reserve must be created with room to spare: %+v", st)
	}
	free.Store(1 * gib)
	st = g.Refresh()
	if st.ReserveHeld || !strings.Contains(st.ReserveNote, "Released") {
		t.Fatalf("emergency must release the reserve: %+v", st)
	}
	if _, err := os.Stat(g.ReservePath); !os.IsNotExist(err) {
		t.Fatal("reserve file must be gone")
	}
	free.Store(50 * gib) // critical-free floor passed, but not back to OK
	if st = g.Refresh(); st.ReserveHeld {
		t.Fatal("reserve must not be retaken until the disk is healthy")
	}
	free.Store(300 * gib)
	if st = g.Refresh(); !st.ReserveHeld {
		t.Fatal("reserve must come back once the disk is healthy")
	}
	if st, err := g.ReleaseReserve(); err != nil || st.ReserveHeld {
		t.Fatalf("manual release: %+v %v", st, err)
	}
}

func TestReserveUnsupportedFilesystemIsReported(t *testing.T) {
	g, _, _ := fakeDisk(t, 868*gib, 300*gib)
	g.allocate = func(string, int64) error { return errReserveUnsupported }
	st := g.Refresh()
	if st.ReserveHeld || !strings.Contains(st.ReserveNote, "cannot preallocate") {
		t.Fatalf("%+v", st)
	}
}

func touch(t *testing.T, p string, age time.Duration) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	when := time.Now().Add(-age)
	if err := os.Chtimes(p, when, when); err != nil {
		t.Fatal(err)
	}
}

func TestCleanRemovesOnlyDisposableFiles(t *testing.T) {
	g, root, _ := fakeDisk(t, 868*gib, 300*gib)
	ctx := context.Background()
	touch(t, filepath.Join(root, "update-checkpoints", "old.tar"), 72*time.Hour)
	touch(t, filepath.Join(root, "update-checkpoints", "old.sql"), 72*time.Hour)
	touch(t, filepath.Join(root, "update-checkpoints", "new.tar"), time.Hour)
	touch(t, filepath.Join(root, "update-checkpoints", "new.sql"), time.Hour)
	touch(t, filepath.Join(root, "backup-staging", "stale", "disk.qcow2"), 48*time.Hour)
	touch(t, filepath.Join(root, "backup-staging", "running"), time.Minute)
	touch(t, filepath.Join(root, "backup-staging", "protected"), 48*time.Hour)
	touch(t, filepath.Join(root, "storage", "local", "volumes", "vm.qcow2"), 1000*time.Hour)
	touch(t, filepath.Join(root, "backup-repo", "packs", "p1"), 1000*time.Hour)

	res, err := g.Clean(ctx, root, CleanCheckpoints, nil)
	if err != nil || res.Kept != 1 || len(res.Removed) != 2 {
		t.Fatalf("%+v %v", res, err)
	}
	if _, err := os.Stat(filepath.Join(root, "update-checkpoints", "new.tar")); err != nil {
		t.Fatal("newest checkpoint must be kept")
	}
	res, err = g.Clean(ctx, root, CleanBackupStaging, []string{"protected"})
	if err != nil || len(res.Removed) != 1 || res.Removed[0] != "stale" {
		t.Fatalf("only stale, unprotected staging may go: %+v %v", res, err)
	}
	for _, id := range []string{"storage", "backup-repo", "backups", "gameservers", "game-backups", "../etc"} {
		if _, err := g.Clean(ctx, root, id, nil); err == nil {
			t.Fatalf("%s must never be cleaned", id)
		}
	}
	for _, keep := range []string{"storage/local/volumes/vm.qcow2", "backup-repo/packs/p1", "backup-staging/running"} {
		if _, err := os.Stat(filepath.Join(root, keep)); err != nil {
			t.Fatalf("%s must be untouched", keep)
		}
	}
	u := g.Measure(ctx, root, 5*time.Second)
	if len(u.Categories) == 0 || u.FS == nil {
		t.Fatalf("%+v", u)
	}
}

func TestGuardIsSafeForConcurrentUse(t *testing.T) {
	g, root, _ := fakeDisk(t, 868*gib, 300*gib)
	done := make(chan struct{})
	for i := 0; i < 8; i++ {
		go func() {
			defer func() { done <- struct{}{} }()
			for j := 0; j < 50; j++ {
				g.Refresh()
				_ = g.Allow("Backup", root, 0)
				_ = g.Status()
			}
		}()
	}
	go func() { _, _ = g.ReleaseReserve() }()
	for i := 0; i < 8; i++ {
		<-done
	}
}
