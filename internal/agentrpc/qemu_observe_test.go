package agentrpc

import (
	"encoding/json"
	"testing"

	"github.com/no-dal/ndl-ce/internal/lxc"
	"github.com/no-dal/ndl-ce/internal/qemu"
)

func TestObserveWorkloadsMapsKindVMViaQEMUNotLXC(t *testing.T) {
	root := t.TempDir()
	qeng := &qemu.Engine{DataDir: root, SkipHostCmds: true}
	leng := &lxc.Engine{DataDir: t.TempDir(), SkipHostCmds: true}
	h := &Handler{QEMU: qeng, Workloads: leng}

	vmID := "12121212-1212-4121-8121-121212121212"
	ctID := "34343434-3434-4343-8343-343434343434"
	if _, err := qeng.Prepare(qemu.Spec{
		WorkloadID: vmID,
		VolumeID:   "vol-vm-obs",
		DiskPath:   "/var/lib/ndl/storage/p/volumes/vm-disk/" + vmID + ".qcow2",
		Accel:      "tcg",
		Machine:    qemu.DefaultMachine,
	}); err != nil {
		t.Fatal(err)
	}

	raw := h.observeWorkloads([]lxc.Hint{
		{WorkloadID: vmID, Kind: qemu.KindVM, VolumeID: "vol-vm-obs"},
		{WorkloadID: ctID, Kind: lxc.KindSystemContainer},
	})
	var obs lxc.Observation
	if err := json.Unmarshal(raw, &obs); err != nil {
		t.Fatal(err)
	}

	var vmHits, ctHits int
	for _, w := range obs.Workloads {
		switch w.WorkloadID {
		case vmID:
			vmHits++
			if w.Kind != qemu.KindVM {
				t.Fatalf("vm kind=%s", w.Kind)
			}
			if w.Status != qemu.StatusStopped {
				t.Fatalf("vm status=%s want stopped via qemu.Engine", w.Status)
			}
			if w.Reason != "fixture" {
				t.Fatalf("vm reason=%q want qemu fixture, not lxc", w.Reason)
			}
			if w.Reason == "workload was not observed" {
				t.Fatal("vm hint must not be sent to lxc")
			}
			foundQEMUBlocker := false
			for _, b := range w.MigrateBlockers {
				if b == "QEMU live migrate is not implemented" {
					foundQEMUBlocker = true
				}
				if b == "offline migrate is Phase 32" {
					t.Fatal("vm hint must not receive lxc migrate blockers")
				}
			}
			if !foundQEMUBlocker {
				t.Fatalf("vm must be mapped via qemu.Engine: blockers=%v", w.MigrateBlockers)
			}
		case ctID:
			ctHits++
			if w.Kind != lxc.KindSystemContainer {
				t.Fatalf("ct kind=%s", w.Kind)
			}
			if w.Reason != "workload was not observed" {
				t.Fatalf("ct reason=%q", w.Reason)
			}
		default:
			t.Fatalf("unexpected workload %s", w.WorkloadID)
		}
	}
	if vmHits != 1 {
		t.Fatalf("want exactly one qemu observation for the vm hint, got %d", vmHits)
	}
	if ctHits != 1 {
		t.Fatalf("want the ct hint observed by lxc, got %d", ctHits)
	}
}

func TestObserveWorkloadsVMOnlyDoesNotInventLXCRow(t *testing.T) {
	qeng := &qemu.Engine{DataDir: t.TempDir(), SkipHostCmds: true}
	leng := &lxc.Engine{DataDir: t.TempDir(), SkipHostCmds: true}
	h := &Handler{QEMU: qeng, Workloads: leng}
	vmID := "56565656-5656-4565-8565-565656565656"

	raw := h.observeWorkloads([]lxc.Hint{{WorkloadID: vmID, Kind: qemu.KindVM}})
	var obs lxc.Observation
	if err := json.Unmarshal(raw, &obs); err != nil {
		t.Fatal(err)
	}
	if len(obs.Workloads) != 1 {
		t.Fatalf("vm-only hints must not be forwarded to lxc, got %+v", obs.Workloads)
	}
	w := obs.Workloads[0]
	if w.WorkloadID != vmID || w.Kind != qemu.KindVM {
		t.Fatalf("%+v", w)
	}
	if w.Reason == "workload was not observed" {
		t.Fatal("kind=vm must be observed by qemu.Engine, not lxc")
	}
	if w.Status != qemu.StatusStopped || w.Reason != "fixture" {
		t.Fatalf("qemu fixture status=%s reason=%s", w.Status, w.Reason)
	}
}
