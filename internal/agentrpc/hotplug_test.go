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
