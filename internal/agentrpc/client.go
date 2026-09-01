package agentrpc

import (
	"context"
	"encoding/json"
	"net"
	"net/http"

	"connectrpc.com/connect"
	agentv1 "github.com/no-dal/ndl-ce/gen/nodal/agent/v1"
	"github.com/no-dal/ndl-ce/gen/nodal/agent/v1/agentv1connect"
	"github.com/no-dal/ndl-ce/internal/transport"
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
