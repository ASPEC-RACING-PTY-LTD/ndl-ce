package agentrpc

import (
	"context"
	"encoding/json"

	"connectrpc.com/connect"
	agentv1 "github.com/no-dal/ndl-ce/gen/nodal/agent/v1"
	"github.com/no-dal/ndl-ce/internal/hostos"
)

func (c Client) HostUpdate(ctx context.Context, req hostos.UpdateRequest) (hostos.UpdateResult, error) {
	res, err := c.rpc().Execute(ctx, connect.NewRequest(&agentv1.ExecuteRequest{
		Method: &agentv1.ExecuteRequest_HostUpdate{HostUpdate: &agentv1.HostUpdate{
			Action:       req.Action,
			Channel:      req.Channel,
			PackageName:  req.PackageName,
			Version:      req.Version,
			DryRun:       req.DryRun,
			CheckpointId: req.CheckpointID,
		}},
	}))
	if err != nil {
		return hostos.UpdateResult{}, err
	}
	var out hostos.UpdateResult
	if err := json.Unmarshal(res.Msg.GetResultJson(), &out); err != nil {
		return hostos.UpdateResult{}, err
	}
	return out, nil
}
