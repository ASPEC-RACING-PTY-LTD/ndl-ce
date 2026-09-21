package qemu

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/no-dal/ndl-ce/internal/physdisk"
	"github.com/no-dal/ndl-ce/internal/vmspec"
)

func physicalLaunch(t *testing.T, extra []vmspec.ResolvedDisk, iso string, boot []string) (vmspec.Launch, []string) {
	t.Helper()
	e := &Engine{DataDir: t.TempDir(), SkipHostCmds: true}
	id := "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
	netID := "33333333-3333-4333-8333-333333333333"
	spec := vmspec.Normalize(vmspec.Spec{
		Name: "windows", CPUs: 2, MemoryBytes: 2 << 30, Firmware: vmspec.FirmwareBIOS,
		BootOrder: boot,
		Disks: []vmspec.Disk{{
			Role: vmspec.DiskRoleBoot, Source: vmspec.DiskSourcePhysical,
			DeviceID: "ata-CT1000MX500SSD1_0001", Format: "raw", Bus: vmspec.DiskBusAHCI,
		}},
		NICs: []vmspec.NIC{{ID: netID, NetworkID: netID}},
	})
	if iso != "" {
		spec.ISOLibraryID = "44444444-4444-4444-8444-444444444444"
	}
	disks := []vmspec.ResolvedDisk{{
		Role: vmspec.DiskRoleBoot, Source: vmspec.DiskSourcePhysical,
		DeviceID: "ata-CT1000MX500SSD1_0001", Path: "/dev/disk/by-id/ata-CT1000MX500SSD1_0001",
		Format: "raw", Bus: vmspec.DiskBusAHCI, Serial: "0001", Discard: true,
	}}
	disks = append(disks, extra...)
	resolved := vmspec.Resolved{
		Accel: "tcg",
		Disks: disks,
		NICs: []vmspec.ResolvedNIC{{
			ID: netID, NetworkID: netID, BridgeName: "ndl12345678", MAC: vmspec.MACFromID(id),
		}},
		ISOPath: iso,
	}
	launch, err := vmspec.Compile(id, spec, resolved)
	if err != nil {
		t.Fatal(err)
	}
	argv, err := e.CompileLaunch(launch)
	if err != nil {
		t.Fatal(err)
	}
	return launch, argv
}

func TestCompilePhysicalBootDisk(t *testing.T) {
	launch, argv := physicalLaunch(t, nil, "", []string{"disk"})
	joined := strings.Join(argv, " ")
	if !strings.Contains(joined, "driver=host_device") {
		t.Fatal(joined)
	}
	if !strings.Contains(joined, "filename=/dev/disk/by-id/ata-CT1000MX500SSD1_0001") {
		t.Fatal(joined)
	}
	if !strings.Contains(joined, "cache.direct=on,aio=native") {
		t.Fatal(joined)
	}
	if !strings.Contains(joined, "ide-hd,drive=") {
		t.Fatal(joined)
	}
	if !strings.Contains(joined, "bootindex=1") {
		t.Fatal(joined)
	}
	if strings.Contains(joined, "virtio-scsi") || strings.Contains(joined, "scsi-cd") {
		t.Fatal(joined)
	}
	if launch.Disks[0].Format != "raw" || launch.Disks[0].Source != vmspec.DiskSourcePhysical {
		t.Fatalf("%+v", launch.Disks[0])
	}
}

func TestCompileMixedVirtualAndPhysical(t *testing.T) {
	id := "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	extra := []vmspec.ResolvedDisk{{
		VolumeID: id, Role: vmspec.DiskRoleData, Slot: 1, PCIAddr: "0x5",
		Path: "/var/lib/ndl/storage/local/volumes/vm-disk/" + id + ".qcow2", Format: "qcow2",
	}}
	_, argv := physicalLaunch(t, extra, "", []string{"disk"})
	joined := strings.Join(argv, " ")
	if !strings.Contains(joined, "driver=host_device") || !strings.Contains(joined, "ide-hd") {
		t.Fatal(joined)
	}
	if !strings.Contains(joined, "virtio-blk-pci") {
		t.Fatal(joined)
	}
}

func TestCompileISOUsesAHCIAndDeterministicBootOrder(t *testing.T) {
	iso := "/var/lib/ndl/storage/local/library/win11.iso"
	_, withISO := physicalLaunch(t, nil, iso, []string{"cdrom", "disk"})
	joined := strings.Join(withISO, " ")
	if !strings.Contains(joined, "ide-cd,drive=") {
		t.Fatal(joined)
	}
	if strings.Contains(joined, "scsi-cd") || strings.Contains(joined, "virtio-scsi") {
		t.Fatal(joined)
	}
	if !strings.Contains(joined, "-boot order=dc") {
		t.Fatal(joined)
	}
	if !strings.Contains(joined, "ide-cd,drive=iso0,bus=ide.1,id=iso0,bootindex=1") && !strings.Contains(joined, "bootindex=1") {
		t.Fatal(joined)
	}
	if !strings.Contains(joined, "bootindex=2") {
		t.Fatal(joined)
	}
	_, diskOnly := physicalLaunch(t, nil, "", []string{"disk"})
	if !strings.Contains(strings.Join(diskOnly, " "), "-boot order=c") {
		t.Fatal(strings.Join(diskOnly, " "))
	}
}

func TestDeleteRuntimeNeverTouchesPhysicalDisk(t *testing.T) {
	root := t.TempDir()
	e := &Engine{DataDir: root, SkipHostCmds: true}
	id := "cccccccc-cccc-4ccc-8ccc-cccccccccccc"
	sentinel := filepath.Join(root, "physical-sentinel")
	if err := os.WriteFile(sentinel, []byte("windows-install"), 0o644); err != nil {
		t.Fatal(err)
	}
	launch := vmspec.Launch{
		WorkloadID: id, Machine: vmspec.DefaultMachine, Accel: "tcg", CPUs: 1, MemoryMiB: 128, QGA: true,
		BootOrder: "c", Console: vmspec.LaunchConsole{Serial: true, VNC: true},
		PCI: map[string]string{"vga": "0x2", "serial": "0x3"},
		Disks: []vmspec.LaunchDisk{{
			Role: vmspec.DiskRoleBoot, Source: vmspec.DiskSourcePhysical,
			DeviceID: "ata-CT1000MX500SSD1_0001", Path: "/dev/disk/by-id/ata-CT1000MX500SSD1_0001",
			Format: "raw", Bus: vmspec.DiskBusAHCI, NodeName: "disk0",
		}},
		NICs: []vmspec.LaunchNIC{{NetworkID: id, BridgeName: "ndl0", TAPName: "nvabc", MAC: "02:00:00:00:00:01", PCIAddr: "0x8"}},
	}
	if _, err := e.PrepareLaunch(context.Background(), launch, ConvertRequest{}); err != nil {
		t.Fatal(err)
	}
	if err := e.DeleteRuntime(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(sentinel)
	if err != nil || string(body) != "windows-install" {
		t.Fatalf("physical contents were modified: %v %q", err, body)
	}
	if _, err := os.Stat("/dev/disk/by-id/ata-CT1000MX500SSD1_0001"); err == nil {
		t.Fatal("test must not require a real host disk")
	}
}

func TestCleanupPhysicalReleasesClaim(t *testing.T) {
	prev := physdiskRunDir(t)
	defer prev()
	e := &Engine{DataDir: t.TempDir(), SkipHostCmds: true}
	id := "dddddddd-dddd-4ddd-8ddd-dddddddddddd"
	dev := physdisk.DeviceID("ata-UNUSED")
	if err := physdisk.Claim(dev, id, true); err != nil {
		t.Fatal(err)
	}
	launch := vmspec.Launch{
		WorkloadID: id,
		Disks: []vmspec.LaunchDisk{{
			Role: vmspec.DiskRoleBoot, Source: vmspec.DiskSourcePhysical,
			DeviceID: string(dev), Path: "/dev/disk/by-id/ata-UNUSED",
		}},
	}
	if err := e.cleanupPhysical(launch); err != nil {
		t.Fatal(err)
	}
	if err := physdisk.Claim(dev, "other", false); err != nil {
		t.Fatalf("failed launch must release the runtime claim: %v", err)
	}
}

func physdiskRunDir(t *testing.T) func() {
	t.Helper()
	return physdisk.SetRunDirForTest(t.TempDir())
}
