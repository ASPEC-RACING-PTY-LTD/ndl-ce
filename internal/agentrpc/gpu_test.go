package agentrpc

import (
	"context"
	"strings"
	"testing"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	agentv1 "github.com/no-dal/ndl-ce/gen/nodal/agent/v1"
	"github.com/no-dal/ndl-ce/internal/lxc"
	"github.com/no-dal/ndl-ce/internal/qemu"
	"github.com/no-dal/ndl-ce/internal/vmspec"
)

func TestGPUAssignRefusesAllAndACS(t *testing.T) {
	h := &Handler{SkipHostCmds: true}
	_, err := h.Execute(context.Background(), connect.NewRequest(&agentv1.ExecuteRequest{
		Method: &agentv1.ExecuteRequest_GpuAssign{GpuAssign: &agentv1.GPUAssign{
			Action: "assign", GpuId: "all", WorkloadId: uuid.NewString(), Mode: "render",
		}},
	}))
	if err == nil {
		t.Fatal("gpu=all")
	}
	_, err = h.Execute(context.Background(), connect.NewRequest(&agentv1.ExecuteRequest{
		Method: &agentv1.ExecuteRequest_GpuAssign{GpuAssign: &agentv1.GPUAssign{
			Action: "assign", GpuId: "0000:02:00.0", WorkloadId: uuid.NewString(), Mode: "render", AcsOverride: true,
		}},
	}))
	if err == nil {
		t.Fatal("acs")
	}
}

func TestGPUAssignRenderRewritesLXC(t *testing.T) {
	eng := &lxc.Engine{DataDir: t.TempDir(), SkipHostCmds: true, FakeUnpack: true}
	id := uuid.NewString()
	root := t.TempDir()
	if _, err := eng.Create(context.Background(), lxc.Spec{
		WorkloadID: id, Name: "ct", ImagePin: "alpine/3.21/amd64/default",
		VolumeID: uuid.NewString(), RootfsPath: root, BridgeName: "ndldeadbeef",
	}); err != nil {
		t.Fatal(err)
	}
	h := &Handler{SkipHostCmds: true, Workloads: eng}
	res, err := h.Execute(context.Background(), connect.NewRequest(&agentv1.ExecuteRequest{
		Method: &agentv1.ExecuteRequest_GpuAssign{GpuAssign: &agentv1.GPUAssign{
			Action: "assign", GpuId: "0000:02:00.0", WorkloadId: id, Mode: "render",
			DeviceNodes: []string{"/dev/dri/renderD128"},
		}},
	}))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(res.Msg.GetResultJson()), `"status":"assigned"`) {
		t.Fatalf("SkipHostCmds must not claim assigned: %s", res.Msg.GetResultJson())
	}
	if res.Msg.GetOk() {
		t.Fatal("SkipHostCmds GPU assign must not be Ok")
	}
	if !strings.Contains(string(res.Msg.GetResultJson()), `"status":"unavailable"`) && !strings.Contains(string(res.Msg.GetResultJson()), `"status":"failed"`) {
		t.Fatalf("expected unavailable or failed: %s", res.Msg.GetResultJson())
	}
	applied, err := eng.LastApplied(id)
	if err != nil {
		t.Fatal(err)
	}
	if len(applied.Spec.GPUDevices) != 1 {
		t.Fatalf("%v", applied.Spec.GPUDevices)
	}
}

func seedGPUQEMU(t *testing.T) (*qemu.Engine, string) {
	t.Helper()
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
	return e, id
}

func TestGPUVFIOAssignMergesAndUnassignSubtracts(t *testing.T) {
	e, id := seedGPUQEMU(t)
	h := &Handler{QEMU: e, SkipHostCmds: true}
	_, err := h.Execute(context.Background(), connect.NewRequest(&agentv1.ExecuteRequest{
		Method: &agentv1.ExecuteRequest_GpuAssign{GpuAssign: &agentv1.GPUAssign{
			Action: "assign", GpuId: "0000:02:00.0", WorkloadId: id, Mode: "vfio",
			PciDevices: []string{"0000:02:00.0", "0000:02:00.1"},
		}},
	}))
	if err != nil {
		t.Fatal(err)
	}
	_, err = h.Execute(context.Background(), connect.NewRequest(&agentv1.ExecuteRequest{
		Method: &agentv1.ExecuteRequest_GpuAssign{GpuAssign: &agentv1.GPUAssign{
			Action: "assign", GpuId: "0000:03:00.0", WorkloadId: id, Mode: "vfio",
			PciDevices: []string{"0000:03:00.0"},
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
	if len(qemu.DropHostAddrs(hosts, []string{"0000:02:00.0", "0000:02:00.1", "0000:03:00.0"})) != 0 {
		t.Fatalf("assign must keep both GPUs: %v", hosts)
	}
	if len(hosts) != 3 {
		t.Fatalf("assign hosts %v", hosts)
	}
	_, err = h.Execute(context.Background(), connect.NewRequest(&agentv1.ExecuteRequest{
		Method: &agentv1.ExecuteRequest_GpuAssign{GpuAssign: &agentv1.GPUAssign{
			Action: "unassign", GpuId: "0000:02:00.0", WorkloadId: id, Mode: "vfio",
			PciDevices: []string{"0000:02:00.0", "0000:02:00.1"},
		}},
	}))
	if err != nil {
		t.Fatal(err)
	}
	got, err = e.ReadLaunch(id)
	if err != nil {
		t.Fatal(err)
	}
	hosts = qemu.HostAddrsFromLaunch(got)
	if len(hosts) != 1 || hosts[0] != "0000:03:00.0" {
		t.Fatalf("unassign must keep the other VFIO host: %v", hosts)
	}
}
