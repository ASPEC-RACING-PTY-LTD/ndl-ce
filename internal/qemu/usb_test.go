package qemu

import (
	"strings"
	"testing"

	"github.com/no-dal/ndl-ce/internal/vmspec"
)

func TestUSBHostArgvIsTypedAndXHCIAlwaysPresent(t *testing.T) {
	e := &Engine{DataDir: t.TempDir(), SkipHostCmds: true}
	id := "11111111-1111-4111-8111-111111111111"
	spec := vmspec.Normalize(vmspec.Spec{
		Name: "web", CPUs: 1, MemoryBytes: 128 << 20,
		NICs: []vmspec.NIC{{ID: id, NetworkID: id}},
		USBs: []vmspec.USB{{Address: "1-2", Vendor: "046d", Product: "c52b"}},
	})
	resolved := vmspec.Resolved{
		Accel: "tcg",
		Disks: []vmspec.ResolvedDisk{{
			VolumeID: id, Role: vmspec.DiskRoleBoot,
			Path: "/var/lib/ndl/storage/local/volumes/vm-disk/" + id + ".qcow2", Format: "qcow2",
		}},
		NICs: []vmspec.ResolvedNIC{{ID: id, NetworkID: id, BridgeName: "ndl12345678", MAC: vmspec.MACFromID(id)}},
	}
	launch, err := vmspec.Compile(id, spec, resolved)
	if err != nil {
		t.Fatal(err)
	}
	argv, err := e.CompileLaunch(launch)
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(argv, " ")
	if !strings.Contains(joined, "qemu-xhci,id=usb") {
		t.Fatal(joined)
	}
	if !strings.Contains(joined, "usb-host,id=usb_1_2,vendorid=0x046d,productid=0xc52b") {
		t.Fatal(joined)
	}
	if strings.Contains(joined, "-incoming") || strings.Contains(joined, "/bin/sh") {
		t.Fatal(joined)
	}
}

func TestUSBHostRejectsInjection(t *testing.T) {
	_, err := usbHostDevice(vmspec.LaunchUSB{Address: "1-2,id=evil", Vendor: "046d", Product: "c52b"})
	if err == nil {
		t.Fatal("injected address")
	}
	_, err = usbHostDevice(vmspec.LaunchUSB{Address: "1-2", Vendor: "zzzz", Product: "c52b"})
	if err == nil {
		t.Fatal("non-hex vendor")
	}
}
