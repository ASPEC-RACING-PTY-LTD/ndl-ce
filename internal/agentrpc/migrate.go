package agentrpc

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"connectrpc.com/connect"
	agentv1 "github.com/no-dal/ndl-ce/gen/nodal/agent/v1"
	"github.com/no-dal/ndl-ce/internal/migrate"
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

// PrepareDest prepares incoming on the local unix agent. Dest on a worker
// must not be started here; the control plane refuses that path first.
func (c Client) PrepareDest(ctx context.Context, req migrate.Request) error {
	m := &agentv1.ComputeMigrate{
		Action: "prepare_incoming", WorkloadId: req.WorkloadID,
		Cpus: int32(req.CPUs), MemoryBytes: req.MemoryBytes,
		Machine: req.Machine, Accel: req.Accel,
	}
	if len(req.Disks) > 0 {
		m.VolumeId = req.Disks[0].VolumeID
		m.DiskPath = req.Disks[0].DestPath
		if m.DiskPath == "" {
			m.DiskPath = req.Disks[0].SourcePath
		}
	}
	_, err := c.ComputeMigrate(ctx, m)
	return err
}

func (c Client) CopyVolume(ctx context.Context, vol migrate.VolumeCopy) error {
	_, err := c.ComputeMigrate(ctx, &agentv1.ComputeMigrate{
		Action: "copy_volume", VolumeId: vol.VolumeID,
		SourcePath: vol.SourcePath, DestPath: vol.DestPath,
	})
	return err
}

func (c Client) StopSource(ctx context.Context, id string) error {
	_, err := c.ComputeMigrate(ctx, &agentv1.ComputeMigrate{Action: "stop_source", WorkloadId: id})
	return err
}

func (c Client) StartDest(ctx context.Context, id string) error {
	_, err := c.ComputeMigrate(ctx, &agentv1.ComputeMigrate{Action: "start_offline", WorkloadId: id})
	return err
}

func (c Client) LiveMigrate(ctx context.Context, id string) error {
	uri := (&qemu.Engine{}).IncomingURI(id)
	_, err := c.ComputeMigrate(ctx, &agentv1.ComputeMigrate{
		Action: "live_migrate", WorkloadId: id, Uri: uri,
	})
	return err
}

func (c Client) AbortDest(ctx context.Context, id string) error {
	_, err := c.ComputeMigrate(ctx, &agentv1.ComputeMigrate{Action: "abort_incoming", WorkloadId: id})
	return err
}

func (c Client) SourceRunning(ctx context.Context, id string) bool {
	obs, err := c.StatusQemuProto(ctx, id)
	if err != nil {
		return true
	}
	return obs.UnitActive || obs.Status == qemu.StatusRunning
}
