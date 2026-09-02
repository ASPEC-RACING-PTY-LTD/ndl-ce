package agentrpc

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"connectrpc.com/connect"
	agentv1 "github.com/no-dal/ndl-ce/gen/nodal/agent/v1"
	"github.com/no-dal/ndl-ce/internal/qemu"
	"github.com/no-dal/ndl-ce/internal/vmspec"
)

func (h *Handler) execVMPrepare(ctx context.Context, m *agentv1.VMPrepare) (*connect.Response[agentv1.ExecuteResponse], error) {
	if err := requireWorkloadUUID(m.GetWorkloadId()); err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	var launch vmspec.Launch
	if err := json.Unmarshal(m.GetLaunchJson(), &launch); err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("launch json is invalid"))
	}
	launch.WorkloadID = strings.TrimSpace(m.GetWorkloadId())
	if launch.Accel == "" {
		launch.Accel = qemu.DetectAccel()
	}
	if launch.Firmware.Mode == vmspec.FirmwareUEFI && launch.Firmware.CodePath == "" {
		launch.Firmware.CodePath = qemu.DetectFirmware()
	}
	if m.GetUserData() != "" {
		if err := h.qemu().WriteNoCloudSeed(launch.WorkloadID, m.GetUserData()); err != nil {
			return nil, connect.NewError(connect.CodeFailedPrecondition, err)
		}
	}
	source := qemu.ConvertRequest{
		SourcePath: m.GetSourcePath(), SourceFormat: m.GetSourceFormat(),
		DestPath: m.GetDestPath(), DestFormat: m.GetDestFormat(),
	}
	res, err := h.qemu().PrepareLaunch(ctx, launch, source)
	if err != nil {
		return nil, connect.NewError(connect.CodeFailedPrecondition, err)
	}
	return connect.NewResponse(&agentv1.ExecuteResponse{Ok: true, Message: "prepared", ResultJson: mustJSON(res)}), nil
}

func (h *Handler) execVMLifecycle(ctx context.Context, m *agentv1.VMLifecycle) (*connect.Response[agentv1.ExecuteResponse], error) {
	if err := requireWorkloadUUID(m.GetWorkloadId()); err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	id := strings.TrimSpace(m.GetWorkloadId())
	var err error
	switch strings.TrimSpace(m.GetAction()) {
	case "start":
		err = h.qemu().Start(ctx, id)
		if err == nil {
			err = h.qemu().EnableAutostart(ctx, id, m.GetAutostart())
		}
	case "stop":
		err = h.qemu().Stop(ctx, id)
	case "restart":
		err = h.qemu().Restart(ctx, id)
	case "force-stop", "force_stop", "kill":
		err = h.qemu().ForceStop(ctx, id)
	case "delete-runtime", "cleanup":
		err = h.qemu().DeleteRuntime(ctx, id)
	case "autostart":
		err = h.qemu().EnableAutostart(ctx, id, m.GetAutostart())
	default:
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("unsupported vm action"))
	}
	if err != nil {
		return nil, connect.NewError(connect.CodeFailedPrecondition, err)
	}
	obs := h.qemu().Observe(ctx, id)
	return connect.NewResponse(&agentv1.ExecuteResponse{Ok: true, Message: m.GetAction(), ResultJson: mustJSON(obs)}), nil
}

func (h *Handler) execVMQueryPCI(ctx context.Context, m *agentv1.VMQueryPCI) (*connect.Response[agentv1.ExecuteResponse], error) {
	if err := requireWorkloadUUID(m.GetWorkloadId()); err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	obs := h.qemu().Observe(ctx, strings.TrimSpace(m.GetWorkloadId()))
	return connect.NewResponse(&agentv1.ExecuteResponse{Ok: true, Message: "query-pci", ResultJson: mustJSON(obs)}), nil
}

func (h *Handler) execVMSnapshot(ctx context.Context, m *agentv1.VMSnapshot) (*connect.Response[agentv1.ExecuteResponse], error) {
	if err := requireWorkloadUUID(m.GetWorkloadId()); err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	res, err := h.qemu().OverlayDisk(ctx, qemu.OverlayRequest{
		Action: m.GetAction(), WorkloadID: strings.TrimSpace(m.GetWorkloadId()),
		OverlayPath: m.GetOverlayPath(), BackingPath: m.GetBackingPath(),
		ChainDepth: int(m.GetChainDepth()), ChainMax: int(m.GetChainMax()),
	})
	if err != nil {
		return nil, connect.NewError(connect.CodeFailedPrecondition, err)
	}
	return connect.NewResponse(&agentv1.ExecuteResponse{Ok: true, Message: m.GetAction(), ResultJson: mustJSON(res)}), nil
}

func (h *Handler) execBackupCopy(ctx context.Context, m *agentv1.BackupCopy) (*connect.Response[agentv1.ExecuteResponse], error) {
	res, err := h.qemu().CopyOffline(ctx, m.GetAction(), m.GetSourcePath(), m.GetDestPath())
	if err != nil {
		return nil, connect.NewError(connect.CodeFailedPrecondition, err)
	}
	return connect.NewResponse(&agentv1.ExecuteResponse{Ok: true, Message: m.GetAction(), ResultJson: mustJSON(res)}), nil
}
