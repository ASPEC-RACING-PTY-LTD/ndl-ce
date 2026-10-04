package agentrpc

import (
	"context"
	"encoding/json"

	"connectrpc.com/connect"
	agentv1 "github.com/no-dal/ndl-ce/gen/nodal/agent/v1"
)

// HostDisk reads or manages host disk protection on the agent.
func (c Client) HostDisk(ctx context.Context, action, category string, protect []string) (HostDiskResult, error) {
	res, err := c.rpc().Execute(ctx, connect.NewRequest(&agentv1.ExecuteRequest{
		Method: &agentv1.ExecuteRequest_HostDisk{HostDisk: &agentv1.HostDisk{
			Action: action, Category: category, Protect: protect,
		}},
	}))
	if err != nil {
		return HostDiskResult{}, err
	}
	var out HostDiskResult
	if err := json.Unmarshal(res.Msg.GetResultJson(), &out); err != nil {
		return HostDiskResult{}, err
	}
	return out, nil
}
