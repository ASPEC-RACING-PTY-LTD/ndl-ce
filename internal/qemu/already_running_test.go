package qemu

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestAlreadyRunningFalseWhenSkipHostCmds(t *testing.T) {
	e := &Engine{DataDir: t.TempDir(), SkipHostCmds: true}
	id := "eeeeeeee-eeee-4eee-8eee-eeeeeeeeeeee"
	if e.AlreadyRunning(context.Background(), id) {
		t.Fatal("AlreadyRunning must be false when SkipHostCmds")
	}
	if _, err := e.Prepare(Spec{
		WorkloadID: id,
		VolumeID:   "vol-already",
		DiskPath:   "/var/lib/ndl/storage/p/volumes/vm-disk/" + id + ".qcow2",
		Accel:      "tcg",
		Machine:    DefaultMachine,
	}); err != nil {
		t.Fatal(err)
	}
	if e.AlreadyRunning(context.Background(), id) {
		t.Fatal("prepared fixture must not report AlreadyRunning when SkipHostCmds")
	}
	if err := e.Start(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	if e.AlreadyRunning(context.Background(), id) {
		t.Fatal("Start with SkipHostCmds must not report the unit as running")
	}
}

func TestDiscoverObserveFixtureStoppedNotSilentRunning(t *testing.T) {
	e := &Engine{DataDir: t.TempDir(), SkipHostCmds: true}
	id := "ffffffff-ffff-4fff-8fff-ffffffffffff"
	if _, err := e.Prepare(Spec{
		WorkloadID: id,
		VolumeID:   "vol-observe",
		DiskPath:   "/var/lib/ndl/storage/p/volumes/vm-disk/" + id + ".qcow2",
		Accel:      "tcg",
		Machine:    DefaultMachine,
	}); err != nil {
		t.Fatal(err)
	}

	obs := e.Observe(context.Background(), id)
	if obs.Status != StatusStopped {
		t.Fatalf("Observe fixture status=%s want %s", obs.Status, StatusStopped)
	}
	if obs.Status == StatusRunning {
		t.Fatal("Observe must not silently report running")
	}
	if obs.UnitActive {
		t.Fatal("Observe fixture must not report unit_active")
	}
	if obs.Reason != "fixture" {
		t.Fatalf("Observe reason=%q want fixture", obs.Reason)
	}

	disc, err := e.Discover(id)
	if err != nil {
		t.Fatal(err)
	}
	if disc.Status != StatusStopped {
		t.Fatalf("Discover fixture status=%s want %s", disc.Status, StatusStopped)
	}
	if disc.Status == StatusRunning {
		t.Fatal("Discover must not silently report running")
	}
	if disc.UnitActive {
		t.Fatal("Discover fixture must not report unit_active")
	}
	if disc.Reason != "fixture" {
		t.Fatalf("Discover reason=%q want fixture", disc.Reason)
	}
}

func TestEnsureDirsAddsOtherTraverse(t *testing.T) {
	root := t.TempDir()
	workloads := filepath.Join(root, "workloads")
	if err := os.MkdirAll(workloads, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(workloads, 0o750); err != nil {
		t.Fatal(err)
	}
	e := &Engine{DataDir: root, SkipHostCmds: true}
	id := "99999999-9999-4999-8999-999999999999"
	if err := e.ensureDirs(id); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(workloads)
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm()&0o001 == 0 {
		t.Fatalf("workloads must be traversable by ndl-qemu, got %o", st.Mode().Perm())
	}
}
