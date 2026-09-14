package agentrpc

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	agentv1 "github.com/no-dal/ndl-ce/gen/nodal/agent/v1"
	"github.com/no-dal/ndl-ce/internal/qemu"
	"github.com/no-dal/ndl-ce/internal/vmspec"
)

func prepareHotplugVM(t *testing.T, live bool) (*qemu.Engine, string) {
	t.Helper()
	id := uuid.NewString()
	e := &qemu.Engine{DataDir: t.TempDir(), SkipHostCmds: true, LiveUnits: map[string]bool{}}
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
	if live {
		e.LiveUnits[id] = true
	}
	return e, id
}

func TestHotplugUSBQMPFailureIsNotOk(t *testing.T) {
	e, id := prepareHotplugVM(t, true)
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
		t.Fatal("running VM QMP/device_add failure must set Ok false")
	}
	if !strings.Contains(res.Msg.GetMessage(), "QMP") && !strings.Contains(res.Msg.GetMessage(), "qmp") && !strings.Contains(res.Msg.GetMessage(), "usb") {
		t.Fatalf("error must not be discarded: %q", res.Msg.GetMessage())
	}
}

func TestHotplugUSBStoppedAppliesFrozenArgvWithoutQMP(t *testing.T) {
	e, id := prepareHotplugVM(t, false)
	h := &Handler{QEMU: e, SkipHostCmds: true}
	res, err := h.Execute(context.Background(), connect.NewRequest(&agentv1.ExecuteRequest{
		Method: &agentv1.ExecuteRequest_VmHotplug{VmHotplug: &agentv1.VMHotplug{
			WorkloadId: id, DeviceKind: "usb-host", Action: "add",
			Address: "1-2", VendorId: "046d", ProductId: "c52b",
		}},
	}))
	if err != nil {
		t.Fatal(err)
	}
	if !res.Msg.GetOk() {
		t.Fatalf("stopped USB apply must not require QMP: %q", res.Msg.GetMessage())
	}
	got, err := e.ReadLaunch(id)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.USBs) != 1 || got.USBs[0].Address != "1-2" {
		t.Fatalf("frozen argv must record USB: %+v", got.USBs)
	}
	res, err = h.Execute(context.Background(), connect.NewRequest(&agentv1.ExecuteRequest{
		Method: &agentv1.ExecuteRequest_VmHotplug{VmHotplug: &agentv1.VMHotplug{
			WorkloadId: id, DeviceKind: "usb-host", Action: "add",
			Address: "3-4", VendorId: "1d6b", ProductId: "0002",
		}},
	}))
	if err != nil {
		t.Fatal(err)
	}
	if !res.Msg.GetOk() {
		t.Fatalf("second stopped USB apply: %q", res.Msg.GetMessage())
	}
	got, err = e.ReadLaunch(id)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.USBs) != 2 {
		t.Fatalf("second USB must merge into frozen argv: %+v", got.USBs)
	}
	joined := got.USBs[0].Address + " " + got.USBs[1].Address
	if !strings.Contains(joined, "1-2") || !strings.Contains(joined, "3-4") {
		t.Fatalf("second USB dropped the first: %+v", got.USBs)
	}
}

func TestHotplugUSBStoppedDelUpdatesFrozenArgvWithoutQMP(t *testing.T) {
	e, id := prepareHotplugVM(t, false)
	h := &Handler{QEMU: e, SkipHostCmds: true}
	if _, err := h.Execute(context.Background(), connect.NewRequest(&agentv1.ExecuteRequest{
		Method: &agentv1.ExecuteRequest_VmHotplug{VmHotplug: &agentv1.VMHotplug{
			WorkloadId: id, DeviceKind: "usb-host", Action: "add",
			Address: "1-2", VendorId: "046d", ProductId: "c52b",
		}},
	})); err != nil {
		t.Fatal(err)
	}
	res, err := h.Execute(context.Background(), connect.NewRequest(&agentv1.ExecuteRequest{
		Method: &agentv1.ExecuteRequest_VmHotplug{VmHotplug: &agentv1.VMHotplug{
			WorkloadId: id, DeviceKind: "usb-host", Action: "del",
			Address: "1-2", VendorId: "046d", ProductId: "c52b",
		}},
	}))
	if err != nil {
		t.Fatal(err)
	}
	if !res.Msg.GetOk() {
		t.Fatalf("stopped USB del must not require QMP: %q", res.Msg.GetMessage())
	}
	got, err := e.ReadLaunch(id)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.USBs) != 0 {
		t.Fatalf("stopped del must drop USB from frozen argv: %+v", got.USBs)
	}
}

func TestExecuteOKTreatsFalseAsError(t *testing.T) {
	res := connect.NewResponse(&agentv1.ExecuteResponse{Ok: false, Message: "usb hotplug requires a live QMP session"})
	err := executeOK(res, nil)
	if err == nil || !strings.Contains(err.Error(), "QMP") {
		t.Fatalf("Ok false must be an error: %v", err)
	}
	if err := executeOK(connect.NewResponse(&agentv1.ExecuteResponse{Ok: true, Message: "add"}), nil); err != nil {
		t.Fatal(err)
	}
	if err := executeOK(nil, fmt.Errorf("dial")); err == nil || !strings.Contains(err.Error(), "dial") {
		t.Fatal("connect error must propagate")
	}
}

func TestHotplugVFIOMergesExistingHosts(t *testing.T) {
	e, id := prepareHotplugVM(t, false)
	if err := e.ApplyVFIOHost(id, []string{"0000:03:00.0"}); err != nil {
		t.Fatal(err)
	}
	h := &Handler{QEMU: e, SkipHostCmds: true}
	_, err := h.Execute(context.Background(), connect.NewRequest(&agentv1.ExecuteRequest{
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
