package agentrpc

import (
	"context"
	"encoding/json"

	"connectrpc.com/connect"
	agentv1 "github.com/no-dal/ndl-ce/gen/nodal/agent/v1"
	"github.com/no-dal/ndl-ce/internal/qemu"
)

// StartQemuProto is a typed Execute method.
func (c Client) StartQemuProto(ctx context.Context, spec qemu.Spec) (qemu.Result, error) {
	res, err := c.rpc().Execute(ctx, connect.NewRequest(&agentv1.ExecuteRequest{
		Method: &agentv1.ExecuteRequest_QemuProtoStart{QemuProtoStart: &agentv1.QemuProtoStart{
			WorkloadId: spec.WorkloadID, VolumeId: spec.VolumeID, DiskPath: spec.DiskPath,
			DiskFormat: spec.DiskFormat, Cpus: int32(spec.CPUs), MemoryBytes: spec.MemoryBytes,
			Machine: spec.Machine, Accel: spec.Accel, Autostart: spec.Autostart,
		}},
	}))
	if err != nil {
		return qemu.Result{}, err
	}
	var out qemu.Result
	if err := json.Unmarshal(res.Msg.GetResultJson(), &out); err != nil {
		return qemu.Result{}, err
	}
	return out, nil
}

// StopQemuProto is a typed Execute method. force uses systemd SIGKILL.
func (c Client) StopQemuProto(ctx context.Context, workloadID string) (qemu.Result, error) {
	return c.stopQemu(ctx, workloadID, false)
}

// KillQemuProto force-stops the prototype unit.
func (c Client) KillQemuProto(ctx context.Context, workloadID string) (qemu.Result, error) {
	return c.stopQemu(ctx, workloadID, true)
}

func (c Client) stopQemu(ctx context.Context, workloadID string, force bool) (qemu.Result, error) {
	res, err := c.rpc().Execute(ctx, connect.NewRequest(&agentv1.ExecuteRequest{
		Method: &agentv1.ExecuteRequest_QemuProtoStop{QemuProtoStop: &agentv1.QemuProtoStop{
			WorkloadId: workloadID,
			Force:      force,
		}},
	}))
	if err != nil {
		return qemu.Result{}, err
	}
	var out qemu.Result
	if err := json.Unmarshal(res.Msg.GetResultJson(), &out); err != nil {
		return qemu.Result{}, err
	}
	return out, nil
}

// StatusQemuProto observes unit and QMP state.
func (c Client) StatusQemuProto(ctx context.Context, workloadID string) (qemu.Observed, error) {
	res, err := c.rpc().Execute(ctx, connect.NewRequest(&agentv1.ExecuteRequest{
		Method: &agentv1.ExecuteRequest_QemuProtoStatus{QemuProtoStatus: &agentv1.QemuProtoStatus{
			WorkloadId: workloadID,
		}},
	}))
	if err != nil {
		return qemu.Observed{}, err
	}
	var out qemu.Observed
	if err := json.Unmarshal(res.Msg.GetResultJson(), &out); err != nil {
		return qemu.Observed{}, err
	}
	return out, nil
}
