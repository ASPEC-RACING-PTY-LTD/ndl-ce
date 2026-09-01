package agentrpc

import (
	"context"
	"encoding/json"

	"connectrpc.com/connect"
	agentv1 "github.com/no-dal/ndl-ce/gen/nodal/agent/v1"
	"github.com/no-dal/ndl-ce/internal/qemu"
	"github.com/no-dal/ndl-ce/internal/vmspec"
)

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
