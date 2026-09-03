package agentrpc

import (
	"context"
	"strings"
	"testing"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	agentv1 "github.com/no-dal/ndl-ce/gen/nodal/agent/v1"
	"github.com/no-dal/ndl-ce/internal/qemu"
	"github.com/no-dal/ndl-ce/internal/vmspec"
)

func TestHotplugUSBQMPFailureIsNotOk(t *testing.T) {
	e := &qemu.Engine{DataDir: t.TempDir(), SkipHostCmds: true}
	id := uuid.NewString()
	spec := vmspec.Normalize(vmspec.Spec{
		Name: "web", CPUs: 1, MemoryBytes: 128 << 20,
		NICs: []vmspec.NIC{{ID: id, NetworkID: id}},
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
	if _, err := e.PrepareLaunch(context.Background(), launch, qemu.ConvertRequest{}); err != nil {
		t.Fatal(err)
	}
	h := &Handler{QEMU: e, SkipHostCmds: true}
	res, err := h.Execute(context.Background(), connect.NewRequest(&agentv1.ExecuteRequest{
		Method: &agentv1.ExecuteRequest_VmHotplug{VmHotplug: &agentv1.VMHotplug{
			WorkloadId: id, DeviceKind: "usb-host", Action: "device_add",
			Address: "1-2", VendorId: "046d", ProductId: "c52b",
		}},
	}))
	if err != nil {
		t.Fatal(err)
	}
	if res.Msg.GetOk() {
		t.Fatal("QMP/device_add failure must set Ok false")
	}
	if !strings.Contains(res.Msg.GetMessage(), "QMP") && !strings.Contains(res.Msg.GetMessage(), "qmp") && !strings.Contains(res.Msg.GetMessage(), "usb") {
		t.Fatalf("error must not be discarded: %q", res.Msg.GetMessage())
	}
}

func TestHotplugVFIOMergesExistingHosts(t *testing.T) {
	e := &qemu.Engine{DataDir: t.TempDir(), SkipHostCmds: true}
	id := uuid.NewString()
	spec := vmspec.Normalize(vmspec.Spec{
		Name: "web", CPUs: 1, MemoryBytes: 128 << 20,
		NICs: []vmspec.NIC{{ID: id, NetworkID: id}},
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
	if _, err := e.PrepareLaunch(context.Background(), launch, qemu.ConvertRequest{}); err != nil {
		t.Fatal(err)
	}
	if err := e.ApplyVFIOHost(id, []string{"0000:03:00.0"}); err != nil {
		t.Fatal(err)
	}
	h := &Handler{QEMU: e, SkipHostCmds: true}
	_, err = h.Execute(context.Background(), connect.NewRequest(&agentv1.ExecuteRequest{
		Method: &agentv1.ExecuteRequest_VmHotplug{VmHotplug: &agentv1.VMHotplug{
			WorkloadId: id, DeviceKind: "vfio-pci", Action: "add",
			PciHosts: []string{"0000:04:00.0"},
		}},
	}))
	if err != nil {
		t.Fatal(err)
	}
	got, err := e.ReadLaunch(id)
	if err != nil {
		t.Fatal(err)
	}
	hosts := qemu.HostAddrsFromLaunch(got)
	if len(hosts) != 2 {
		t.Fatalf("hotplug must merge VFIO hosts: %v", hosts)
	}
	joined := strings.Join(hosts, " ")
	if !strings.Contains(joined, "0000:03:00.0") || !strings.Contains(joined, "0000:04:00.0") {
		t.Fatalf("hotplug dropped a VFIO host: %v", hosts)
	}
}
