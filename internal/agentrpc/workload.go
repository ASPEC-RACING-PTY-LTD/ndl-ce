package agentrpc

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"connectrpc.com/connect"
	agentv1 "github.com/no-dal/ndl-ce/gen/nodal/agent/v1"
	"github.com/no-dal/ndl-ce/internal/lxc"
	"github.com/no-dal/ndl-ce/internal/oci"
	"github.com/no-dal/ndl-ce/internal/qemu"
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
	var ct []lxc.Hint
	var ociHints []oci.Hint
	var vmIDs []string
	for _, hint := range hints {
		if hint.Kind == qemu.KindVM {
			vmIDs = append(vmIDs, hint.WorkloadID)
			continue
		}
		if hint.Kind == oci.KindOCI {
			ociHints = append(ociHints, oci.Hint{
				WorkloadID: hint.WorkloadID, Kind: oci.KindOCI,
				VolumeID: hint.VolumeID, NetworkID: hint.NetworkID,
			})
			continue
		}
		ct = append(ct, hint)
	}
	// Containers, VMs and OCI workloads are observed side by side; each
	// observation shells out to systemctl, so doing them one after another
	// made every workload read wait for all of them in turn.
	var (
		wg    sync.WaitGroup
		obs   lxc.Observation
		vms   = make([]lxc.Observed, len(vmIDs))
		ociWs []lxc.Observed
	)
	wg.Add(2)
	go func() {
		defer wg.Done()
		var err error
		obs, err = h.workloads().Observe(context.Background(), ct)
		if err != nil {
			obs = lxc.Observation{}
		}
	}()
	go func() {
		defer wg.Done()
		if len(ociHints) == 0 {
			return
		}
		ociObs, err := h.oci().Observe(context.Background(), ociHints)
		if err != nil {
			return
		}
		for _, w := range ociObs.Workloads {
			ociWs = append(ociWs, lxc.Observed{
				WorkloadID: w.WorkloadID, Kind: oci.KindOCI, Status: w.Status,
				Reason: w.Reason, UnitActive: w.UnitActive, Warnings: w.Warnings,
				MigrateReady: false, MigrateBlockers: []string{"OCI migrate recreates the container; live is not supported"},
				ObservedAt: w.ObservedAt,
			})
		}
	}()
	sem := make(chan struct{}, vmObserveParallel)
	for i, id := range vmIDs {
		wg.Add(1)
		sem <- struct{}{}
		go func(i int, id string) {
			defer wg.Done()
			defer func() { <-sem }()
			vms[i] = h.observeVM(id)
		}(i, id)
	}
	wg.Wait()
	obs.Workloads = append(obs.Workloads, vms...)
	obs.Workloads = append(obs.Workloads, ociWs...)
	return mustJSON(obs)
}

// vmObserveParallel bounds concurrent VM observations.
const vmObserveParallel = 8

func (h *Handler) observeVM(id string) lxc.Observed {
	obs := h.qemu().Observe(context.Background(), id)
	ready := false
	blockers := []string{"frozen argv is missing"}
	ipv4 := ""
	if applied, err := h.qemu().ReadApplied(id); err == nil {
		ready, blockers = qemu.MigrateReadiness(applied.Argv)
		for _, nic := range applied.Launch.NICs {
			if ip := qemu.IPv4ForMAC(nic.MAC); ip != "" {
				ipv4 = ip
				break
			}
		}
	}
	return lxc.Observed{
		WorkloadID:      obs.WorkloadID,
		Kind:            qemu.KindVM,
		Status:          obs.Status,
		Reason:          obs.Reason,
		UnitActive:      obs.UnitActive,
		IPv4:            ipv4,
		MigrateReady:    ready,
		MigrateBlockers: blockers,
	}
}

func (h *Handler) GetWorkloads(ctx context.Context, req *connect.Request[agentv1.GetWorkloadsRequest]) (*connect.Response[agentv1.GetWorkloadsResponse], error) {
	if err := h.authorize(ctx); err != nil {
		return nil, err
	}
	return connect.NewResponse(&agentv1.GetWorkloadsResponse{
		WorkloadJson: h.observeWorkloads(decodeWorkloadHints(req.Msg.GetWorkloads())),
	}), nil
}

func mustIPJSON(cfg lxc.IPConfig) string {
	b, err := json.Marshal(cfg)
	if err != nil {
		return ""
	}
	return string(b)
}

func ipConfigFromJSON(raw string) lxc.IPConfig {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return lxc.IPConfig{}
	}
	var cfg lxc.IPConfig
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		return lxc.IPConfig{}
	}
	return cfg
}

func specFromCTCreate(m *agentv1.CTCreate) lxc.Spec {
	return lxc.Spec{
		WorkloadID: m.GetWorkloadId(), Name: m.GetName(), ImagePin: m.GetImagePin(),
		CPUs: int(m.GetCpus()), MemoryBytes: m.GetMemoryBytes(), VolumeID: m.GetVolumeId(),
		RootfsPath: m.GetRootfsPath(), NetworkID: m.GetNetworkId(), BridgeName: m.GetBridgeName(),
		MAC: m.GetMac(), Privileged: m.GetPrivileged(), UIDMap: m.GetUidMap(), GIDMap: m.GetGidMap(),
		SkipImage: m.GetSkipImage(), NoStart: m.GetNoStart(), IP: ipConfigFromJSON(m.GetIpConfigJson()),
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
	req := lxc.LifecycleRequest{
		WorkloadID: m.GetWorkloadId(), Action: m.GetAction(), CloneID: m.GetCloneId(),
		CloneVolumeID: m.GetCloneVolumeId(), CloneRootfsPath: m.GetCloneRootfsPath(),
		CloneMAC: m.GetCloneMac(), CloneName: m.GetCloneName(),
		CPUs: int(m.GetCpus()), MemoryBytes: m.GetMemoryBytes(), Name: m.GetName(),
		MAC: m.GetMac(), Extras: append([]string{}, m.GetExtras()...),
	}
	if m.GetIpConfigJson() != "" {
		req.IP = ipConfigFromJSON(m.GetIpConfigJson())
		req.IPSet = true
	}
	if m.GetAutostartSet() {
		on := m.GetAutostart()
		req.Autostart = &on
	}
	if raw := m.GetMountsJson(); raw != "" {
		if err := json.Unmarshal([]byte(raw), &req.Mounts); err != nil {
			return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("mounts are invalid"))
		}
	}
	res, err := h.workloads().Lifecycle(ctx, req)
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
