package agentrpc

import (
	"context"
	"encoding/json"
	"strings"

	"connectrpc.com/connect"
	agentv1 "github.com/no-dal/ndl-ce/gen/nodal/agent/v1"
	"github.com/no-dal/ndl-ce/internal/storage"
)

func (h *Handler) datastore() storage.DatastoreEngine {
	if h.Datastore != nil {
		e := *h.Datastore
		e.SkipHostCmds = e.SkipHostCmds || h.SkipHostCmds
		return e
	}
	return storage.DatastoreEngine{SkipHostCmds: h.SkipHostCmds, Run: func(ctx context.Context, argv []string) (string, error) {
		return runTypedArgv(ctx, argv)
	}}
}

func (h *Handler) execDatastore(ctx context.Context, m *agentv1.Datastore) (*connect.Response[agentv1.ExecuteResponse], error) {
	op := storage.DatastoreOp{
		Action: strings.TrimSpace(m.GetAction()), PoolID: strings.TrimSpace(m.GetPoolId()),
		Kind: m.GetKind(), Locator: m.GetLocator(), Portal: m.GetPortal(),
		Username: m.GetUsername(), Password: m.GetPassword(), IQN: m.GetIqn(),
	}
	res, err := h.datastore().Apply(ctx, op)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	return connect.NewResponse(&agentv1.ExecuteResponse{Ok: res.Status != storage.StatusFailed, Message: op.Action, ResultJson: mustJSON(res)}), nil
}

func (c Client) Datastore(ctx context.Context, op storage.DatastoreOp) (storage.DatastoreResult, error) {
	res, err := c.rpc().Execute(ctx, connect.NewRequest(&agentv1.ExecuteRequest{
		Method: &agentv1.ExecuteRequest_Datastore{Datastore: &agentv1.Datastore{
			Action: op.Action, PoolId: op.PoolID, Kind: op.Kind, Locator: op.Locator,
			Portal: op.Portal, Username: op.Username, Password: op.Password, Iqn: op.IQN,
		}},
	}))
	if err != nil {
		return storage.DatastoreResult{}, err
	}
	var out storage.DatastoreResult
	if err := json.Unmarshal(res.Msg.GetResultJson(), &out); err != nil {
		return storage.DatastoreResult{}, err
	}
	return out, nil
}
