package agentrpc

import (
	"context"
	"encoding/json"

	"connectrpc.com/connect"
	agentv1 "github.com/no-dal/ndl-ce/gen/nodal/agent/v1"
	"github.com/no-dal/ndl-ce/internal/lxc"
)

func (h *Handler) workloads() *lxc.Engine {
	if h.Workloads != nil {
		return h.Workloads
	}
	return &lxc.Engine{}
}

func decodeWorkloadHints(in []*agentv1.WorkloadHint) []lxc.Hint {
	out := make([]lxc.Hint, 0, len(in))
	for _, h := range in {
		out = append(out, lxc.Hint{
			WorkloadID: h.GetWorkloadId(), Kind: h.GetKind(),
			VolumeID: h.GetVolumeId(), NetworkID: h.GetNetworkId(),
		})
	}
	return out
}

func (h *Handler) observeWorkloads(hints []lxc.Hint) []byte {
	obs, err := h.workloads().Observe(context.Background(), hints)
	if err != nil {
		return mustJSON(lxc.Observation{})
	}
	return mustJSON(obs)
}

// GetWorkloads observes known system containers. Missing is unavailable.
func (h *Handler) GetWorkloads(ctx context.Context, req *connect.Request[agentv1.GetWorkloadsRequest]) (*connect.Response[agentv1.GetWorkloadsResponse], error) {
	if err := h.authorize(ctx); err != nil {
		return nil, err
	}
	return connect.NewResponse(&agentv1.GetWorkloadsResponse{
		WorkloadJson: h.observeWorkloads(decodeWorkloadHints(req.Msg.GetWorkloads())),
	}), nil
}

func specFromCTCreate(m *agentv1.CTCreate) lxc.Spec {
	return lxc.Spec{
		WorkloadID: m.GetWorkloadId(), Name: m.GetName(), ImagePin: m.GetImagePin(),
		CPUs: int(m.GetCpus()), MemoryBytes: m.GetMemoryBytes(), VolumeID: m.GetVolumeId(),
		RootfsPath: m.GetRootfsPath(), NetworkID: m.GetNetworkId(), BridgeName: m.GetBridgeName(),
		MAC: m.GetMac(), Privileged: m.GetPrivileged(), UIDMap: m.GetUidMap(), GIDMap: m.GetGidMap(),
	}
}

func (h *Handler) execCTCreate(ctx context.Context, m *agentv1.CTCreate) (*connect.Response[agentv1.ExecuteResponse], error) {
	res, err := h.workloads().Create(ctx, specFromCTCreate(m))
	if err != nil {
		return nil, connect.NewError(connect.CodeFailedPrecondition, err)
	}
	return connect.NewResponse(&agentv1.ExecuteResponse{Ok: true, Message: "created", ResultJson: mustJSON(res)}), nil
}

func (h *Handler) execCTLifecycle(ctx context.Context, m *agentv1.CTLifecycle) (*connect.Response[agentv1.ExecuteResponse], error) {
	res, err := h.workloads().Lifecycle(ctx, lxc.LifecycleRequest{
		WorkloadID: m.GetWorkloadId(), Action: m.GetAction(), CloneID: m.GetCloneId(),
		CloneVolumeID: m.GetCloneVolumeId(), CloneRootfsPath: m.GetCloneRootfsPath(),
		CloneMAC: m.GetCloneMac(), CloneName: m.GetCloneName(),
	})
	if err != nil {
		return nil, connect.NewError(connect.CodeFailedPrecondition, err)
	}
	return connect.NewResponse(&agentv1.ExecuteResponse{Ok: true, Message: m.GetAction(), ResultJson: mustJSON(res)}), nil
}

func encodeWorkloadHints(hints []lxc.Hint) []*agentv1.WorkloadHint {
	out := make([]*agentv1.WorkloadHint, 0, len(hints))
	for _, h := range hints {
		out = append(out, &agentv1.WorkloadHint{
			WorkloadId: h.WorkloadID, Kind: h.Kind, VolumeId: h.VolumeID, NetworkId: h.NetworkID,
		})
	}
	return out
}

func decodeWorkloads(raw []byte) (lxc.Observation, error) {
	var obs lxc.Observation
	if len(raw) == 0 {
		return obs, nil
	}
	if err := json.Unmarshal(raw, &obs); err != nil {
		return obs, err
	}
	return obs, nil
}
