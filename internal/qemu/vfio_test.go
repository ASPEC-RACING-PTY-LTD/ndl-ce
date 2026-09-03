package qemu

import (
	"strings"
	"testing"

	"github.com/no-dal/ndl-ce/internal/vmspec"
)

func TestApplyVFIOHostAddsTypedDevice(t *testing.T) {
	e := &Engine{DataDir: t.TempDir(), SkipHostCmds: true}
	id := "11111111-1111-4111-8111-111111111111"
	spec := vmspec.Normalize(vmspec.Spec{
		Name: "web", CPUs: 2, MemoryBytes: 512 << 20,
		NICs: []vmspec.NIC{{ID: id, NetworkID: id}},
	})
	resolved := vmspec.Resolved{
		Accel: "tcg",
		Disks: []vmspec.ResolvedDisk{{
			VolumeID: id, Role: vmspec.DiskRoleBoot,
			Path:   "/var/lib/ndl/storage/local/volumes/vm-disk/" + id + ".qcow2",
			Format: "qcow2",
		}},
		NICs: []vmspec.ResolvedNIC{{
			ID: id, NetworkID: id, BridgeName: "ndl12345678", MAC: vmspec.MACFromID(id),
		}},
	}
	launch, err := vmspec.Compile(id, spec, resolved)
	if err != nil {
		t.Fatal(err)
	}
	argv, err := e.CompileLaunch(launch)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.writeLaunch(launch, argv); err != nil {
		t.Fatal(err)
	}
	if err := e.ApplyVFIOHost(id, []string{"0000:02:00.0", "0000:02:00.1"}); err != nil {
		t.Fatal(err)
	}
	got, err := e.ReadLaunch(id)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.GPUs) != 2 {
		t.Fatalf("%d", len(got.GPUs))
	}
	argv, err = e.CompileLaunch(got)
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(argv, " ")
	if !strings.Contains(joined, "vfio-pci,host=0000:02:00.0") || !strings.Contains(joined, "vfio-pci,host=0000:02:00.1") {
		t.Fatal(joined)
	}
	if strings.Contains(joined, "/bin/sh") || strings.Contains(joined, "all") && strings.Contains(joined, "NVIDIA_VISIBLE") {
		t.Fatal(joined)
	}
	if err := e.ApplyVFIOHost(id, nil); err != nil {
		t.Fatal(err)
	}
	got, _ = e.ReadLaunch(id)
	if len(got.GPUs) != 0 {
		t.Fatal("unassign must drop vfio devices")
	}
}

func TestApplyVFIOHostRejectsAll(t *testing.T) {
	e := &Engine{DataDir: t.TempDir(), SkipHostCmds: true}
	if err := e.ApplyVFIOHost("11111111-1111-4111-8111-111111111111", []string{"all"}); err == nil {
		t.Fatal("gpu=all")
	}
}

func TestMergeAndDropHostAddrs(t *testing.T) {
	merged := MergeHostAddrs([]string{"0000:03:00.0"}, []string{"0000:04:00.0", "0000:03:00.0"})
	if len(merged) != 2 || merged[0] != "0000:03:00.0" || merged[1] != "0000:04:00.0" {
		t.Fatalf("%v", merged)
	}
	kept := DropHostAddrs(merged, []string{"0000:03:00.0"})
	if len(kept) != 1 || kept[0] != "0000:04:00.0" {
		t.Fatalf("%v", kept)
	}
	if got := MergeHostAddrs(nil, nil); len(got) != 0 {
		t.Fatalf("%v", got)
	}
}
