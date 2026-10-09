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
		{868 * gib, 396 * gib, LevelOK},      // the host after cleanup
		{868 * gib, 100 * gib, LevelWarning}, // 88% used
		{868 * gib, 25 * gib, LevelCritical}, // under the 30 GiB cap (the reserve holds 50 more)
		{868 * gib, 5 * gib, LevelEmergency}, // under the 10 GiB cap
		{868 * gib, 0, LevelEmergency},       // the outage
		{4 * tib, 300 * gib, LevelOK},        // big disk, plenty left
		{4 * tib, 150 * gib, LevelWarning},   // big disk, under the 200 GiB cap
		{4 * tib, 25 * gib, LevelCritical},   // big disk, under the 30 GiB cap
		{4 * tib, 5 * gib, LevelEmergency},   // big disk, under the 10 GiB cap
		{20 * gib, 15 * gib, LevelOK},        // small disk, room left
		{20 * gib, 8 * gib, LevelWarning},    // small disk, under the 10 GiB floor
		{20 * gib, 1 * gib, LevelEmergency},  // small disk, under the 2 GiB floor
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
		var had int64
		if fi, err := os.Stat(p); err == nil {
			had = fi.Size()
		}
		free.Add(had - size)
		if err := os.WriteFile(p, nil, 0o600); err != nil && had == 0 {
			return err
		}
		return os.Truncate(p, size)
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
	free.Store(25 * gib)
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

func TestReserveHolds50GiBAndIsReleasedInAnEmergency(t *testing.T) {
	g, _, free := fakeDisk(t, 868*gib, 300*gib)
	st := g.Refresh()
	if !st.ReserveHeld || st.ReserveBytes != 50*gib || st.ReserveTargetBytes != 50*gib {
		t.Fatalf("50 GiB must be reserved with room to spare: %+v", st)
	}
	if free.Load() != 250*gib {
		t.Fatalf("the reserve must take real space: %d GiB free", free.Load()/gib)
	}
	free.Store(5 * gib)
	st = g.Refresh()
	if st.ReserveHeld || !strings.Contains(st.ReserveNote, "Released") {
		t.Fatalf("emergency must release the reserve: %+v", st)
	}
	if _, err := os.Stat(g.ReservePath); !os.IsNotExist(err) {
		t.Fatal("reserve file must be gone")
	}
	free.Store(60 * gib) // room again, but not healthy yet
	if st = g.Refresh(); st.ReserveHeld {
		t.Fatal("after an emergency the reserve waits until the disk is healthy")
	}
	free.Store(300 * gib)
	if st = g.Refresh(); !st.ReserveHeld || st.ReserveBytes != 50*gib {
		t.Fatalf("reserve must come back once the disk is healthy: %+v", st)
	}
	if st, err := g.ReleaseReserve(); err != nil || st.ReserveHeld {
		t.Fatalf("manual release: %+v %v", st, err)
	}
	if st = g.Refresh(); st.ReserveHeld {
		t.Fatal("a manually released reserve stays released for the hold-off")
	}
}

func TestReserveGrowsAsSpaceFreesUp(t *testing.T) {
	// 868 GiB disk with 104 GiB free and a 30 GiB critical threshold:
	// the full 50 GiB fits and leaves 54 GiB.
	g, _, free := fakeDisk(t, 868*gib, 104*gib)
	if st := g.Refresh(); st.ReserveBytes != 50*gib {
		t.Fatalf("%+v", st)
	}
	// A fuller disk holds what fits above the critical threshold.
	g2, _, free2 := fakeDisk(t, 868*gib, 60*gib)
	st := g2.Refresh()
	if st.ReserveBytes != 29*gib || !strings.Contains(st.ReserveNote, "grows as space frees up") {
		t.Fatalf("partial reserve: %+v (%d GiB free)", st, free2.Load()/gib)
	}
	free2.Add(40 * gib)
	if st = g2.Refresh(); st.ReserveBytes != 50*gib {
		t.Fatalf("reserve must grow to its target: %+v", st)
	}
	// Small disks keep at most 10%.
	g3, _, _ := fakeDisk(t, 100*gib, 80*gib)
	if st := g3.Refresh(); st.ReserveTargetBytes != 10*gib || st.ReserveBytes != 10*gib {
		t.Fatalf("small disk reserve: %+v", st)
	}
	_ = free
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
