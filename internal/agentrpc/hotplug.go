package agentrpc

import (
	"context"
	"fmt"
	"strings"

	"connectrpc.com/connect"
	agentv1 "github.com/no-dal/ndl-ce/gen/nodal/agent/v1"
	"github.com/no-dal/ndl-ce/internal/qemu"
	"github.com/no-dal/ndl-ce/internal/vmspec"
)

func (h *Handler) execVMHotplug(ctx context.Context, m *agentv1.VMHotplug) (*connect.Response[agentv1.ExecuteResponse], error) {
	if err := requireWorkloadUUID(m.GetWorkloadId()); err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	id := strings.TrimSpace(m.GetWorkloadId())
	kind := strings.TrimSpace(m.GetDeviceKind())
	action := strings.TrimSpace(m.GetAction())
	switch kind {
	case "usb-host":
		usb := vmspec.LaunchUSB{
			Address: m.GetAddress(),
			Vendor:  m.GetVendorId(),
			Product: m.GetProductId(),
			ID:      vmspec.USBDeviceID(m.GetAddress()),
		}
		launch, _ := h.qemu().ReadLaunch(id)
		switch action {
		case "device_add", "add":
			usbs := append(launch.USBs, usb)
			if err := h.qemu().ApplyUSBHost(id, usbs); err != nil {
				return nil, connect.NewError(connect.CodeFailedPrecondition, err)
			}
			if err := h.qemu().HotplugUSB(id, true, usb); err != nil {
				return connect.NewResponse(&agentv1.ExecuteResponse{Ok: false, Message: err.Error()}), nil
			}
		case "device_del", "del":
			if err := h.qemu().HotplugUSB(id, false, usb); err != nil {
				return connect.NewResponse(&agentv1.ExecuteResponse{Ok: false, Message: err.Error()}), nil
			}
			kept := make([]vmspec.LaunchUSB, 0, len(launch.USBs))
			for _, u := range launch.USBs {
				if u.Address != usb.Address {
					kept = append(kept, u)
				}
			}
			if err := h.qemu().ApplyUSBHost(id, kept); err != nil {
				return nil, connect.NewError(connect.CodeFailedPrecondition, err)
			}
		default:
			return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("unsupported usb hotplug action"))
		}
	case "vfio-pci":
		hosts := append([]string{}, m.GetPciHosts()...)
		if h := strings.TrimSpace(m.GetPciHost()); h != "" {
			hosts = append(hosts, h)
		}
		if launch, err := h.qemu().ReadLaunch(id); err == nil {
			hosts = qemu.MergeHostAddrs(qemu.HostAddrsFromLaunch(launch), hosts)
		}
		if err := h.qemu().ApplyVFIOHost(id, hosts); err != nil {
			return nil, connect.NewError(connect.CodeFailedPrecondition, err)
		}
	default:
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("unsupported hotplug device"))
	}
	_ = ctx
	return connect.NewResponse(&agentv1.ExecuteResponse{Ok: true, Message: action}), nil
}
