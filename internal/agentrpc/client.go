package agentrpc

import (
	"context"
	"encoding/json"
	"net"
	"net/http"

	"connectrpc.com/connect"
	agentv1 "github.com/no-dal/ndl-ce/gen/nodal/agent/v1"
	"github.com/no-dal/ndl-ce/gen/nodal/agent/v1/agentv1connect"
	"github.com/no-dal/ndl-ce/internal/inventory"
	"github.com/no-dal/ndl-ce/internal/metrics"
	"github.com/no-dal/ndl-ce/internal/transport"
	"time"
)

// Client is the control-plane southbound client.
type Client struct {
	Socket string
}

// Hello is the idle Observe-first ping.
func (c Client) Hello(ctx context.Context) error {
	_, err := c.rpc().Hello(ctx, connect.NewRequest(&agentv1.HelloRequest{}))
	return err
}

// Enroll performs local enroll.
func (c Client) Enroll(ctx context.Context, clusterID string) (string, json.RawMessage, error) {
	cli := c.rpc()
	res, err := cli.Enroll(ctx, connect.NewRequest(&agentv1.EnrollRequest{ClusterId: clusterID}))
	if err != nil {
		return "", nil, err
	}
	return res.Msg.GetNodeId(), HostPlatformJSON(res.Msg.GetHostPlatform()), nil
}

// Observe asks the agent for a fresh inventory snapshot.
func (c Client) Observe(ctx context.Context) (inventory.Inventory, error) {
	res, err := c.rpc().Observe(ctx, connect.NewRequest(&agentv1.ObserveRequest{}))
	if err != nil {
		return inventory.Inventory{}, err
	}
	return decodeInventory(res.Msg.GetInventoryJson())
}

// GetInventory returns the agent's cached observation.
func (c Client) GetInventory(ctx context.Context) (inventory.Inventory, error) {
	res, err := c.rpc().GetInventory(ctx, connect.NewRequest(&agentv1.GetInventoryRequest{}))
	if err != nil {
		return inventory.Inventory{}, err
	}
	return decodeInventory(res.Msg.GetInventoryJson())
}

// GetMetrics reads agent-side SQLite samples over RPC.
func (c Client) GetMetrics(ctx context.Context, from, to time.Time) (metrics.QueryResult, error) {
	res, err := c.rpc().GetMetrics(ctx, connect.NewRequest(&agentv1.GetMetricsRequest{
		From: from.UTC().Format(time.RFC3339),
		To:   to.UTC().Format(time.RFC3339),
	}))
	if err != nil {
		return metrics.QueryResult{Status: metrics.StatusUnavailable}, err
	}
	var out metrics.QueryResult
	if len(res.Msg.GetSeriesJson()) == 0 {
		return metrics.QueryResult{Status: metrics.Status(res.Msg.GetStatus())}, nil
	}
	if err := json.Unmarshal(res.Msg.GetSeriesJson(), &out); err != nil {
		return metrics.QueryResult{Status: metrics.StatusUnavailable}, err
	}
	return out, nil
}

func decodeInventory(raw []byte) (inventory.Inventory, error) {
	var inv inventory.Inventory
	if len(raw) == 0 {
		return inv, nil
	}
	if err := json.Unmarshal(raw, &inv); err != nil {
		return inventory.Inventory{}, err
	}
	return inv, nil
}

func (c Client) rpc() agentv1connect.AgentServiceClient {
	path := c.Socket
	if path == "" {
		path = transport.AgentSocket
	}
	httpClient := &http.Client{Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", path)
		},
	}}
	return agentv1connect.NewAgentServiceClient(httpClient, "http://local")
}
