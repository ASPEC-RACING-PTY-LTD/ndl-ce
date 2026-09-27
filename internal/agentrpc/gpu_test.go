package agentrpc

import (
	"context"
	"strings"
	"testing"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	agentv1 "github.com/no-dal/ndl-ce/gen/nodal/agent/v1"
	"github.com/no-dal/ndl-ce/internal/gpu"
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

func TestGPUReapplyAndDiagnoseSystemContainer(t *testing.T) {
	eng := &lxc.Engine{DataDir: t.TempDir(), SkipHostCmds: true, FakeUnpack: true}
	id := uuid.NewString()
	if _, err := eng.Create(context.Background(), lxc.Spec{
		WorkloadID: id, Name: "ct", ImagePin: "alpine/3.21/amd64/default",
		VolumeID: uuid.NewString(), RootfsPath: t.TempDir(), BridgeName: "ndldeadbeef",
	}); err != nil {
		t.Fatal(err)
	}
	h := &Handler{SkipHostCmds: true, Workloads: eng}
	exec := func(m *agentv1.GPUAssign) (*connect.Response[agentv1.ExecuteResponse], error) {
		return h.Execute(context.Background(), connect.NewRequest(&agentv1.ExecuteRequest{
			Method: &agentv1.ExecuteRequest_GpuAssign{GpuAssign: m},
		}))
	}
	nodes := []string{"/dev/nvidia1", "/dev/nvidiactl"}
	res, err := exec(&agentv1.GPUAssign{Action: "reapply", WorkloadId: id, DeviceNodes: nodes})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Msg.GetOk() || !strings.Contains(string(res.Msg.GetResultJson()), `"diagnosis":{`) {
		t.Fatalf("reapply %s", res.Msg.GetResultJson())
	}
	applied, _ := eng.LastApplied(id)
	if len(applied.Spec.GPUDevices) != 2 || applied.Spec.GPUDevices[0] != "/dev/nvidia1" {
		t.Fatalf("applied %v", applied.Spec.GPUDevices)
	}
	res, err = exec(&agentv1.GPUAssign{Action: "diagnose", WorkloadId: id})
	if err != nil || !strings.Contains(string(res.Msg.GetResultJson()), `"saved":["/dev/nvidia1","/dev/nvidiactl"]`) {
		t.Fatalf("diagnose %v %s", err, res.Msg.GetResultJson())
	}
	if _, err := exec(&agentv1.GPUAssign{Action: "reapply", WorkloadId: id, DeviceNodes: []string{"/dev/sda"}}); err == nil {
		t.Fatal("non-GPU node must be refused")
	}
	if _, err := exec(&agentv1.GPUAssign{Action: "diagnose", WorkloadId: "../etc"}); err == nil {
		t.Fatal("workload id must be a UUID")
	}
}

func TestGPUReleaseKeepsRemainingDevices(t *testing.T) {
	eng := &lxc.Engine{DataDir: t.TempDir(), SkipHostCmds: true, FakeUnpack: true}
	id := uuid.NewString()
	if _, err := eng.Create(context.Background(), lxc.Spec{
		WorkloadID: id, Name: "ct", ImagePin: "alpine/3.21/amd64/default",
		VolumeID: uuid.NewString(), RootfsPath: t.TempDir(), BridgeName: "ndldeadbeef",
		GPUDevices: []string{"/dev/nvidia1", "/dev/nvidiactl", "/dev/dri/renderD128"},
	}); err != nil {
		t.Fatal(err)
	}
	h := &Handler{SkipHostCmds: true, Workloads: eng}
	exec := func(m *agentv1.GPUAssign) (*connect.Response[agentv1.ExecuteResponse], error) {
		return h.Execute(context.Background(), connect.NewRequest(&agentv1.ExecuteRequest{
			Method: &agentv1.ExecuteRequest_GpuAssign{GpuAssign: m},
		}))
	}
	res, err := exec(&agentv1.GPUAssign{Action: gpu.ActionRelease, GpuId: "0000:02:00.0", WorkloadId: id, Mode: "encode", DeviceNodes: []string{"/dev/dri/renderD128"}})
	if err != nil {
		t.Fatal(err)
	}
	body := string(res.Msg.GetResultJson())
	if !res.Msg.GetOk() || !strings.Contains(body, `"status":"released"`) || !strings.Contains(body, `"config_current":true`) {
		t.Fatalf("release %s", body)
	}
	applied, _ := eng.LastApplied(id)
	if len(applied.Spec.GPUDevices) != 1 || applied.Spec.GPUDevices[0] != "/dev/dri/renderD128" {
		t.Fatalf("remaining devices %v", applied.Spec.GPUDevices)
	}

	res, err = exec(&agentv1.GPUAssign{Action: gpu.ActionRelease, GpuId: "0000:13:00.0", WorkloadId: id, Mode: "render"})
	if err != nil || !res.Msg.GetOk() {
		t.Fatalf("last release %v %s", err, res.Msg.GetResultJson())
	}
	applied, _ = eng.LastApplied(id)
	if len(applied.Spec.GPUDevices) != 0 {
		t.Fatalf("last release must clear devices %v", applied.Spec.GPUDevices)
	}

	missing := uuid.NewString()
	res, err = exec(&agentv1.GPUAssign{Action: gpu.ActionRelease, GpuId: "0000:02:00.0", WorkloadId: missing, Mode: "encode"})
	if err != nil || res.Msg.GetOk() || !strings.Contains(string(res.Msg.GetResultJson()), `"status":"failed"`) {
		t.Fatalf("release on a missing workload must fail, got %v %s", err, res.Msg.GetResultJson())
	}
	if _, err := exec(&agentv1.GPUAssign{Action: gpu.ActionRelease, GpuId: "0000:02:00.0", WorkloadId: id, Mode: "encode", DeviceNodes: []string{"/dev/sda"}}); err == nil {
		t.Fatal("non-GPU node must be refused")
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
