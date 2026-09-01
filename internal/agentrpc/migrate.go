package agentrpc

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"connectrpc.com/connect"
	agentv1 "github.com/no-dal/ndl-ce/gen/nodal/agent/v1"
	"github.com/no-dal/ndl-ce/internal/qemu"
)

func (h *Handler) execComputeMigrate(ctx context.Context, m *agentv1.ComputeMigrate) (*connect.Response[agentv1.ExecuteResponse], error) {
	id := strings.TrimSpace(m.GetWorkloadId())
	if err := requireWorkloadUUID(id); err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	action := strings.TrimSpace(m.GetAction())
	switch action {
	case "prepare_incoming":
		spec := qemu.Spec{
			WorkloadID: id, VolumeID: m.GetVolumeId(), DiskPath: m.GetDiskPath(),
			DiskFormat: m.GetDiskFormat(), CPUs: int(m.GetCpus()), MemoryBytes: m.GetMemoryBytes(),
			Machine: m.GetMachine(), Accel: m.GetAccel(), IncomingDefer: true,
		}
		res, err := h.qemu().PrepareIncoming(spec)
		if err != nil {
			return nil, connect.NewError(connect.CodeFailedPrecondition, err)
		}
		return connect.NewResponse(&agentv1.ExecuteResponse{Ok: true, Message: action, ResultJson: mustJSON(res)}), nil
	case "copy_volume":
		res, err := h.qemu().CopyOffline(ctx, qemu.BackupCopy, m.GetSourcePath(), m.GetDestPath())
		if err != nil {
			return nil, connect.NewError(connect.CodeFailedPrecondition, err)
		}
		return connect.NewResponse(&agentv1.ExecuteResponse{Ok: true, Message: action, ResultJson: mustJSON(res)}), nil
	case "live_migrate":
		if err := h.qemu().LiveMigrate(ctx, id, m.GetUri()); err != nil {
			return nil, connect.NewError(connect.CodeFailedPrecondition, err)
		}
		obs := h.qemu().Observe(ctx, id)
		return connect.NewResponse(&agentv1.ExecuteResponse{Ok: true, Message: action, ResultJson: mustJSON(obs)}), nil
	case "live_cancel":
		if err := h.qemu().CancelLiveMigrate(ctx, id); err != nil {
			return nil, connect.NewError(connect.CodeFailedPrecondition, err)
		}
		return connect.NewResponse(&agentv1.ExecuteResponse{Ok: true, Message: action}), nil
	case "abort_incoming":
		if err := h.qemu().AbortIncoming(ctx, id); err != nil {
			return nil, connect.NewError(connect.CodeFailedPrecondition, err)
		}
		return connect.NewResponse(&agentv1.ExecuteResponse{Ok: true, Message: action}), nil
	case "stop_source":
		if err := h.qemu().Stop(ctx, id); err != nil {
			return nil, connect.NewError(connect.CodeFailedPrecondition, err)
		}
		obs := h.qemu().Observe(ctx, id)
		return connect.NewResponse(&agentv1.ExecuteResponse{Ok: true, Message: action, ResultJson: mustJSON(obs)}), nil
	case "start_offline":
		if err := h.qemu().Start(ctx, id); err != nil {
			return nil, connect.NewError(connect.CodeFailedPrecondition, err)
		}
		obs := h.qemu().Observe(ctx, id)
		return connect.NewResponse(&agentv1.ExecuteResponse{Ok: true, Message: action, ResultJson: mustJSON(obs)}), nil
	default:
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("unknown compute.migrate action"))
	}
}

func (c Client) ComputeMigrate(ctx context.Context, m *agentv1.ComputeMigrate) (json.RawMessage, error) {
	res, err := c.rpc().Execute(ctx, connect.NewRequest(&agentv1.ExecuteRequest{
		Method: &agentv1.ExecuteRequest_ComputeMigrate{ComputeMigrate: m},
	}))
	if err != nil {
		return nil, err
	}
	return res.Msg.GetResultJson(), nil
}
