package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/no-dal/ndl-ce/internal/appdb"
	"github.com/no-dal/ndl-ce/internal/gpu"
	"github.com/no-dal/ndl-ce/internal/hostos"
	"github.com/no-dal/ndl-ce/internal/inventory"
	"github.com/no-dal/ndl-ce/internal/lxc"
	"github.com/no-dal/ndl-ce/internal/oci"
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
	// Claims on a GPU that inventory no longer reports (removed card or
	// unloaded driver) stay listed so they can be unassigned.
	orphaned := make([]map[string]any, 0)
	for _, a := range assigns {
		if _, ok := gpuRecord(a.GPUID, parsed); !ok {
			orphaned = append(orphaned, gpuAssignmentJSON(a))
		}
	}
	rt := gpu.EvaluateRuntime(s.hostPlatform(parsed), nil)
	writeJSON(w, http.StatusOK, map[string]any{
		"items":                items,
		"orphaned_assignments": orphaned,
		"iommu":                parsed.IOMMU,
		"runtime":              rt,
		"acs_override":         "refused",
		"default_devices":      []string{},
		"note":                 "Workloads created without a GPU assignment do not receive /dev/dri.",
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
		if wl.Kind == lxc.KindSystemContainer || wl.Kind == oci.KindOCI {
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
	} else if wl.Kind != lxc.KindSystemContainer && wl.Kind != oci.KindOCI {
		writeErr(w, http.StatusUnprocessableEntity, "render, compute, and encode assign Linux device nodes to system containers and OCI workloads")
		return
	}
	var pci []string
	for _, m := range members {
		pci = append(pci, m.Address)
	}
	rec, ok := gpuRecord(gpuID, parsed)
	if !ok {
		writeErr(w, http.StatusUnprocessableEntity, "gpu is not present on this node")
		return
	}
	nodes, err := deviceNodesForMode(mode, rec)
	if err != nil {
		writeErr(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	a := appdb.GPUAssignment{
		ID: uuid.NewString(), ClusterID: p.User.ClusterID, GPUID: gpuID, WorkloadID: wl.ID,
		Mode: mode, Exclusive: exclusive, IOMMUGroup: groupID, PCIDevices: pci, DeviceNodes: nodes,
		Status: gpu.StatusAssigned,
	}
	if err := s.Store.CreateGPUAssignment(r.Context(), a); err != nil {
		writeErr(w, http.StatusConflict, err.Error())
		return
	}
	stored, err := s.Store.GetGPUAssignment(r.Context(), p.User.ClusterID, a.ID)
	if err != nil || stored == nil {
		writeErr(w, http.StatusInternalServerError, "could not record GPU assign")
		return
	}
	a = *stored
	origSpec := wl.SpecJSON
	if mode == gpu.ModeVFIO {
		hosts := vfioHostsForWorkload(r.Context(), s, p.User.ClusterID, wl.ID, parsed)
		if err := persistVFIOHosts(r.Context(), s, *wl, hosts); err != nil {
			_ = s.Store.DeleteGPUAssignment(r.Context(), p.User.ClusterID, a.ID)
			writeErr(w, http.StatusInternalServerError, "could not record VFIO spec")
			return
		}
	}
	applyNodes := nodes
	if mode != gpu.ModeVFIO {
		// The agent replaces the guest's whole device list, so send every
		// claim this workload holds, not only the new one.
		applyNodes = s.gpuDeviceNodes(r.Context(), p.User.ClusterID, wl.ID)
	}
	res, err := s.gpus().GPUAssign(r.Context(), gpu.AssignRequest{
		Action: "assign", GPUID: gpuID, WorkloadID: wl.ID, Mode: mode, Exclusive: exclusive,
		PCIDevices: pci, DeviceNodes: applyNodes,
	})
	if gpuApplyFailed(res, err) || (res.Status != "" && res.Status != gpu.StatusAssigned) {
		_ = s.Store.DeleteGPUAssignment(r.Context(), p.User.ClusterID, a.ID)
		if mode == gpu.ModeVFIO {
			_ = s.Store.UpdateWorkloadSpec(r.Context(), appdb.Workload{
				ID: wl.ID, SpecJSON: origSpec, Firmware: wl.Firmware, CPUs: wl.CPUs, MemoryBytes: wl.MemoryBytes,
			})
		}
		writeErr(w, http.StatusBadGateway, gpuApplyError(res, err))
		return
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
	body := map[string]any{"ok": true}
	if a.Mode == gpu.ModeVFIO {
		err = s.releaseGPUAssignment(r.Context(), p.User.ClusterID, *a)
	} else {
		var rel deviceRelease
		rel, err = s.releaseDeviceClaim(r.Context(), p.User.ClusterID, *a)
		body["device_nodes"] = rel.Remaining
		body["restart_required"] = rel.RestartRequired
		if rel.RestartRequired {
			body["message"] = "The config no longer grants this GPU. The running container keeps its device nodes until it restarts."
		}
	}
	if err != nil {
		s.audit(r, p.User.ClusterID, p.User.ID, "gpu.unassign", "failed", a.ID)
		writeErr(w, statusFor(err), err.Error())
		return
	}
	s.audit(r, p.User.ClusterID, p.User.ID, "gpu.unassign", "ok", a.ID)
	writeJSON(w, http.StatusOK, body)
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

func assignmentHosts(a appdb.GPUAssignment) []string {
	if len(a.PCIDevices) > 0 {
		return append([]string{}, a.PCIDevices...)
	}
	if strings.TrimSpace(a.GPUID) != "" {
		return []string{a.GPUID}
	}
	return nil
}

func (s *Server) clusterInventory(ctx context.Context, clusterID string) inventory.Inventory {
	node, err := s.Store.GetNode(ctx, clusterID)
	if err != nil || node == nil {
		return inventory.Inventory{}
	}
	invRow, _ := s.Store.GetInventory(ctx, node.ID)
	parsed, _ := decodeInv(invRow)
	return parsed
}

func (s *Server) releaseGPUAssignment(ctx context.Context, clusterID string, a appdb.GPUAssignment) error {
	if a.Mode != gpu.ModeVFIO {
		_, err := s.releaseDeviceClaim(ctx, clusterID, a)
		return err
	}
	var origSpec []byte
	var vfioRow *appdb.Workload
	hosts := assignmentHosts(a)
	if a.Mode == gpu.ModeVFIO {
		if wl, _ := s.Store.GetWorkload(ctx, clusterID, a.WorkloadID); wl != nil {
			vfioRow = wl
			origSpec = append([]byte(nil), wl.SpecJSON...)
			remain := vfioHostsForWorkload(ctx, s, clusterID, wl.ID, s.clusterInventory(ctx, clusterID), a.ID)
			if err := persistVFIOHosts(ctx, s, *wl, remain); err != nil {
				return errInternal("could not record VFIO spec")
			}
		}
	}
	res, applyErr := s.gpus().GPUAssign(ctx, gpu.AssignRequest{
		Action: "unassign", GPUID: a.GPUID, WorkloadID: a.WorkloadID, Mode: a.Mode,
		PCIDevices: hosts, DeviceNodes: a.DeviceNodes,
	})
	if gpuApplyFailed(res, applyErr) {
		if res.Status == gpu.StatusUnsupported && a.Mode == gpu.ModeVFIO && s.VM != nil {
			remain := hosts
			if vfioRow != nil {
				remain = vfioHostsForWorkload(ctx, s, clusterID, vfioRow.ID, s.clusterInventory(ctx, clusterID), a.ID)
			}
			if err := s.VM.ApplyVFIO(ctx, a.WorkloadID, remain); err != nil {
				if vfioRow != nil {
					_ = s.Store.UpdateWorkloadSpec(ctx, appdb.Workload{
						ID: vfioRow.ID, SpecJSON: origSpec, Firmware: vfioRow.Firmware, CPUs: vfioRow.CPUs, MemoryBytes: vfioRow.MemoryBytes,
					})
				}
				return err
			}
		} else {
			if vfioRow != nil {
				_ = s.Store.UpdateWorkloadSpec(ctx, appdb.Workload{
					ID: vfioRow.ID, SpecJSON: origSpec, Firmware: vfioRow.Firmware, CPUs: vfioRow.CPUs, MemoryBytes: vfioRow.MemoryBytes,
				})
			}
			return errUnavailable(gpuApplyError(res, applyErr))
		}
	}
	if err := s.Store.DeleteGPUAssignment(ctx, clusterID, a.ID); err != nil {
		return errInternal("could not record GPU unassign")
	}
	got, gerr := s.Store.GetGPUAssignment(ctx, clusterID, a.ID)
	if gerr != nil || got != nil {
		return errInternal("could not record GPU unassign")
	}
	return nil
}

// deviceRelease reports what a released device claim leaves behind.
type deviceRelease struct {
	// Remaining is the complete device list the workload keeps.
	Remaining []string
	// RestartRequired means the running guest keeps the released nodes until
	// it restarts; the config no longer grants them.
	RestartRequired bool
}

// releaseDeviceClaim drops one render, compute, or encode claim. The agent
// replaces the guest's whole device list, so it receives every node the
// remaining claims hold, rebuilt from their saved GPU and mode. Stored rows,
// the applied config, and the claim are rolled back together on failure so
// they cannot drift apart.
func (s *Server) releaseDeviceClaim(ctx context.Context, clusterID string, a appdb.GPUAssignment) (deviceRelease, error) {
	var out deviceRelease
	all, err := s.Store.ListGPUAssignments(ctx, clusterID)
	if err != nil {
		return out, errInternal("could not list GPU assignments")
	}
	inv := s.clusterInventory(ctx, clusterID)
	var previous []string
	var stale []appdb.GPUAssignment
	var restoreNodes [][]string
	seenPrev, seenNext := map[string]bool{}, map[string]bool{}
	desired := []string{}
	for _, row := range all {
		if row.WorkloadID != a.WorkloadID || row.Mode == gpu.ModeVFIO {
			continue
		}
		for _, n := range row.DeviceNodes {
			if n != "" && !seenPrev[n] {
				seenPrev[n] = true
				previous = append(previous, n)
			}
		}
		if row.ID == a.ID {
			continue
		}
		keep := row.DeviceNodes
		if rec, ok := gpuRecord(row.GPUID, inv); ok {
			if nodes, nerr := deviceNodesForMode(row.Mode, rec); nerr == nil && !slices.Equal(nodes, row.DeviceNodes) {
				stale = append(stale, row)
				restoreNodes = append(restoreNodes, row.DeviceNodes)
				keep = nodes
				stale[len(stale)-1].DeviceNodes = nodes
			}
		}
		for _, n := range keep {
			if n == "" || seenNext[n] {
				continue
			}
			if !gpu.AllowDeviceNode(n) {
				return out, errUnprocessable("device node is not allowlisted")
			}
			seenNext[n] = true
			desired = append(desired, n)
		}
	}
	restoreRows := func(upTo int) {
		for i := 0; i < upTo; i++ {
			_ = s.Store.UpdateGPUAssignmentDeviceNodes(ctx, clusterID, stale[i].ID, restoreNodes[i])
		}
	}
	for i, row := range stale {
		if err := s.Store.UpdateGPUAssignmentDeviceNodes(ctx, clusterID, row.ID, row.DeviceNodes); err != nil {
			restoreRows(i)
			return out, errInternal("could not record GPU device nodes")
		}
	}
	rollback := func() {
		restoreRows(len(stale))
		if previous == nil {
			previous = []string{}
		}
		_, _ = s.gpus().GPUAssign(ctx, gpu.AssignRequest{
			Action: gpu.ActionRelease, GPUID: a.GPUID, WorkloadID: a.WorkloadID, Mode: a.Mode, DeviceNodes: previous,
		})
	}
	res, applyErr := s.gpus().GPUAssign(ctx, gpu.AssignRequest{
		Action: gpu.ActionRelease, GPUID: a.GPUID, WorkloadID: a.WorkloadID, Mode: a.Mode, DeviceNodes: desired,
	})
	if gpuApplyFailed(res, applyErr) || (res.Status != gpu.StatusReleased && res.Status != gpu.StatusAssigned) {
		restoreRows(len(stale))
		if applyErr == nil && res.Status != gpu.StatusFailed && res.Status != gpu.StatusUnsupported && res.Reason == "" {
			res.Reason = "gpu agent did not confirm the release"
		}
		return out, errUnavailable(gpuApplyError(res, applyErr))
	}
	if len(res.DeviceNodes) != len(desired) || (len(desired) > 0 && !slices.Equal(res.DeviceNodes, desired)) {
		rollback()
		return out, errUnavailable("gpu agent applied a different device list; the previous GPU config was restored")
	}
	if len(res.Diagnosis) > 0 {
		var diag lxc.GPUDiagnosis
		if err := json.Unmarshal(res.Diagnosis, &diag); err != nil || !diag.ConfigCurrent {
			rollback()
			return out, errUnavailable("the LXC config did not match the remaining GPU assignments; the previous GPU config was restored")
		}
		out.RestartRequired = diag.Running && len(previous) > len(desired)
	}
	// A delete error is ignored when the row is gone: the applied config then
	// already matches the store.
	_ = s.Store.DeleteGPUAssignment(ctx, clusterID, a.ID)
	got, gerr := s.Store.GetGPUAssignment(ctx, clusterID, a.ID)
	if gerr != nil {
		return out, errInternal("could not confirm GPU unassign; open workload Diagnostics to compare the saved and applied devices")
	}
	if got != nil {
		rollback()
		return out, errInternal("could not record GPU unassign; the previous GPU config was restored")
	}
	out.Remaining = desired
	return out, nil
}

func (s *Server) releaseWorkloadClaims(ctx context.Context, clusterID, workloadID string) error {
	assigns, err := s.Store.ListGPUAssignments(ctx, clusterID)
	if err != nil {
		return err
	}
	for _, a := range assigns {
		if a.WorkloadID != workloadID {
			continue
		}
		if err := s.releaseGPUAssignment(ctx, clusterID, a); err != nil {
			return err
		}
	}
	usbs, err := s.Store.ListUSBAttachments(ctx, clusterID, workloadID)
	if err != nil {
		return err
	}
	for _, u := range usbs {
		if err := s.Store.DeleteUSBAttachment(ctx, clusterID, u.ID); err != nil {
			return err
		}
	}
	if err := s.releasePhysicalAssignments(ctx, clusterID, workloadID); err != nil {
		return err
	}
	return nil
}

func gpuApplyFailed(res gpu.AssignResult, err error) bool {
	if err != nil {
		return true
	}
	return res.Status == gpu.StatusFailed || res.Status == gpu.StatusUnsupported
}

func gpuApplyError(res gpu.AssignResult, err error) string {
	if err != nil {
		return err.Error()
	}
	if strings.TrimSpace(res.Reason) != "" {
		return res.Reason
	}
	if res.Status == gpu.StatusUnsupported {
		return "gpu agent is unavailable"
	}
	return "gpu apply failed"
}

func gpuAssignmentJSON(a appdb.GPUAssignment) map[string]any {
	return map[string]any{
		"id": a.ID, "gpu_id": a.GPUID, "workload_id": a.WorkloadID, "mode": a.Mode,
		"exclusive": a.Exclusive, "iommu_group": a.IOMMUGroup, "pci_devices": a.PCIDevices,
		"device_nodes": a.DeviceNodes, "status": a.Status, "reason": a.Reason,
	}
}

func gpuRecord(gpuID string, inv inventory.Inventory) (inventory.GPU, bool) {
	for _, g := range inv.GPUs {
		if strings.EqualFold(g.ID, gpuID) || strings.EqualFold(g.PCI, gpuID) {
			return g, true
		}
	}
	return inventory.GPU{}, false
}

func locatorsFromGPU(g inventory.GPU) []string {
	var out []string
	seen := map[string]bool{}
	for _, part := range strings.FieldsFunc(g.Hint, func(r rune) bool {
		return r == ',' || r == ';' || r == ' ' || r == '\n' || r == '\t'
	}) {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if !gpu.AllowDeviceNode(part) {
			continue
		}
		if seen[part] {
			continue
		}
		seen[part] = true
		out = append(out, part)
	}
	return out
}

// nvidiaSharedNodes are the proprietary driver's control nodes. NVENC and
// NVDEC need nvidiactl plus the per-GPU node, CUDA needs nvidia-uvm, and
// nvidia-modeset is used by the driver's display and encode stack. They carry
// no per-GPU access, so sharing them keeps other GPUs isolated. nvidia-caps
// is left out: it only gates MIG, and caps/nvidia-cap1 grants MIG
// configuration of every GPU on the host.
var nvidiaSharedNodes = []string{"/dev/nvidiactl", "/dev/nvidia-uvm", "/dev/nvidia-uvm-tools", "/dev/nvidia-modeset"}

func deviceNodesForMode(mode string, rec inventory.GPU) ([]string, error) {
	if mode == gpu.ModeVFIO {
		return nil, nil
	}
	nodes := locatorsFromGPU(rec)
	if rec.Driver == "nvidia" {
		if rec.NVIDIAMinor == nil {
			return nil, errUnprocessable("NVIDIA device minor is unavailable in inventory; refresh node inventory after the NVIDIA driver loads")
		}
		nodes = append(nodes, "/dev/nvidia"+strconv.Itoa(*rec.NVIDIAMinor))
		nodes = append(nodes, nvidiaSharedNodes...)
	}
	if len(nodes) == 0 {
		return nil, errUnprocessable("GPU device nodes are unavailable in inventory")
	}
	return nodes, nil
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
