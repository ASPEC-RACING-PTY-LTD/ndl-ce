package qemu

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/no-dal/ndl-ce/internal/vmspec"
)

func TestCompileLaunchDeterministicAndPinsABI(t *testing.T) {
	e := &Engine{DataDir: t.TempDir(), SkipHostCmds: true}
	id := "11111111-1111-4111-8111-111111111111"
	spec := vmspec.Normalize(vmspec.Spec{
		Name: "web", CPUs: 2, MemoryBytes: 512 << 20,
		NICs:    []vmspec.NIC{{ID: id, NetworkID: id}},
		NoCloud: vmspec.NoCloud{Enable: true, Hostname: "web"},
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
	a, err := e.CompileLaunch(launch)
	if err != nil {
		t.Fatal(err)
	}
	b, err := e.CompileLaunch(launch)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(a, "\x00") != strings.Join(b, "\x00") {
		t.Fatal("argv must be deterministic")
	}
	joined := strings.Join(a, " ")
	if !strings.Contains(joined, vmspec.DefaultMachine) {
		t.Fatal(joined)
	}
	if !strings.Contains(joined, GuestAgentName) || !strings.Contains(joined, "mode=control") {
		t.Fatal(joined)
	}
	if !strings.Contains(joined, "script=no") || !strings.Contains(joined, launch.NICs[0].MAC) {
		t.Fatal(joined)
	}
	if strings.Contains(joined, ":5900") || strings.Contains(joined, "/bin/sh") {
		t.Fatal(joined)
	}
}

func TestCompileLaunchRejectsRawArgsAndTraversal(t *testing.T) {
	e := &Engine{DataDir: t.TempDir(), SkipHostCmds: true}
	id := "11111111-1111-4111-8111-111111111111"
	launch := vmspec.Launch{
		WorkloadID: id, Machine: vmspec.DefaultMachine, Accel: "tcg", CPUs: 1, MemoryMiB: 128, QGA: true,
		BootOrder: "c", Console: vmspec.LaunchConsole{Serial: true, VNC: true},
		PCI: map[string]string{"vga": "0x2", "serial": "0x3"},
		Disks: []vmspec.LaunchDisk{{
			Role: vmspec.DiskRoleBoot, Path: "/etc/passwd", Format: "qcow2", PCIAddr: "0x5", NodeName: "disk0",
		}},
		NICs: []vmspec.LaunchNIC{{NetworkID: id, BridgeName: "ndl0", TAPName: "nvabc", MAC: "02:00:00:00:00:01", PCIAddr: "0x8"}},
	}
	if _, err := e.CompileLaunch(launch); err == nil {
		t.Fatal("etc passwd")
	}
	launch.Disks[0].Path = "/var/lib/ndl/storage/p/d.qcow2,driver=raw"
	if _, err := e.CompileLaunch(launch); err == nil {
		t.Fatal("comma")
	}
}

func TestAssertDiskOfflineBlocksRunning(t *testing.T) {
	root := t.TempDir()
	e := &Engine{DataDir: root, SkipHostCmds: true}
	id := "22222222-2222-4222-8222-222222222222"
	disk := "/var/lib/ndl/storage/p/volumes/vm-disk/" + id + ".qcow2"
	if _, err := e.Prepare(Spec{WorkloadID: id, VolumeID: "v", DiskPath: disk, Accel: "tcg", Machine: DefaultMachine}); err != nil {
		t.Fatal(err)
	}
	if err := e.AssertDiskOffline(context.Background(), disk); err != nil {
		t.Fatal(err)
	}
	e.LiveUnits = map[string]bool{id: true}
	if err := e.AssertDiskOffline(context.Background(), disk); err == nil {
		t.Fatal("live disk must be refused")
	}
}

func TestConvertOfflineAssertsSourceAndCorruptApplied(t *testing.T) {
	root := t.TempDir()
	e := &Engine{DataDir: root, SkipHostCmds: true, LiveUnits: map[string]bool{}}
	id := "cccccccc-cccc-4ccc-8ccc-cccccccccccc"
	disk := "/var/lib/ndl/storage/p/volumes/vm-disk/" + id + ".qcow2"
	if _, err := e.Prepare(Spec{WorkloadID: id, VolumeID: "v", DiskPath: disk, Accel: "tcg", Machine: DefaultMachine}); err != nil {
		t.Fatal(err)
	}
	e.LiveUnits[id] = true
	dest := "/var/lib/ndl/storage/p/volumes/vm-disk/other.qcow2"
	if err := e.ConvertOffline(context.Background(), ConvertRequest{
		SourcePath: disk, DestPath: dest, SourceFormat: "qcow2", DestFormat: "qcow2",
	}); err == nil {
		t.Fatal("live source disk must be refused")
	}
	e.LiveUnits[id] = false
	if err := os.WriteFile(e.appliedPath(id), []byte("{"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := e.AssertDiskOffline(context.Background(), dest); err == nil {
		t.Fatal("corrupt applied state must refuse qemu-img")
	}
}

func TestCleanupLaunchRefusesUnmanagedTAP(t *testing.T) {
	e := &Engine{DataDir: t.TempDir(), SkipHostCmds: true}
	launch := vmspec.Launch{
		WorkloadID: "11111111-1111-4111-8111-111111111111",
		NICs:       []vmspec.LaunchNIC{{TAPName: "eth0", BridgeName: "br0"}},
	}
	if err := e.cleanupTAPs(launch); err == nil {
		t.Fatal("must not delete unmanaged interfaces")
	}
}
