package httpapi

import (
	"context"
	"net/http"
	"strings"

	"github.com/google/uuid"
	"github.com/no-dal/ndl-ce/internal/appdb"
	"github.com/no-dal/ndl-ce/internal/gpu"
	"github.com/no-dal/ndl-ce/internal/hostos"
	"github.com/no-dal/ndl-ce/internal/inventory"
	"github.com/no-dal/ndl-ce/internal/lxc"
	"github.com/no-dal/ndl-ce/internal/rbac"
)

// GPURPC is the typed agent surface for VFIO and device-node apply.
type GPURPC interface {
	GPUAssign(ctx context.Context, req gpu.AssignRequest) (gpu.AssignResult, error)
}

type gpuUnavailable struct{}

func (gpuUnavailable) GPUAssign(context.Context, gpu.AssignRequest) (gpu.AssignResult, error) {
	return gpu.AssignResult{Status: gpu.StatusUnsupported, Reason: "gpu agent is unavailable"}, nil
}

func AdaptGPU(client any) GPURPC {
	if v, ok := client.(GPURPC); ok {
		return v
	}
	return gpuUnavailable{}
}

func (s *Server) gpus() GPURPC {
	if s.GPU != nil {
		return s.GPU
	}
	return AdaptGPU(s.Agent)
}

func (s *Server) listGPUs(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.NodeRead)
	if err != nil {
		return
	}
	_, invRow, err := s.cachedNode(r, p.User.ClusterID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	parsed, _ := decodeInv(invRow)
	assigns, _ := s.Store.ListGPUAssignments(r.Context(), p.User.ClusterID)
	items := make([]map[string]any, 0, len(parsed.GPUs))
	for _, g := range parsed.GPUs {
		row := map[string]any{
			"id": g.ID, "vendor": g.Vendor, "model": g.Model, "pci": g.PCI,
			"driver": g.Driver, "iommu_group": g.IOMMUGroup, "hint": g.Hint,
		}
		gid, members, err := gpu.GroupMembers(g.ID, parsed)
		if err == nil {
			row["iommu_group"] = gid
			row["group_members"] = encodeGroupMembers(members)
		}
		var claimed []map[string]any
		for _, a := range assigns {
			if strings.EqualFold(a.GPUID, g.ID) {
				claimed = append(claimed, gpuAssignmentJSON(a))
			}
		}
		row["assignments"] = claimed
		items = append(items, row)
	}
	rt := gpu.EvaluateRuntime(s.hostPlatform(parsed), nil)
	writeJSON(w, http.StatusOK, map[string]any{
		"items":           items,
		"iommu":           parsed.IOMMU,
		"runtime":         rt,
		"acs_override":    "refused",
		"default_devices": []string{},
		"note":            "Workloads created without a GPU assignment do not receive /dev/dri.",
	})
}

func (s *Server) gpuRuntime(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.NodeRead)
	if err != nil {
		return
	}
	_, invRow, _ := s.cachedNode(r, p.User.ClusterID)
	parsed, _ := decodeInv(invRow)
	writeJSON(w, http.StatusOK, gpu.EvaluateRuntime(s.hostPlatform(parsed), nil))
}

func (s *Server) installGPURuntime(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.ComputeGPUAssign)
	if err != nil {
		return
	}
	var req struct {
		DryRun bool `json:"dry_run"`
	}
	_ = readJSON(r, &req)
	if !req.DryRun && strings.TrimSpace(r.Header.Get(confirmHeader)) != "install-gpu-runtime" {
		writeErr(w, http.StatusUnprocessableEntity, "install requires dry_run or X-Nodal-Confirm: install-gpu-runtime")
		return
	}
	res, err := s.gpus().GPUAssign(r.Context(), gpu.AssignRequest{Action: "runtime-install", DryRun: req.DryRun})
	if err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	s.audit(r, p.User.ClusterID, p.User.ID, "gpu.runtime.install", "ok", "")
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) assignGPU(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.ComputeGPUAssign)
	if err != nil {
		return
	}
	var req struct {
		GPUID      string `json:"gpu_id"`
		WorkloadID string `json:"workload_id"`
		Mode       string `json:"mode"`
		Exclusive  bool   `json:"exclusive"`
		ACS        bool   `json:"acs_override"`
	}
	if err := readJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request")
		return
	}
	gpuID, err := gpu.ParseGPUID(req.GPUID)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	mode, err := gpu.ParseMode(req.Mode)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := gpu.RefuseACSOverride(req.ACS); err != nil {
		writeErr(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	if strings.TrimSpace(req.WorkloadID) == "" {
		writeErr(w, http.StatusBadRequest, "workload_id is required")
		return
	}
	wl, err := s.Store.GetWorkload(r.Context(), p.User.ClusterID, req.WorkloadID)
	if err != nil || wl == nil {
		writeErr(w, http.StatusNotFound, "workload not found")
		return
	}
	exclusive := gpu.ExclusiveForMode(mode, req.Exclusive)
	_, invRow, _ := s.cachedNode(r, p.User.ClusterID)
	parsed, ok := decodeInv(invRow)
	if !ok {
		writeErr(w, http.StatusUnprocessableEntity, "node inventory is unavailable")
		return
	}
	groupID, members, err := gpu.GroupMembers(gpuID, parsed)
	if err != nil {
		writeErr(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	if mode == gpu.ModeVFIO {
		if wl.Kind == lxc.KindSystemContainer {
			writeErr(w, http.StatusUnprocessableEntity, "VFIO assignment is for VMs")
			return
		}
		if parsed.IOMMU.Status != inventory.StatusAvailable {
			writeErr(w, http.StatusUnprocessableEntity, "IOMMU is unavailable; VFIO cannot be assigned")
			return
		}
		if wl.Status == lxc.StatusRunning {
			writeErr(w, http.StatusUnprocessableEntity, "stop the VM before VFIO bind")
			return
		}
		snaps, _ := s.Store.ListSnapshots(r.Context(), p.User.ClusterID, wl.ID)
		if len(snaps) == 0 {
			writeErr(w, http.StatusUnprocessableEntity, "snapshot the VM before VFIO bind")
			return
		}
	} else if wl.Kind != lxc.KindSystemContainer {
		writeErr(w, http.StatusUnprocessableEntity, "render, compute, and encode assign Linux device nodes to system containers")
		return
	}
	var pci []string
	for _, m := range members {
		pci = append(pci, m.Address)
	}
	nodes := deviceNodesForMode(mode, gpuID)
	a := appdb.GPUAssignment{
		ID: uuid.NewString(), ClusterID: p.User.ClusterID, GPUID: gpuID, WorkloadID: wl.ID,
		Mode: mode, Exclusive: exclusive, IOMMUGroup: groupID, PCIDevices: pci, DeviceNodes: nodes,
		Status: gpu.StatusAssigned,
	}
	if err := s.Store.CreateGPUAssignment(r.Context(), a); err != nil {
		writeErr(w, http.StatusConflict, err.Error())
		return
	}
	res, err := s.gpus().GPUAssign(r.Context(), gpu.AssignRequest{
		Action: "assign", GPUID: gpuID, WorkloadID: wl.ID, Mode: mode, Exclusive: exclusive,
		PCIDevices: pci, DeviceNodes: nodes,
	})
	if err != nil {
		_ = s.Store.DeleteGPUAssignment(r.Context(), p.User.ClusterID, a.ID)
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	if res.Status != "" {
		a.Status = res.Status
		a.Reason = res.Reason
	}
	s.audit(r, p.User.ClusterID, p.User.ID, "gpu.assign", "ok", a.ID)
	writeJSON(w, http.StatusCreated, gpuAssignmentJSON(a))
}

func (s *Server) unassignGPU(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.ComputeGPUAssign)
	if err != nil {
		return
	}
	var req struct {
		ID string `json:"id"`
	}
	if err := readJSON(r, &req); err != nil || req.ID == "" {
		writeErr(w, http.StatusBadRequest, "id is required")
		return
	}
	a, err := s.Store.GetGPUAssignment(r.Context(), p.User.ClusterID, req.ID)
	if err != nil || a == nil {
		writeErr(w, http.StatusNotFound, "assignment not found")
		return
	}
	_, _ = s.gpus().GPUAssign(r.Context(), gpu.AssignRequest{
		Action: "unassign", GPUID: a.GPUID, WorkloadID: a.WorkloadID, Mode: a.Mode,
		PCIDevices: a.PCIDevices, DeviceNodes: a.DeviceNodes,
	})
	if err := s.Store.DeleteGPUAssignment(r.Context(), p.User.ClusterID, a.ID); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.audit(r, p.User.ClusterID, p.User.ID, "gpu.unassign", "ok", a.ID)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) workloadGPUs(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.ComputeRead)
	if err != nil {
		return
	}
	items, _ := s.Store.ListGPUAssignments(r.Context(), p.User.ClusterID)
	out := make([]map[string]any, 0)
	for _, a := range items {
		if a.WorkloadID == r.PathValue("id") {
			out = append(out, gpuAssignmentJSON(a))
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": out})
}

func (s *Server) hostPlatform(inv inventory.Inventory) hostos.Platform {
	return hostos.Platform{
		ID: inv.Host.ID, VersionID: inv.Host.VersionID, Family: inv.Host.Family,
		Architecture: inv.Host.Architecture, PrettyName: inv.Host.PrettyName,
	}
}

func encodeGroupMembers(members []inventory.PCIDevice) []map[string]any {
	out := make([]map[string]any, 0, len(members))
	for _, m := range members {
		out = append(out, map[string]any{
			"pci": m.Address, "class": m.Class, "kind": gpu.MemberKind(m.Class), "driver": m.Driver,
		})
	}
	return out
}

func gpuAssignmentJSON(a appdb.GPUAssignment) map[string]any {
	return map[string]any{
		"id": a.ID, "gpu_id": a.GPUID, "workload_id": a.WorkloadID, "mode": a.Mode,
		"exclusive": a.Exclusive, "iommu_group": a.IOMMUGroup, "pci_devices": a.PCIDevices,
		"device_nodes": a.DeviceNodes, "status": a.Status, "reason": a.Reason,
	}
}

func deviceNodesForMode(mode, gpuID string) []string {
	switch mode {
	case gpu.ModeRender, gpu.ModeEncode:
		return []string{"/dev/dri/renderD128"}
	case gpu.ModeCompute:
		return []string{"/dev/nvidia0", "/dev/nvidiactl", "/dev/nvidia-uvm"}
	default:
		return nil
	}
}

func (s *Server) gpuDeviceNodes(ctx context.Context, clusterID, workloadID string) []string {
	items, _ := s.Store.ListGPUAssignments(ctx, clusterID)
	var nodes []string
	seen := map[string]bool{}
	for _, a := range items {
		if a.WorkloadID != workloadID {
			continue
		}
		for _, n := range a.DeviceNodes {
			if n == "" || seen[n] {
				continue
			}
			seen[n] = true
			nodes = append(nodes, n)
		}
	}
	return nodes
}
