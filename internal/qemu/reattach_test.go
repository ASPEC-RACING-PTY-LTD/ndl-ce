package qemu

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestListAppliedIDsFindsUUIDDirsAndIgnoresJunk(t *testing.T) {
	root := t.TempDir()
	e := &Engine{DataDir: root, SkipHostCmds: true}
	id := "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
	if _, err := e.Prepare(Spec{
		WorkloadID: id,
		VolumeID:   "vol-reattach",
		DiskPath:   "/var/lib/ndl/storage/p/volumes/vm-disk/" + id + ".qcow2",
		Accel:      "tcg",
		Machine:    DefaultMachine,
	}); err != nil {
		t.Fatal(err)
	}

	workloads := filepath.Join(root, "workloads")
	for _, junk := range []string{"not-a-uuid", "lab", "nodal-vm@x", "tmp"} {
		if err := os.MkdirAll(filepath.Join(workloads, junk), 0o750); err != nil {
			t.Fatal(err)
		}
	}
	orphan := "cccccccc-cccc-4ccc-8ccc-cccccccccccc"
	if err := os.MkdirAll(filepath.Join(workloads, orphan), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workloads, "readme.txt"), []byte("ignore"), 0o640); err != nil {
		t.Fatal(err)
	}

	got := e.ListAppliedIDs()
	if len(got) != 1 || got[0] != id {
		t.Fatalf("ListAppliedIDs=%v want only %s", got, id)
	}
}

func TestReattachAppliedDoesNotStartQEMUWhenSkipHostCmds(t *testing.T) {
	root := t.TempDir()
	e := &Engine{DataDir: root, SkipHostCmds: true}
	id := "dddddddd-dddd-4ddd-8ddd-dddddddddddd"
	if _, err := e.Prepare(Spec{
		WorkloadID: id,
		VolumeID:   "vol-reattach-skip",
		DiskPath:   "/var/lib/ndl/storage/p/volumes/vm-disk/" + id + ".qcow2",
		Accel:      "tcg",
		Machine:    DefaultMachine,
	}); err != nil {
		t.Fatal(err)
	}
	if e.AlreadyRunning(context.Background(), id) {
		t.Fatal("AlreadyRunning must be false when SkipHostCmds")
	}

	errs := e.ReattachApplied(context.Background())
	if len(errs) != 0 {
		t.Fatalf("ReattachApplied must not start QEMU or reconnect when SkipHostCmds: %v", errs)
	}

	obs := e.Observe(context.Background(), id)
	if obs.Status != StatusStopped {
		t.Fatalf("fixture observe after ReattachApplied must stay stopped, got %s", obs.Status)
	}
	if obs.UnitActive {
		t.Fatal("ReattachApplied must not mark the unit active")
	}
	if obs.Status == StatusRunning {
		t.Fatal("ReattachApplied must not silently start QEMU")
	}
	if _, err := os.Stat(e.qmpPath(id)); !os.IsNotExist(err) {
		t.Fatal("ReattachApplied must not create a live QMP socket")
	}

	disc, err := e.Discover(id)
	if err != nil {
		t.Fatal(err)
	}
	if disc.Status != StatusStopped {
		t.Fatalf("Discover after ReattachApplied must stay stopped, got %s", disc.Status)
	}
}
