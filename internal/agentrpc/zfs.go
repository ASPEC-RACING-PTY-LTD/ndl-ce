package agentrpc

import (
	"context"
	"encoding/json"
	"strings"

	"connectrpc.com/connect"
	agentv1 "github.com/no-dal/ndl-ce/gen/nodal/agent/v1"
	"github.com/no-dal/ndl-ce/internal/storage"
)

func (h *Handler) zfs() storage.ZFSEngine {
	if h.ZFS != nil {
		e := *h.ZFS
		e.SkipHostCmds = e.SkipHostCmds || h.SkipHostCmds
		return e
	}
	return storage.ZFSEngine{SkipHostCmds: h.SkipHostCmds, Run: func(ctx context.Context, argv []string) (string, error) {
		return runTypedArgv(ctx, argv)
	}}
}

func (h *Handler) execZFSPool(ctx context.Context, m *agentv1.ZFSPool) (*connect.Response[agentv1.ExecuteResponse], error) {
	op := storage.ZFSOp{
		Action: strings.TrimSpace(m.GetAction()), PoolID: strings.TrimSpace(m.GetPoolId()),
		Name: m.GetName(), GUID: m.GetGuid(), Disks: append([]string{}, m.GetDisks()...),
		VolumeID: m.GetVolumeId(), Class: m.GetClass(), SizeBytes: m.GetSizeBytes(),
		Snapshot: m.GetSnapshot(), FromSnap: m.GetFromSnapshot(), DestPath: m.GetDestPath(), Force: m.GetForce(),
	}
	res, err := h.zfs().Apply(ctx, op)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	return connect.NewResponse(&agentv1.ExecuteResponse{Ok: res.Status != storage.StatusFailed, Message: op.Action, ResultJson: mustJSON(res)}), nil
}

func (c Client) ZFSPool(ctx context.Context, op storage.ZFSOp) (storage.ZFSResult, error) {
	res, err := c.rpc().Execute(ctx, connect.NewRequest(&agentv1.ExecuteRequest{
		Method: &agentv1.ExecuteRequest_ZfsPool{ZfsPool: &agentv1.ZFSPool{
			Action: op.Action, PoolId: op.PoolID, Name: op.Name, Guid: op.GUID, Disks: op.Disks,
			VolumeId: op.VolumeID, Class: op.Class, SizeBytes: op.SizeBytes,
			Snapshot: op.Snapshot, FromSnapshot: op.FromSnap, DestPath: op.DestPath, Force: op.Force,
		}},
	}))
	if err != nil {
		return storage.ZFSResult{}, err
	}
	var out storage.ZFSResult
	if err := json.Unmarshal(res.Msg.GetResultJson(), &out); err != nil {
		return storage.ZFSResult{}, err
	}
	return out, nil
}
