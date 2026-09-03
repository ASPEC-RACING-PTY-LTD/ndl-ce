package agentrpc

import (
	"context"
	"encoding/json"

	"connectrpc.com/connect"
	agentv1 "github.com/no-dal/ndl-ce/gen/nodal/agent/v1"
	"github.com/no-dal/ndl-ce/internal/hostos/debian"
	"github.com/no-dal/ndl-ce/internal/ndnet"
)

func (h *Handler) nets() *ndnet.Engine {
	if h.Nets != nil {
		return h.Nets
	}
	return &ndnet.Engine{Render: debian.NetworkdFiles, SkipHostCmds: h.SkipHostCmds}
}

func specFromProto(id, name, kind, cidr string, dhcp, dns bool, uplink, confirm string, reservations []byte, arm bool) ndnet.Spec {
	spec := ndnet.Spec{
		NetworkID: id, Name: name, Kind: kind, IPv4CIDR: cidr, DHCP: dhcp, DNS: dns,
		UplinkIfName: uplink, ConfirmIfName: confirm, ArmRollback: arm,
	}
	if len(reservations) > 0 {
		_ = json.Unmarshal(reservations, &spec.Reservations)
	}
	return spec
}

func decodeNetworkHints(in []*agentv1.NetworkHint) []ndnet.Hint {
	out := make([]ndnet.Hint, 0, len(in))
	for _, h := range in {
		out = append(out, ndnet.Hint{
			NetworkID: h.GetNetworkId(), Kind: h.GetKind(),
			BridgeName: h.GetBridgeName(), UplinkIfName: h.GetUplinkIfname(),
		})
	}
	return out
}

func (h *Handler) observeNetworks(hints []ndnet.Hint) []byte {
	obs, err := h.nets().Observe(context.Background(), hints)
	if err != nil {
		return mustJSON(ndnet.Observation{})
	}
	return mustJSON(obs)
}

// GetNetworks observes desired networks. Missing objects stay unavailable.
func (h *Handler) GetNetworks(ctx context.Context, req *connect.Request[agentv1.GetNetworksRequest]) (*connect.Response[agentv1.GetNetworksResponse], error) {
	if err := h.authorize(ctx); err != nil {
		return nil, err
	}
	return connect.NewResponse(&agentv1.GetNetworksResponse{
		NetworkJson: h.observeNetworks(decodeNetworkHints(req.Msg.GetNetworks())),
	}), nil
}

func (h *Handler) execNetDryRun(ctx context.Context, m *agentv1.NetDryRun) (*connect.Response[agentv1.ExecuteResponse], error) {
	prev, err := h.nets().DryRun(ctx, specFromProto(
		m.GetNetworkId(), m.GetName(), m.GetKind(), m.GetIpv4Cidr(), m.GetDhcp(), m.GetDns(),
		m.GetUplinkIfname(), m.GetConfirmIfname(), m.GetReservationsJson(), false,
	))
	if err != nil {
		return nil, connect.NewError(connect.CodeFailedPrecondition, err)
	}
	return connect.NewResponse(&agentv1.ExecuteResponse{Ok: true, Message: "dry-run", ResultJson: mustJSON(prev)}), nil
}

func (h *Handler) execNetApply(ctx context.Context, m *agentv1.NetApply) (*connect.Response[agentv1.ExecuteResponse], error) {
	res, err := h.nets().Apply(ctx, specFromProto(
		m.GetNetworkId(), m.GetName(), m.GetKind(), m.GetIpv4Cidr(), m.GetDhcp(), m.GetDns(),
		m.GetUplinkIfname(), m.GetConfirmIfname(), m.GetReservationsJson(), m.GetArmRollback(),
	))
	if err != nil {
		return nil, connect.NewError(connect.CodeFailedPrecondition, err)
	}
	return connect.NewResponse(&agentv1.ExecuteResponse{Ok: true, Message: "applied", ResultJson: mustJSON(res)}), nil
}

func (h *Handler) execNetAdvanced(ctx context.Context, m *agentv1.NetAdvanced) (*connect.Response[agentv1.ExecuteResponse], error) {
	op := ndnet.AdvancedOp{
		Action: m.GetAction(), ObjectID: m.GetObjectId(), NetworkID: m.GetNetworkId(), Name: m.GetName(),
		VID: int(m.GetVlanId()), ParentIfName: m.GetParentIfname(), Mode: m.GetMode(), AccessIfName: m.GetAccessIfname(),
		Members: m.GetMembers(), SrcMAC: m.GetSrcMac(), DstMAC: m.GetDstMac(), PolicyAction: m.GetPolicyAction(),
		OverlayVNI: m.GetOverlayVni(), ConfirmIfName: m.GetConfirmIfname(), ArmRollback: m.GetArmRollback(),
		BridgeName: m.GetBridgeName(), Policies: decodeNetPolicies(m.GetPolicies()),
	}
	res, err := h.nets().ApplyAdvanced(ctx, op)
	if err != nil {
		return nil, connect.NewError(connect.CodeFailedPrecondition, err)
	}
	return connect.NewResponse(&agentv1.ExecuteResponse{Ok: true, Message: op.Action, ResultJson: mustJSON(res)}), nil
}

func encodeNetPolicies(in []ndnet.PolicyRule) []*agentv1.NetPolicyRule {
	if len(in) == 0 {
		return nil
	}
	out := make([]*agentv1.NetPolicyRule, 0, len(in))
	for _, p := range in {
		out = append(out, &agentv1.NetPolicyRule{
			ObjectId: p.ID, PolicyAction: p.Action, SrcMac: p.SrcMAC, DstMac: p.DstMAC,
		})
	}
	return out
}

func decodeNetPolicies(in []*agentv1.NetPolicyRule) []ndnet.PolicyRule {
	if len(in) == 0 {
		return nil
	}
	out := make([]ndnet.PolicyRule, 0, len(in))
	for _, p := range in {
		if p == nil {
			continue
		}
		out = append(out, ndnet.PolicyRule{
			ID: p.GetObjectId(), Action: p.GetPolicyAction(), SrcMAC: p.GetSrcMac(), DstMAC: p.GetDstMac(),
		})
	}
	return out
}
