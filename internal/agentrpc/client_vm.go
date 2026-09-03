package agentrpc

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"connectrpc.com/connect"
	agentv1 "github.com/no-dal/ndl-ce/gen/nodal/agent/v1"
	"github.com/no-dal/ndl-ce/internal/guest"
	"github.com/no-dal/ndl-ce/internal/qemu"
	"github.com/no-dal/ndl-ce/internal/storage"
	"github.com/no-dal/ndl-ce/internal/vmspec"
)

func executeOK(res *connect.Response[agentv1.ExecuteResponse], err error) error {
	if err != nil {
		return err
	}
	if res == nil || res.Msg == nil || !res.Msg.GetOk() {
		msg := "agent execute failed"
		if res != nil && res.Msg != nil && strings.TrimSpace(res.Msg.GetMessage()) != "" {
			msg = res.Msg.GetMessage()
		}
		return fmt.Errorf("%s", msg)
	}
	return nil
}

// VMPrepareRequest is a typed product VM prepare. No shell strings.
type VMPrepareRequest struct {
	Launch       vmspec.Launch
	UserData     string
	SourcePath   string
	SourceFormat string
	DestPath     string
	DestFormat   string
}

func (c Client) PrepareVM(ctx context.Context, req VMPrepareRequest) (qemu.Result, error) {
	raw, err := json.Marshal(req.Launch)
	if err != nil {
		return qemu.Result{}, err
	}
	res, err := c.rpc().Execute(ctx, connect.NewRequest(&agentv1.ExecuteRequest{
		Method: &agentv1.ExecuteRequest_VmPrepare{VmPrepare: &agentv1.VMPrepare{
			WorkloadId: req.Launch.WorkloadID, LaunchJson: raw, UserData: req.UserData,
			SourcePath: req.SourcePath, SourceFormat: req.SourceFormat,
			DestPath: req.DestPath, DestFormat: req.DestFormat,
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

func (c Client) LifecycleVM(ctx context.Context, id, action string, autostart bool) (qemu.Observed, error) {
	res, err := c.rpc().Execute(ctx, connect.NewRequest(&agentv1.ExecuteRequest{
		Method: &agentv1.ExecuteRequest_VmLifecycle{VmLifecycle: &agentv1.VMLifecycle{
			WorkloadId: id, Action: action, Autostart: autostart,
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

func (c Client) QueryPCIVM(ctx context.Context, id string) (qemu.Observed, error) {
	res, err := c.rpc().Execute(ctx, connect.NewRequest(&agentv1.ExecuteRequest{
		Method: &agentv1.ExecuteRequest_VmQueryPci{VmQueryPci: &agentv1.VMQueryPCI{WorkloadId: id}},
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

func (c Client) SnapshotVM(ctx context.Context, req qemu.OverlayRequest) (qemu.OverlayResult, error) {
	res, err := c.rpc().Execute(ctx, connect.NewRequest(&agentv1.ExecuteRequest{
		Method: &agentv1.ExecuteRequest_VmSnapshot{VmSnapshot: &agentv1.VMSnapshot{
			WorkloadId: req.WorkloadID, Action: req.Action,
			OverlayPath: req.OverlayPath, BackingPath: req.BackingPath,
			ChainDepth: int32(req.ChainDepth), ChainMax: int32(req.ChainMax),
		}},
	}))
	if err != nil {
		return qemu.OverlayResult{}, err
	}
	var out qemu.OverlayResult
	if err := json.Unmarshal(res.Msg.GetResultJson(), &out); err != nil {
		return qemu.OverlayResult{}, err
	}
	return out, nil
}

func (c Client) CopyBackup(ctx context.Context, action, src, dest string) (storage.CopyResult, error) {
	res, err := c.rpc().Execute(ctx, connect.NewRequest(&agentv1.ExecuteRequest{
		Method: &agentv1.ExecuteRequest_BackupCopy{BackupCopy: &agentv1.BackupCopy{
			Action: action, SourcePath: src, DestPath: dest,
		}},
	}))
	if err != nil {
		return storage.CopyResult{}, err
	}
	var out storage.CopyResult
	if err := json.Unmarshal(res.Msg.GetResultJson(), &out); err != nil {
		return storage.CopyResult{}, err
	}
	return out, nil
}

func (c Client) ConvertImport(ctx context.Context, req qemu.ConvertRequest) error {
	_, err := c.rpc().Execute(ctx, connect.NewRequest(&agentv1.ExecuteRequest{
		Method: &agentv1.ExecuteRequest_DiskConvert{DiskConvert: &agentv1.DiskConvert{
			SourcePath: req.SourcePath, DestPath: req.DestPath,
			SourceFormat: req.SourceFormat, DestFormat: req.DestFormat,
		}},
	}))
	return err
}

func (c Client) ExtractArchive(ctx context.Context, src, dest string) error {
	_, err := c.rpc().Execute(ctx, connect.NewRequest(&agentv1.ExecuteRequest{
		Method: &agentv1.ExecuteRequest_ArchiveExtract{ArchiveExtract: &agentv1.ArchiveExtract{
			SourcePath: src, DestPath: dest,
		}},
	}))
	return err
}

func (c Client) ApplyUSB(ctx context.Context, id string, usbs []vmspec.LaunchUSB) error {
	if len(usbs) == 0 {
		res, err := c.rpc().Execute(ctx, connect.NewRequest(&agentv1.ExecuteRequest{
			Method: &agentv1.ExecuteRequest_VmHotplug{VmHotplug: &agentv1.VMHotplug{
				WorkloadId: id, Action: "del", DeviceKind: "usb-host",
			}},
		}))
		return executeOK(res, err)
	}
	for _, u := range usbs {
		res, err := c.rpc().Execute(ctx, connect.NewRequest(&agentv1.ExecuteRequest{
			Method: &agentv1.ExecuteRequest_VmHotplug{VmHotplug: &agentv1.VMHotplug{
				WorkloadId: id, Action: "add", DeviceKind: "usb-host",
				VendorId: u.Vendor, ProductId: u.Product, Address: u.Address,
			}},
		}))
		if err := executeOK(res, err); err != nil {
			return err
		}
	}
	return nil
}

func (c Client) HotplugUSB(ctx context.Context, id string, add bool, usb vmspec.LaunchUSB) error {
	action := "add"
	if !add {
		action = "del"
	}
	res, err := c.rpc().Execute(ctx, connect.NewRequest(&agentv1.ExecuteRequest{
		Method: &agentv1.ExecuteRequest_VmHotplug{VmHotplug: &agentv1.VMHotplug{
			WorkloadId: id, Action: action, DeviceKind: "usb-host",
			VendorId: usb.Vendor, ProductId: usb.Product, Address: usb.Address,
		}},
	}))
	return executeOK(res, err)
}

func (c Client) ApplyVFIO(ctx context.Context, id string, hosts []string) error {
	res, err := c.rpc().Execute(ctx, connect.NewRequest(&agentv1.ExecuteRequest{
		Method: &agentv1.ExecuteRequest_VmHotplug{VmHotplug: &agentv1.VMHotplug{
			WorkloadId: id, Action: "add", DeviceKind: "vfio-pci",
			PciHosts: hosts,
		}},
	}))
	return executeOK(res, err)
}

func (c Client) GuestStatus(ctx context.Context, id string) (guest.Status, error) {
	res, err := c.rpc().Execute(ctx, connect.NewRequest(&agentv1.ExecuteRequest{
		Method: &agentv1.ExecuteRequest_VmGuest{VmGuest: &agentv1.VMGuest{
			WorkloadId: id, Action: "status",
		}},
	}))
	if err != nil {
		return guest.Status{}, err
	}
	var out guest.Status
	if err := json.Unmarshal(res.Msg.GetResultJson(), &out); err != nil {
		return guest.Status{}, err
	}
	return out, nil
}
