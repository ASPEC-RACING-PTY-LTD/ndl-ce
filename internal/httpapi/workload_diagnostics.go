package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"

	"github.com/no-dal/ndl-ce/internal/appdb"
	"github.com/no-dal/ndl-ce/internal/gpu"
	"github.com/no-dal/ndl-ce/internal/inventory"
	"github.com/no-dal/ndl-ce/internal/lxc"
	"github.com/no-dal/ndl-ce/internal/rbac"
)

// Reapply outcomes. Applied means the running guest (or the next start of a
// stopped one) receives every expected node; restart_required means the
// config is written but the running guest has not received the devices.
const (
	gpuReapplyApplied         = "applied"
	gpuReapplyRestartRequired = "restart_required"
	gpuReapplyFailed          = "failed"
)

type gpuDiagAssignment struct {
	ID            string   `json:"id"`
	GPUID         string   `json:"gpu_id"`
	Mode          string   `json:"mode"`
	SavedNodes    []string `json:"saved_nodes"`
	ExpectedNodes []string `json:"expected_nodes"`
	Current       bool     `json:"current"`
	Error         string   `json:"error,omitempty"`
}

// workloadGPUPlan recomputes each device-node claim of one workload from the
// saved assignment (GPU and mode) against the current node inventory.
func (s *Server) workloadGPUPlan(ctx context.Context, r *http.Request, clusterID, workloadID string) ([]appdb.GPUAssignment, []gpuDiagAssignment, error) {
	all, err := s.Store.ListGPUAssignments(ctx, clusterID)
	if err != nil {
		return nil, nil, errInternal("could not list GPU assignments")
	}
	var parsed inventory.Inventory
	invOK := false
	if _, invRow, _ := s.cachedNode(r, clusterID); invRow != nil {
		parsed, invOK = decodeInv(invRow)
	}
	var rows []appdb.GPUAssignment
	plan := []gpuDiagAssignment{}
	for _, a := range all {
		if a.WorkloadID != workloadID || a.Mode == gpu.ModeVFIO {
			continue
		}
		item := gpuDiagAssignment{
			ID: a.ID, GPUID: a.GPUID, Mode: a.Mode,
			SavedNodes: append([]string{}, a.DeviceNodes...), ExpectedNodes: []string{},
		}
		switch rec, ok := gpuRecord(a.GPUID, parsed); {
		case !invOK:
			item.Error = "node inventory is unavailable"
		case !ok:
			item.Error = "gpu is not present on this node; if it was removed, unassign it"
		default:
			nodes, nerr := deviceNodesForMode(a.Mode, rec)
			if nerr != nil {
				item.Error = nerr.Error()
			} else {
				item.ExpectedNodes = nodes
				item.Current = slices.Equal(nodes, a.DeviceNodes)
			}
		}
		rows = append(rows, a)
		plan = append(plan, item)
	}
	return rows, plan, nil
}

func (s *Server) diagnoseWorkloadGPU(ctx context.Context, workloadID string) (json.RawMessage, string) {
	res, err := s.gpus().GPUAssign(ctx, gpu.AssignRequest{Action: gpu.ActionDiagnose, WorkloadID: workloadID})
	if gpuApplyFailed(res, err) {
		return nil, gpuApplyError(res, err)
	}
	if len(res.Diagnosis) == 0 {
		return nil, "gpu diagnosis is unavailable"
	}
	return res.Diagnosis, ""
}

func (s *Server) workloadGPUDiagnostics(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.ComputeRead)
	if err != nil {
		return
	}
	wl, err := s.Store.GetWorkload(r.Context(), p.User.ClusterID, r.PathValue("id"))
	if err != nil || wl == nil {
		writeErr(w, http.StatusNotFound, "workload not found")
		return
	}
	if wl.Kind != lxc.KindSystemContainer {
		writeJSON(w, http.StatusOK, map[string]any{
			"workload_id": wl.ID, "supported": false,
			"reason": "GPU diagnostics compare LXC device config and apply to system containers",
		})
		return
	}
	_, plan, err := s.workloadGPUPlan(r.Context(), r, p.User.ClusterID, wl.ID)
	if err != nil {
		writeErr(w, statusFor(err), err.Error())
		return
	}
	body := map[string]any{"workload_id": wl.ID, "supported": true, "assignments": plan}
	if diag, diagErr := s.diagnoseWorkloadGPU(r.Context(), wl.ID); diagErr != "" {
		body["diagnosis_error"] = diagErr
	} else {
		body["diagnosis"] = diag
		var parsed lxc.GPUDiagnosis
		if json.Unmarshal(diag, &parsed) == nil {
			body["applied_matches_saved"] = sameNodeSet(savedPlanNodes(plan), parsed.Saved)
		}
	}
	writeJSON(w, http.StatusOK, body)
}

// savedPlanNodes is every node the workload's saved claims hold.
func savedPlanNodes(plan []gpuDiagAssignment) []string {
	var out []string
	for _, item := range plan {
		out = append(out, item.SavedNodes...)
	}
	return out
}

func sameNodeSet(a, b []string) bool {
	set := func(in []string) map[string]bool {
		m := map[string]bool{}
		for _, n := range in {
			if n != "" {
				m[n] = true
			}
		}
		return m
	}
	sa, sb := set(a), set(b)
	if len(sa) != len(sb) {
		return false
	}
	for n := range sa {
		if !sb[n] {
			return false
		}
	}
	return true
}

// reapplyWorkloadGPU regenerates the guest's GPU devices from the saved
// assignments. It never restarts the workload; a running guest that has not
// received the devices is reported as restart_required.
func (s *Server) reapplyWorkloadGPU(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.ComputeGPUAssign)
	if err != nil {
		return
	}
	wl, err := s.Store.GetWorkload(r.Context(), p.User.ClusterID, r.PathValue("id"))
	if err != nil || wl == nil {
		writeErr(w, http.StatusNotFound, "workload not found")
		return
	}
	if wl.Kind != lxc.KindSystemContainer {
		writeErr(w, http.StatusUnprocessableEntity, "GPU reapply is for system containers")
		return
	}
	rows, plan, err := s.workloadGPUPlan(r.Context(), r, p.User.ClusterID, wl.ID)
	if err != nil {
		writeErr(w, statusFor(err), err.Error())
		return
	}
	var nodes []string
	seen := map[string]bool{}
	for _, item := range plan {
		if item.Error != "" {
			writeErr(w, http.StatusUnprocessableEntity, item.GPUID+": "+item.Error)
			return
		}
		for _, n := range item.ExpectedNodes {
			if !gpu.AllowDeviceNode(n) {
				writeErr(w, http.StatusUnprocessableEntity, "device node is not allowlisted")
				return
			}
			if !seen[n] {
				seen[n] = true
				nodes = append(nodes, n)
			}
		}
	}
	restore := func() {
		for _, a := range rows {
			_ = s.Store.UpdateGPUAssignmentDeviceNodes(r.Context(), p.User.ClusterID, a.ID, a.DeviceNodes)
		}
	}
	for i, a := range rows {
		if plan[i].Current {
			continue
		}
		if err := s.Store.UpdateGPUAssignmentDeviceNodes(r.Context(), p.User.ClusterID, a.ID, plan[i].ExpectedNodes); err != nil {
			restore()
			writeErr(w, http.StatusInternalServerError, "could not record GPU device nodes")
			return
		}
	}
	res, err := s.gpus().GPUAssign(r.Context(), gpu.AssignRequest{
		Action: gpu.ActionReapply, WorkloadID: wl.ID, DeviceNodes: nodes,
	})
	if gpuApplyFailed(res, err) {
		restore()
		s.audit(r, p.User.ClusterID, p.User.ID, "gpu.reapply", gpuReapplyFailed, wl.ID)
		writeJSON(w, http.StatusBadGateway, map[string]any{
			"status": gpuReapplyFailed, "error": gpuApplyError(res, err), "assignments": plan,
		})
		return
	}
	var diag lxc.GPUDiagnosis
	if err := json.Unmarshal(res.Diagnosis, &diag); err != nil {
		s.audit(r, p.User.ClusterID, p.User.ID, "gpu.reapply", gpuReapplyFailed, wl.ID)
		writeJSON(w, http.StatusBadGateway, map[string]any{
			"status": gpuReapplyFailed, "error": "the agent did not report the applied devices", "assignments": plan,
		})
		return
	}
	status, message := reapplyOutcome(diag)
	s.audit(r, p.User.ClusterID, p.User.ID, "gpu.reapply", status, wl.ID)
	writeJSON(w, http.StatusOK, map[string]any{
		"status": status, "message": message, "assignments": plan, "diagnosis": res.Diagnosis,
	})
}

func reapplyOutcome(d lxc.GPUDiagnosis) (string, string) {
	if !d.ConfigCurrent {
		return gpuReapplyFailed, "the LXC config does not match the saved assignment after reapply"
	}
	for _, n := range d.Nodes {
		if n.HostRule == "" && !n.Optional {
			return gpuReapplyFailed, n.Path + " is missing on the host; the GPU may be removed or its driver not loaded"
		}
		if n.HostRule != "" && (!n.Mounted || !n.Allowed) {
			return gpuReapplyFailed, n.Path + " is missing from the regenerated LXC config"
		}
	}
	if !d.Running {
		return gpuReapplyApplied, "Config regenerated. The devices load when the container starts."
	}
	if d.RestartRequired {
		return gpuReapplyRestartRequired, "Config regenerated. The running container has not received every GPU device; restart it to apply."
	}
	for _, n := range d.Nodes {
		if n.HostRule != "" && n.Guest != lxc.GuestNodePresent {
			return gpuReapplyFailed, "Config regenerated, but the devices inside the running container could not be verified."
		}
	}
	return gpuReapplyApplied, "The running container has every GPU device."
}
