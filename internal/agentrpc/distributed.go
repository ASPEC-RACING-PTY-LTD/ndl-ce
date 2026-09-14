package agentrpc

import (
	"context"
	"encoding/json"
	"strings"

	"connectrpc.com/connect"
	agentv1 "github.com/no-dal/ndl-ce/gen/nodal/agent/v1"
	"github.com/no-dal/ndl-ce/internal/storage"
)

func (h *Handler) distributed() storage.DistributedEngine {
	if h.Distributed != nil {
		e := *h.Distributed
		e.SkipHostCmds = e.SkipHostCmds || h.SkipHostCmds
		return e
	}
	return storage.DistributedEngine{SkipHostCmds: h.SkipHostCmds, Run: func(ctx context.Context, argv []string) (string, error) {
		return runTypedArgv(ctx, argv)
	}}
}

func (h *Handler) execDistributed(ctx context.Context, m *agentv1.Distributed) (*connect.Response[agentv1.ExecuteResponse], error) {
	op := storage.DistributedOp{
		Action: strings.TrimSpace(m.GetAction()), PoolID: strings.TrimSpace(m.GetPoolId()),
		Locator: m.GetLocator(), CephPool: m.GetCephPool(), CephUser: m.GetCephUser(),
		CephxKey: m.GetCephxKey(), Keyring: m.GetKeyringPath(), VolumeID: m.GetVolumeId(),
		Class: m.GetClass(), SizeBytes: m.GetSizeBytes(), Disk: m.GetDisk(), Image: m.GetImage(),
		BackendRef: m.GetBackendRef(), RootDevice: m.GetRootDevice(),
	}
	res, err := h.distributed().Apply(ctx, op)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	return connect.NewResponse(&agentv1.ExecuteResponse{Ok: res.Status != storage.StatusFailed, Message: op.Action, ResultJson: mustJSON(res)}), nil
}

func (c Client) Distributed(ctx context.Context, op storage.DistributedOp) (storage.DistributedResult, error) {
	res, err := c.rpc().Execute(ctx, connect.NewRequest(&agentv1.ExecuteRequest{
		Method: &agentv1.ExecuteRequest_Distributed{Distributed: &agentv1.Distributed{
			Action: op.Action, PoolId: op.PoolID, Locator: op.Locator, CephPool: op.CephPool,
			CephUser: op.CephUser, CephxKey: op.CephxKey, KeyringPath: op.Keyring,
			VolumeId: op.VolumeID, Class: op.Class, SizeBytes: op.SizeBytes, Disk: op.Disk,
			Image: op.Image, BackendRef: op.BackendRef, RootDevice: op.RootDevice,
		}},
	}))
	if err != nil {
		return storage.DistributedResult{}, err
	}
	var out storage.DistributedResult
	if err := json.Unmarshal(res.Msg.GetResultJson(), &out); err != nil {
		return storage.DistributedResult{}, err
	}
	return out, nil
}
