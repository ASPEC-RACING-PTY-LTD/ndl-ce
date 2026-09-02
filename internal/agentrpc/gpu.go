package agentrpc

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"connectrpc.com/connect"
	agentv1 "github.com/no-dal/ndl-ce/gen/nodal/agent/v1"
	"github.com/no-dal/ndl-ce/internal/gpu"
	"github.com/no-dal/ndl-ce/internal/hostos"
	"github.com/no-dal/ndl-ce/internal/hostos/debian"
)

func (h *Handler) execGPUAssign(ctx context.Context, m *agentv1.GPUAssign) (*connect.Response[agentv1.ExecuteResponse], error) {
	req := gpu.AssignRequest{
		Action:      strings.TrimSpace(m.GetAction()),
		GPUID:       strings.TrimSpace(m.GetGpuId()),
		WorkloadID:  strings.TrimSpace(m.GetWorkloadId()),
		Mode:        strings.TrimSpace(m.GetMode()),
		Exclusive:   m.GetExclusive(),
		PCIDevices:  append([]string{}, m.GetPciDevices()...),
		DeviceNodes: append([]string{}, m.GetDeviceNodes()...),
		ACSOverride: m.GetAcsOverride(),
		DryRun:      m.GetDryRun(),
	}
	if err := gpu.RefuseACSOverride(req.ACSOverride); err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	if req.Action == "runtime-install" || req.Action == "runtime-status" {
		p, err := h.lookupPlatform()
		if err != nil {
			return nil, connect.NewError(connect.CodeInternal, err)
		}
		var run hostos.ExecFunc
		if !h.SkipHostCmds && req.Action == "runtime-install" && !req.DryRun {
			run = runTypedArgv
		}
		st, err := gpu.RunRuntimeInstall(ctx, p, req.DryRun || h.SkipHostCmds, run)
		if req.Action == "runtime-status" {
			st = gpu.EvaluateRuntime(p, nil)
			err = nil
		}
		if err != nil {
			return nil, connect.NewError(connect.CodeInvalidArgument, err)
		}
		return connect.NewResponse(&agentv1.ExecuteResponse{Ok: true, Message: req.Action, ResultJson: mustJSON(st)}), nil
	}
	if _, err := gpu.ParseGPUID(req.GPUID); err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	for i, addr := range req.PCIDevices {
		clean, err := gpu.ParseGPUID(addr)
		if err != nil {
			return nil, connect.NewError(connect.CodeInvalidArgument, err)
		}
		req.PCIDevices[i] = clean
	}
	for _, node := range req.DeviceNodes {
		if !gpu.AllowDeviceNode(node) {
			return nil, connect.NewError(connect.CodeInvalidArgument, errDeviceNode)
		}
	}
	res := gpu.AssignResult{Status: gpu.StatusAssigned, PCIDevices: req.PCIDevices, DeviceNodes: req.DeviceNodes}
	if req.Action == "unassign" {
		res.Status = "unassigned"
		if req.Mode == gpu.ModeVFIO {
			if err := h.applyVFIO(ctx, req.WorkloadID, req.PCIDevices, true); err != nil {
				res.Status = gpu.StatusFailed
				res.Reason = err.Error()
				return connect.NewResponse(&agentv1.ExecuteResponse{Ok: true, Message: "unassign", ResultJson: mustJSON(res)}), nil
			}
			res.Argv = h.vfioArgv(req.PCIDevices, true)
		} else if h.Workloads != nil {
			if err := h.Workloads.ApplyGPUDevices(req.WorkloadID, nil); err != nil {
				res.Reason = err.Error()
			}
		}
		return connect.NewResponse(&agentv1.ExecuteResponse{Ok: true, Message: "unassign", ResultJson: mustJSON(res)}), nil
	}
	if req.Mode == gpu.ModeVFIO {
		if err := h.applyVFIO(ctx, req.WorkloadID, req.PCIDevices, false); err != nil {
			res.Status = gpu.StatusFailed
			res.Reason = err.Error()
			return connect.NewResponse(&agentv1.ExecuteResponse{Ok: false, Message: req.Action, ResultJson: mustJSON(res)}), nil
		}
		res.Argv = h.vfioArgv(req.PCIDevices, false)
	} else if h.Workloads != nil || h.OCI != nil {
		var err error
		if h.Workloads != nil {
			err = h.Workloads.ApplyGPUDevices(req.WorkloadID, req.DeviceNodes)
		}
		if h.OCI != nil && (h.Workloads == nil || err != nil) {
			err = h.OCI.ApplyGPUDevices(req.WorkloadID, req.DeviceNodes)
		}
		if err != nil {
			res.Status = gpu.StatusFailed
			res.Reason = err.Error()
			return connect.NewResponse(&agentv1.ExecuteResponse{Ok: false, Message: req.Action, ResultJson: mustJSON(res)}), nil
		}
	}
	if h.SkipHostCmds {
		res.Status = "unavailable"
		if res.Reason == "" {
			res.Reason = "host commands skipped; GPU was not assigned"
		}
		return connect.NewResponse(&agentv1.ExecuteResponse{Ok: false, Message: req.Action, ResultJson: mustJSON(res)}), nil
	}
	return connect.NewResponse(&agentv1.ExecuteResponse{Ok: true, Message: req.Action, ResultJson: mustJSON(res)}), nil
}

func (h *Handler) vfioArgv(addrs []string, restore bool) []string {
	var out []string
	for _, addr := range addrs {
		if restore {
			out = append(out, strings.Join(debian.GPUDriverctlRestoreArgv(addr), " "))
			continue
		}
		out = append(out, strings.Join(debian.GPUDriverctlOverrideArgv(addr), " "))
	}
	return out
}

func (h *Handler) applyVFIO(ctx context.Context, workloadID string, hosts []string, restore bool) error {
	for _, addr := range hosts {
		argv := debian.GPUDriverctlOverrideArgv(addr)
		if restore {
			argv = debian.GPUDriverctlRestoreArgv(addr)
		}
		if err := validateDriverctlArgv(argv); err != nil {
			return err
		}
		if !h.SkipHostCmds {
			if out, err := runTypedArgv(ctx, argv); err != nil {
				return fmt.Errorf("driverctl: %s %w", strings.TrimSpace(out), err)
			}
		}
	}
	if h.QEMU == nil {
		return nil
	}
	qemuHosts := hosts
	if restore {
		qemuHosts = nil
	}
	if err := h.QEMU.ApplyVFIOHost(workloadID, qemuHosts); err != nil {
		if h.SkipHostCmds {
			return nil
		}
		return err
	}
	return nil
}

func validateDriverctlArgv(argv []string) error {
	if len(argv) < 3 || argv[0] != "/usr/sbin/driverctl" {
		return fmt.Errorf("driverctl argv is not typed")
	}
	if argv[1] != "set-override" && argv[1] != "unset-override" {
		return fmt.Errorf("driverctl action is not typed")
	}
	if _, err := gpu.ParseGPUID(argv[2]); err != nil {
		return err
	}
	if argv[1] == "set-override" && (len(argv) != 4 || argv[3] != "vfio-pci") {
		return fmt.Errorf("VFIO override driver must be vfio-pci")
	}
	return nil
}

var errDeviceNode = errString("device node is not allowlisted")

type errString string

func (e errString) Error() string { return string(e) }

func (c Client) GPUAssign(ctx context.Context, req gpu.AssignRequest) (gpu.AssignResult, error) {
	res, err := c.rpc().Execute(ctx, connect.NewRequest(&agentv1.ExecuteRequest{
		Method: &agentv1.ExecuteRequest_GpuAssign{GpuAssign: &agentv1.GPUAssign{
			Action: req.Action, GpuId: req.GPUID, WorkloadId: req.WorkloadID, Mode: req.Mode,
			Exclusive: req.Exclusive, PciDevices: req.PCIDevices, DeviceNodes: req.DeviceNodes,
			AcsOverride: req.ACSOverride, DryRun: req.DryRun,
		}},
	}))
	if err != nil {
		return gpu.AssignResult{}, err
	}
	var out gpu.AssignResult
	if err := json.Unmarshal(res.Msg.GetResultJson(), &out); err != nil {
		return gpu.AssignResult{}, err
	}
	return out, nil
}
