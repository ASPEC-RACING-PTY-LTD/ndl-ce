package agentrpc

import (
	"context"
	"fmt"
	"strings"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	agentv1 "github.com/no-dal/ndl-ce/gen/nodal/agent/v1"
	"github.com/no-dal/ndl-ce/internal/qemu"
)

func (h *Handler) qemu() *qemu.Engine {
	if h.QEMU != nil {
		return h.QEMU
	}
	return &qemu.Engine{}
}

func specFromQemuProtoStart(m *agentv1.QemuProtoStart) qemu.Spec {
	return qemu.Spec{
		WorkloadID:  strings.TrimSpace(m.GetWorkloadId()),
		VolumeID:    strings.TrimSpace(m.GetVolumeId()),
		DiskPath:    m.GetDiskPath(),
		DiskFormat:  m.GetDiskFormat(),
		CPUs:        int(m.GetCpus()),
		MemoryBytes: m.GetMemoryBytes(),
		Machine:     m.GetMachine(),
		Accel:       m.GetAccel(),
		Autostart:   m.GetAutostart(),
	}
}

func requireWorkloadUUID(id string) error {
	if _, err := uuid.Parse(strings.TrimSpace(id)); err != nil {
		return fmt.Errorf("workload_id must be a UUID")
	}
	return nil
}

func (h *Handler) execQemuProtoStart(ctx context.Context, m *agentv1.QemuProtoStart) (*connect.Response[agentv1.ExecuteResponse], error) {
	if err := requireWorkloadUUID(m.GetWorkloadId()); err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	spec := specFromQemuProtoStart(m)
	if h.qemu().AlreadyRunning(ctx, spec.WorkloadID) {
		obs := h.qemu().Observe(ctx, spec.WorkloadID)
		res := qemu.Result{WorkloadID: spec.WorkloadID, Status: obs.Status, Machine: obs.Machine, Accel: obs.Accel, Reason: obs.Reason, UnitActive: obs.UnitActive, RunningAs: obs.RunningAs}
		return connect.NewResponse(&agentv1.ExecuteResponse{Ok: true, Message: "already running", ResultJson: mustJSON(res)}), nil
	}
	if err := h.qemu().CleanupFailedLaunch(spec.WorkloadID); err != nil {
		return nil, connect.NewError(connect.CodeFailedPrecondition, err)
	}
	res, err := h.qemu().Prepare(spec)
	if err != nil {
		return nil, connect.NewError(connect.CodeFailedPrecondition, err)
	}
	if err := h.qemu().Start(ctx, spec.WorkloadID); err != nil {
		_ = h.qemu().CleanupFailedLaunch(spec.WorkloadID)
		return nil, connect.NewError(connect.CodeFailedPrecondition, err)
	}
	if spec.Autostart {
		if err := h.qemu().EnableAutostart(ctx, spec.WorkloadID, true); err != nil {
			return nil, connect.NewError(connect.CodeFailedPrecondition, err)
		}
	}
	obs := h.qemu().Observe(ctx, spec.WorkloadID)
	res.Status = obs.Status
	res.Reason = obs.Reason
	res.UnitActive = obs.UnitActive
	res.RunningAs = obs.RunningAs
	if obs.Machine != "" {
		res.Machine = obs.Machine
	}
	if obs.Accel != "" {
		res.Accel = obs.Accel
	}
	return connect.NewResponse(&agentv1.ExecuteResponse{Ok: true, Message: "started", ResultJson: mustJSON(res)}), nil
}

func (h *Handler) execQemuProtoStop(ctx context.Context, m *agentv1.QemuProtoStop) (*connect.Response[agentv1.ExecuteResponse], error) {
	if err := requireWorkloadUUID(m.GetWorkloadId()); err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	id := strings.TrimSpace(m.GetWorkloadId())
	var err error
	if m.GetForce() {
		err = h.qemu().ForceStop(ctx, id)
	} else {
		err = h.qemu().Stop(ctx, id)
	}
	if err != nil {
		return nil, connect.NewError(connect.CodeFailedPrecondition, err)
	}
	obs := h.qemu().Observe(ctx, id)
	return connect.NewResponse(&agentv1.ExecuteResponse{Ok: true, Message: "stopped", ResultJson: mustJSON(qemu.Result{
		WorkloadID: id, Status: obs.Status, Machine: obs.Machine, Accel: obs.Accel,
		Reason: obs.Reason, UnitActive: obs.UnitActive, RunningAs: obs.RunningAs,
	})}), nil
}

func (h *Handler) execQemuProtoStatus(ctx context.Context, m *agentv1.QemuProtoStatus) (*connect.Response[agentv1.ExecuteResponse], error) {
	if err := requireWorkloadUUID(m.GetWorkloadId()); err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	id := strings.TrimSpace(m.GetWorkloadId())
	obs := h.qemu().Observe(ctx, id)
	return connect.NewResponse(&agentv1.ExecuteResponse{Ok: true, Message: "status", ResultJson: mustJSON(obs)}), nil
}
