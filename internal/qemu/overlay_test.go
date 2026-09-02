package qemu

import (
	"context"
	"strings"
	"testing"
)

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
