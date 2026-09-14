package qemu

import (
	"context"
	"os"
	"os/user"
	"path/filepath"
	"strings"
	"testing"
)

func TestEnsureQEMUCanCreateMakesParentGroupWritable(t *testing.T) {
	if _, err := user.Lookup(QEMUUser); err != nil {
		t.Skip("ndl-qemu user missing")
	}
	if os.Geteuid() != 0 {
		t.Skip("requires root to chown")
	}
	e := &Engine{DataDir: t.TempDir()}
	dir := filepath.Join(t.TempDir(), "vm-disk")
	if err := os.MkdirAll(dir, 0o751); err != nil {
		t.Fatal(err)
	}
	overlay := filepath.Join(dir, "x--snap.qcow2")
	if err := e.ensureQEMUCanCreate(overlay); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm()&0o020 == 0 {
		t.Fatalf("ndl-qemu must be able to create overlays, mode %o", st.Mode().Perm())
	}
}

func TestQMPSnapshotSyncArgsIncludeOverlayNodeName(t *testing.T) {
	args := qmpSnapshotSyncArgs("/var/lib/ndl/storage/local/volumes/vm-disk/x--snap.qcow2")
	if args["node-name"] != "disk0" {
		t.Fatalf("device node %v", args["node-name"])
	}
	if args["snapshot-node-name"] != "disk0-overlay" {
		t.Fatal("QEMU 10 requires snapshot-node-name on blockdev-snapshot-sync")
	}
	if args["snapshot-file"] == "" || args["format"] != "qcow2" {
		t.Fatalf("%v", args)
	}
}

func TestOverlayCreateOfflineRetargetsBootDisk(t *testing.T) {
	e := &Engine{DataDir: t.TempDir(), SkipHostCmds: true, LiveUnits: map[string]bool{}}
	id := "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	backing := "/var/lib/ndl/storage/p/volumes/vm-disk/" + id + ".qcow2"
	overlay := "/var/lib/ndl/storage/p/volumes/vm-disk/" + id + "--snap.qcow2"
	if _, err := e.Prepare(Spec{WorkloadID: id, VolumeID: "v", DiskPath: backing, Accel: "tcg", Machine: DefaultMachine}); err != nil {
		t.Fatal(err)
	}
	res, err := e.OverlayDisk(context.Background(), OverlayRequest{
		Action: OverlayCreate, WorkloadID: id, OverlayPath: overlay, BackingPath: backing, ChainDepth: 0, ChainMax: ChainMax,
	})
	if err == nil {
		t.Fatal("SkipHostCmds must not create a qcow2 overlay")
	}
	if res.Mechanism == "qcow2-overlay" {
		t.Fatalf("must not return overlay success: %+v", res)
	}
	_ = overlay
}

func TestOverlayCreateRefusesLiveQEMUImg(t *testing.T) {
	e := &Engine{DataDir: t.TempDir(), SkipHostCmds: true, LiveUnits: map[string]bool{}}
	id := "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
	backing := "/var/lib/ndl/storage/p/volumes/vm-disk/" + id + ".qcow2"
	overlay := "/var/lib/ndl/storage/p/volumes/vm-disk/" + id + "--snap.qcow2"
	if _, err := e.Prepare(Spec{WorkloadID: id, VolumeID: "v", DiskPath: backing, Accel: "tcg", Machine: DefaultMachine}); err != nil {
		t.Fatal(err)
	}
	e.LiveUnits[id] = true
	_, err := e.OverlayDisk(context.Background(), OverlayRequest{
		Action: OverlayCreate, WorkloadID: id, OverlayPath: overlay, BackingPath: backing,
	})
	if err == nil || !strings.Contains(err.Error(), "qemu-img is refused") {
		t.Fatalf("live qemu-img must be refused: %v", err)
	}
}

func TestOverlayChainCap(t *testing.T) {
	e := &Engine{DataDir: t.TempDir(), SkipHostCmds: true, LiveUnits: map[string]bool{}}
	id := "cccccccc-cccc-4ccc-8ccc-cccccccccccc"
	backing := "/var/lib/ndl/storage/p/volumes/vm-disk/" + id + ".qcow2"
	overlay := "/var/lib/ndl/storage/p/volumes/vm-disk/" + id + "--snap.qcow2"
	_, err := e.OverlayDisk(context.Background(), OverlayRequest{
		Action: OverlayCreate, WorkloadID: id, OverlayPath: overlay, BackingPath: backing, ChainDepth: ChainMax, ChainMax: ChainMax,
	})
	if err == nil || !strings.Contains(err.Error(), "chain cap") {
		t.Fatalf("chain cap: %v", err)
	}
}

func TestOverlayCreateRefusesOverlayEqualsBacking(t *testing.T) {
	e := &Engine{DataDir: t.TempDir(), SkipHostCmds: true, LiveUnits: map[string]bool{}}
	id := "eeeeeeee-eeee-4eee-8eee-eeeeeeeeeeee"
	same := "/var/lib/ndl/storage/p/volumes/vm-disk/" + id + "-tmpl.qcow2"
	_, err := e.OverlayDisk(context.Background(), OverlayRequest{
		Action: OverlayCreate, WorkloadID: id, OverlayPath: same, BackingPath: same,
	})
	if err == nil || !strings.Contains(err.Error(), "must not equal backing") {
		t.Fatalf("overlay equal backing: %v", err)
	}
}

func TestOverlayRejectsTraversal(t *testing.T) {
	e := &Engine{DataDir: t.TempDir(), SkipHostCmds: true}
	id := "dddddddd-dddd-4ddd-8ddd-dddddddddddd"
	_, err := e.OverlayDisk(context.Background(), OverlayRequest{
		Action: OverlayCreate, WorkloadID: id,
		OverlayPath: "/etc/passwd",
		BackingPath: "/var/lib/ndl/storage/p/volumes/vm-disk/" + id + ".qcow2",
	})
	if err == nil {
		t.Fatal("etc passwd")
	}
}
