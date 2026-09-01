package agentrpc

import (
	"context"
	"encoding/json"
	"strings"

	"connectrpc.com/connect"
	agentv1 "github.com/no-dal/ndl-ce/gen/nodal/agent/v1"
	"github.com/no-dal/ndl-ce/internal/storage"
)

func (h *Handler) lvm() storage.LVMEngine {
	if h.LVM != nil {
		e := *h.LVM
		e.SkipHostCmds = e.SkipHostCmds || h.SkipHostCmds
		return e
	}
	return storage.LVMEngine{SkipHostCmds: h.SkipHostCmds, Run: func(ctx context.Context, argv []string) (string, error) {
		return runTypedArgv(ctx, argv)
	}}
}

func (h *Handler) execLVMPool(ctx context.Context, m *agentv1.LVMPool) (*connect.Response[agentv1.ExecuteResponse], error) {
	op := storage.LVMOp{
		Action: strings.TrimSpace(m.GetAction()), PoolID: strings.TrimSpace(m.GetPoolId()),
		Name: m.GetName(), VGUUID: m.GetVgUuid(), Disks: append([]string{}, m.GetDisks()...),
		VolumeID: m.GetVolumeId(), Class: m.GetClass(), SizeBytes: m.GetSizeBytes(),
		Snapshot: m.GetSnapshot(),
	}
	res, err := h.lvm().Apply(ctx, op)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	return connect.NewResponse(&agentv1.ExecuteResponse{Ok: res.Status != storage.StatusFailed, Message: op.Action, ResultJson: mustJSON(res)}), nil
}

func (c Client) LVMPool(ctx context.Context, op storage.LVMOp) (storage.LVMResult, error) {
	res, err := c.rpc().Execute(ctx, connect.NewRequest(&agentv1.ExecuteRequest{
		Method: &agentv1.ExecuteRequest_LvmPool{LvmPool: &agentv1.LVMPool{
			Action: op.Action, PoolId: op.PoolID, Name: op.Name, VgUuid: op.VGUUID, Disks: op.Disks,
			VolumeId: op.VolumeID, Class: op.Class, SizeBytes: op.SizeBytes, Snapshot: op.Snapshot,
		}},
	}))
	if err != nil {
		return storage.LVMResult{}, err
	}
	var out storage.LVMResult
	if err := json.Unmarshal(res.Msg.GetResultJson(), &out); err != nil {
		return storage.LVMResult{}, err
	}
	return out, nil
}
