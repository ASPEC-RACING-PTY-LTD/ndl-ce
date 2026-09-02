package agentrpc

import (
	"context"
	"encoding/json"

	"connectrpc.com/connect"
	agentv1 "github.com/no-dal/ndl-ce/gen/nodal/agent/v1"
	"github.com/no-dal/ndl-ce/internal/ndnet"
)

func (h *Handler) execWireGuard(ctx context.Context, m *agentv1.WireGuard) (*connect.Response[agentv1.ExecuteResponse], error) {
	op := ndnet.WGOp{
		Action: m.GetAction(), PeerID: m.GetPeerId(), ListenPort: m.GetListenPort(),
		AddressCIDR: m.GetAddressCidr(), PrivateKeyFile: m.GetPrivateKeyFile(),
	}
	for _, p := range m.GetPeers() {
		op.Peers = append(op.Peers, ndnet.WGPeerSpec{
			PublicKey: p.GetPublicKey(), Endpoint: p.GetEndpoint(),
			AllowedIPs: p.GetAllowedIps(), PersistentKeepalive: p.GetPersistentKeepalive(),
		})
	}
	res, err := h.nets().ApplyWireGuard(ctx, op)
	if err != nil {
		return nil, connect.NewError(connect.CodeFailedPrecondition, err)
	}
	return connect.NewResponse(&agentv1.ExecuteResponse{Ok: true, Message: op.Action, ResultJson: mustJSON(res)}), nil
}

func protoWGPeers(peers []ndnet.WGPeerSpec) []*agentv1.WireGuardPeer {
	out := make([]*agentv1.WireGuardPeer, 0, len(peers))
	for _, p := range peers {
		out = append(out, &agentv1.WireGuardPeer{
			PublicKey: p.PublicKey, Endpoint: p.Endpoint,
			AllowedIps: p.AllowedIPs, PersistentKeepalive: p.PersistentKeepalive,
		})
	}
	return out
}

func (c Client) WireGuard(ctx context.Context, op ndnet.WGOp) (ndnet.WGResult, error) {
	res, err := c.rpc().Execute(ctx, connect.NewRequest(&agentv1.ExecuteRequest{
		Method: &agentv1.ExecuteRequest_Wireguard{Wireguard: &agentv1.WireGuard{
			Action: op.Action, PeerId: op.PeerID, ListenPort: op.ListenPort,
			AddressCidr: op.AddressCIDR, PrivateKeyFile: op.PrivateKeyFile, Peers: protoWGPeers(op.Peers),
		}},
	}))
	if err != nil {
		return ndnet.WGResult{}, err
	}
	var out ndnet.WGResult
	if err := json.Unmarshal(res.Msg.GetResultJson(), &out); err != nil {
		return ndnet.WGResult{}, err
	}
	return out, nil
}
