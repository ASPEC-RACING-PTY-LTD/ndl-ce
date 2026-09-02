package agentrpc

import (
	"context"
	"strings"
	"testing"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	agentv1 "github.com/no-dal/ndl-ce/gen/nodal/agent/v1"
	"github.com/no-dal/ndl-ce/internal/lxc"
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
