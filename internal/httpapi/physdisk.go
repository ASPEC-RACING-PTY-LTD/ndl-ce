package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/google/uuid"
	"github.com/no-dal/ndl-ce/internal/appdb"
	"github.com/no-dal/ndl-ce/internal/inventory"
	"github.com/no-dal/ndl-ce/internal/physdisk"
	"github.com/no-dal/ndl-ce/internal/qemu"
	"github.com/no-dal/ndl-ce/internal/rbac"
	"github.com/no-dal/ndl-ce/internal/vmspec"
)

func (s *Server) physFS() inventory.FS {
	if strings.TrimSpace(s.PhysFS.Root) != "" {
		return s.PhysFS
	}
	return inventory.Live()
}

func (s *Server) listPhysicalDisks(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.NodeRead)
	if err != nil {
		return
	}
	if nodeID := strings.TrimSpace(r.PathValue("id")); nodeID != "" {
		if node, nerr := s.Store.GetNodeByID(r.Context(), p.User.ClusterID, nodeID); nerr != nil || node == nil {
			writeErr(w, http.StatusNotFound, "node not found")
			return
		}
	}
	disks, err := s.discoverPhysicalDisks(r.Context(), p.User.ClusterID, "")
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	items := make([]map[string]any, 0, len(disks))
	for _, d := range disks {
		items = append(items, physicalDiskJSON(d))
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) workloadPhysicalDisks(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.ComputeRead)
	if err != nil {
		return
	}
	row, err := s.Store.GetWorkload(r.Context(), p.User.ClusterID, r.PathValue("id"))
	if err != nil || row == nil || row.Kind != vmspec.KindVM {
		writeErr(w, http.StatusNotFound, "workload not found")
		return
	}
	assigns, err := s.Store.ListPhysicalDiskAssignments(r.Context(), p.User.ClusterID, row.ID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	disks, _ := s.discoverPhysicalDisks(r.Context(), p.User.ClusterID, row.ID)
	items := make([]map[string]any, 0, len(assigns))
	for _, a := range assigns {
		item := physicalAssignmentJSON(a)
		if id, perr := physdisk.ParseDeviceID(a.DeviceID); perr == nil {
			if dev, ok := physdisk.Find(disks, id); ok {
				item["device"] = physicalDiskJSON(dev)
			}
		}
		items = append(items, item)
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) assignPhysicalDiskAPI(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.ComputeModify)
	if err != nil {
		return
	}
	var req struct {
		WorkloadID string `json:"workload_id"`
		DeviceID   string `json:"device_id"`
		Role       string `json:"role"`
		Bus        string `json:"bus"`
		Boot       bool   `json:"boot"`
	}
	if err := readJSON(r, &req); err != nil || strings.TrimSpace(req.WorkloadID) == "" || strings.TrimSpace(req.DeviceID) == "" {
		writeErr(w, http.StatusBadRequest, "workload_id and device_id are required")
		return
	}
	s.assignPhysicalToWorkload(w, r, p, req.WorkloadID, req.DeviceID, req.Role, req.Bus, req.Boot)
}

func (s *Server) assignWorkloadPhysicalDisk(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.ComputeModify)
	if err != nil {
		return
	}
	var req struct {
		DeviceID string `json:"device_id"`
		Role     string `json:"role"`
		Bus      string `json:"bus"`
		Boot     bool   `json:"boot"`
	}
	if err := readJSON(r, &req); err != nil || strings.TrimSpace(req.DeviceID) == "" {
		writeErr(w, http.StatusBadRequest, "device_id is required")
		return
	}
	s.assignPhysicalToWorkload(w, r, p, r.PathValue("id"), req.DeviceID, req.Role, req.Bus, req.Boot)
}

func (s *Server) unassignPhysicalDiskAPI(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.ComputeModify)
	if err != nil {
		return
	}
	var req struct {
		WorkloadID string `json:"workload_id"`
		DeviceID   string `json:"device_id"`
	}
	if err := readJSON(r, &req); err != nil || strings.TrimSpace(req.DeviceID) == "" {
		writeErr(w, http.StatusBadRequest, "device_id is required")
		return
	}
	s.removePhysicalFromWorkload(w, r, p, req.WorkloadID, req.DeviceID)
}

func (s *Server) removeWorkloadPhysicalDisk(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.ComputeModify)
	if err != nil {
		return
	}
	s.removePhysicalFromWorkload(w, r, p, r.PathValue("id"), r.PathValue("device_id"))
}

func (s *Server) assignPhysicalToWorkload(w http.ResponseWriter, r *http.Request, p *principal, workloadID, deviceID, role, bus string, boot bool) {
	row, err := s.Store.GetWorkload(r.Context(), p.User.ClusterID, workloadID)
	if err != nil || row == nil || row.Kind != vmspec.KindVM {
		writeErr(w, http.StatusNotFound, "workload not found")
		return
	}
	running := row.UnitActive || row.Status == qemu.StatusRunning || row.Status == qemu.StatusStarting
	if running {
		writeErr(w, http.StatusConflict, "attaching a physical disk requires the VM to be stopped")
		return
	}
	spec, err := vmspec.Parse(row.SpecJSON)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	disk := vmspec.Disk{
		Role:     firstNonEmpty(role, vmspec.DiskRoleData),
		Source:   vmspec.DiskSourcePhysical,
		DeviceID: deviceID,
		Bus:      firstNonEmpty(bus, vmspec.DiskBusAHCI),
		Format:   "raw",
		Slot:     nextPhysicalSlot(spec),
	}
	if boot || disk.Role == vmspec.DiskRoleBoot {
		disk.Role = vmspec.DiskRoleBoot
		for i := range spec.Disks {
			if spec.Disks[i].Role == vmspec.DiskRoleBoot {
				if isPhysicalSpecDisk(spec.Disks[i]) {
					spec.Disks[i].Role = vmspec.DiskRoleData
				} else {
					writeErr(w, http.StatusConflict, "this VM already has a virtual boot disk. Stop and edit the spec to boot from the physical disk instead")
					return
				}
			}
		}
	}
	gate := physdisk.AssignGate()
	gate.Lock()
	defer gate.Unlock()
	if err := s.persistPhysicalAssignment(r.Context(), p.User.ClusterID, row.ID, row.Name, disk); err != nil {
		writeErr(w, statusFor(err), err.Error())
		return
	}
	spec.Disks = append(spec.Disks, disk)
	spec = vmspec.Normalize(spec)
	if err := vmspec.Validate(spec); err != nil {
		_ = s.Store.DeletePhysicalDiskAssignment(r.Context(), p.User.ClusterID, disk.DeviceID)
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.Store.UpdateWorkloadSpec(r.Context(), appdb.Workload{
		ID: row.ID, CPUs: spec.CPUs, MemoryBytes: spec.MemoryBytes, DesiredPower: row.DesiredPower,
		SpecJSON: vmspec.MustJSON(spec), AppliedJSON: row.AppliedJSON, Autostart: spec.Autostart,
		PendingRestart: false, Firmware: spec.Firmware,
	}); err != nil {
		_ = s.Store.DeletePhysicalDiskAssignment(r.Context(), p.User.ClusterID, disk.DeviceID)
		writeErr(w, http.StatusInternalServerError, "could not record VM spec")
		return
	}
	s.audit(r, p.User.ClusterID, p.User.ID, "vm.physical_disk.assign", "ok", row.ID)
	updated, _ := s.Store.GetWorkload(r.Context(), p.User.ClusterID, row.ID)
	if updated == nil {
		updated = row
	}
	writeJSON(w, http.StatusOK, s.workloadJSON(r.Context(), *updated))
}

func (s *Server) removePhysicalFromWorkload(w http.ResponseWriter, r *http.Request, p *principal, workloadID, deviceID string) {
	id, err := physdisk.ParseDeviceID(deviceID)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	row, err := s.Store.GetWorkload(r.Context(), p.User.ClusterID, workloadID)
	if err != nil || row == nil || row.Kind != vmspec.KindVM {
		if workloadID == "" {
			existing, gerr := s.Store.GetPhysicalDiskAssignment(r.Context(), p.User.ClusterID, string(id))
			if gerr != nil || existing == nil {
				writeErr(w, http.StatusNotFound, "physical disk assignment is not found")
				return
			}
			row, err = s.Store.GetWorkload(r.Context(), p.User.ClusterID, existing.WorkloadID)
		}
		if err != nil || row == nil || row.Kind != vmspec.KindVM {
			writeErr(w, http.StatusNotFound, "workload not found")
			return
		}
	}
	running := row.UnitActive || row.Status == qemu.StatusRunning || row.Status == qemu.StatusStarting
	if running {
		writeErr(w, http.StatusConflict, "removing a physical disk requires the VM to be stopped")
		return
	}
	spec, err := vmspec.Parse(row.SpecJSON)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	kept := spec.Disks[:0]
	removedBoot := false
	for _, d := range spec.Disks {
		if isPhysicalSpecDisk(d) && samePhysicalID(d.DeviceID, string(id)) {
			if d.Role == vmspec.DiskRoleBoot {
				removedBoot = true
			}
			continue
		}
		kept = append(kept, d)
	}
	if removedBoot {
		writeErr(w, http.StatusConflict, "the physical boot disk cannot be removed without another boot disk")
		return
	}
	spec.Disks = kept
	spec = vmspec.Normalize(spec)
	if err := vmspec.Validate(spec); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.releasePhysicalAssignment(r.Context(), p.User.ClusterID, row.ID, id); err != nil {
		writeErr(w, statusFor(err), err.Error())
		return
	}
	if err := s.Store.UpdateWorkloadSpec(r.Context(), appdb.Workload{
		ID: row.ID, CPUs: spec.CPUs, MemoryBytes: spec.MemoryBytes, DesiredPower: row.DesiredPower,
		SpecJSON: vmspec.MustJSON(spec), AppliedJSON: row.AppliedJSON, Autostart: spec.Autostart,
		PendingRestart: false, Firmware: spec.Firmware,
	}); err != nil {
		writeErr(w, http.StatusInternalServerError, "could not record VM spec")
		return
	}
	s.audit(r, p.User.ClusterID, p.User.ID, "vm.physical_disk.unassign", "ok", row.ID)
	updated, _ := s.Store.GetWorkload(r.Context(), p.User.ClusterID, row.ID)
	if updated == nil {
		updated = row
	}
	writeJSON(w, http.StatusOK, s.workloadJSON(r.Context(), *updated))
}

func (s *Server) discoverPhysicalDisks(ctx context.Context, clusterID, selfWorkloadID string) ([]physdisk.Device, error) {
	hints, err := s.physicalHostHints(ctx, clusterID, selfWorkloadID)
	if err != nil {
		return nil, err
	}
	return physdisk.Discover(s.physFS(), hints), nil
}

func (s *Server) physicalHostHints(ctx context.Context, clusterID, selfWorkloadID string) (physdisk.HostHints, error) {
	hints := physdisk.HostHints{SelfWorkloadID: selfWorkloadID}
	assigns, err := s.Store.ListPhysicalDiskAssignments(ctx, clusterID, "")
	if err != nil {
		return hints, err
	}
	workloads, _ := s.Store.ListWorkloads(ctx, clusterID)
	names := map[string]appdb.Workload{}
	for _, w := range workloads {
		names[w.ID] = w
	}
	for _, a := range assigns {
		row := names[a.WorkloadID]
		hints.Assignments = append(hints.Assignments, physdisk.Assignment{
			ID: a.ID, ClusterID: a.ClusterID, WorkloadID: a.WorkloadID,
			WorkloadName: row.Name, DeviceID: physdisk.DeviceID(a.DeviceID),
			ByIDPath: a.ByIDPath, KernelName: a.KernelName, Model: a.Model,
			Serial: a.Serial, SizeBytes: a.SizeBytes, Role: a.Role, Slot: a.Slot, Bus: a.Bus,
			Running: row.UnitActive || row.Status == qemu.StatusRunning || row.Status == qemu.StatusStarting,
		})
	}
	pools, err := s.Store.ListStoragePools(ctx, clusterID)
	if err != nil {
		return hints, err
	}
	for _, pool := range pools {
		for _, disk := range poolMemberDisks(pool) {
			hints.PoolDisks = append(hints.PoolDisks, physdisk.PoolDisk{
				Kind: pool.BackendType, Name: pool.Name, Disk: disk,
			})
		}
	}
	return hints, nil
}

func poolMemberDisks(pool appdb.StoragePool) []string {
	var out []string
	if len(pool.Backing) == 0 {
		return out
	}
	var backing struct {
		Device string   `json:"device"`
		Disks  []string `json:"disks"`
	}
	if err := json.Unmarshal(pool.Backing, &backing); err != nil {
		return out
	}
	if strings.TrimSpace(backing.Device) != "" {
		out = append(out, backing.Device)
	}
	out = append(out, backing.Disks...)
	return out
}

func (s *Server) resolvePhysicalDisk(ctx context.Context, clusterID, workloadID string, d vmspec.Disk) (vmspec.ResolvedDisk, error) {
	id, err := physdisk.ParseDeviceID(d.DeviceID)
	if err != nil {
		return vmspec.ResolvedDisk{}, errBadRequest(err.Error())
	}
	disks, err := s.discoverPhysicalDisks(ctx, clusterID, workloadID)
	if err != nil {
		return vmspec.ResolvedDisk{}, err
	}
	dev, ok := physdisk.Find(disks, id)
	if !ok {
		return vmspec.ResolvedDisk{}, errConflict(physdisk.BlockedError(physdisk.Device{ID: id, Reasons: []string{physdisk.ReasonMissing}}).Error())
	}
	if !dev.Eligible {
		return vmspec.ResolvedDisk{}, errConflict(physdisk.BlockedError(dev).Error())
	}
	path, err := physdisk.ResolvePath(s.physFS(), id)
	if err != nil {
		return vmspec.ResolvedDisk{}, errConflict(err.Error())
	}
	discard := dev.Rotational != nil && !*dev.Rotational
	return vmspec.ResolvedDisk{
		Role:     firstNonEmpty(d.Role, vmspec.DiskRoleData),
		Slot:     d.Slot,
		Path:     path,
		Format:   "raw",
		ReadOnly: d.ReadOnly,
		PCIAddr:  d.PCIAddr,
		Source:   vmspec.DiskSourcePhysical,
		DeviceID: string(id),
		Bus:      firstNonEmpty(d.Bus, vmspec.DiskBusAHCI),
		Serial:   firstNonEmpty(dev.Serial, string(id)),
		Discard:  discard,
	}, nil
}

func (s *Server) persistPhysicalAssignment(ctx context.Context, clusterID, workloadID, workloadName string, d vmspec.Disk) error {
	id, err := physdisk.ParseDeviceID(d.DeviceID)
	if err != nil {
		return errBadRequest(err.Error())
	}
	if existing, err := s.Store.GetPhysicalDiskAssignment(ctx, clusterID, string(id)); err != nil {
		return err
	} else if existing != nil && existing.WorkloadID != workloadID {
		name := existing.WorkloadID
		if workloadName != "" && existing.WorkloadID != "" {
			if row, _ := s.Store.GetWorkload(ctx, clusterID, existing.WorkloadID); row != nil {
				name = row.Name
			}
		}
		return errConflict("Physical disk " + string(id) + " is already assigned to VM " + name + ".")
	} else if existing != nil {
		return nil
	}
	disks, err := s.discoverPhysicalDisks(ctx, clusterID, workloadID)
	if err != nil {
		return err
	}
	dev, ok := physdisk.Find(disks, id)
	if !ok {
		return errConflict(physdisk.BlockedError(physdisk.Device{ID: id, Reasons: []string{physdisk.ReasonMissing}}).Error())
	}
	if !dev.Eligible {
		return errConflict(physdisk.BlockedError(dev).Error())
	}
	row := appdb.PhysicalDiskAssignment{
		ID: uuid.NewString(), ClusterID: clusterID, WorkloadID: workloadID,
		DeviceID: string(id), ByIDPath: firstNonEmpty(dev.ByIDPath, id.Path()),
		KernelName: dev.KernelName, Model: dev.Model, Serial: dev.Serial,
		SizeBytes: int64(dev.SizeBytes), Role: firstNonEmpty(d.Role, vmspec.DiskRoleData),
		Slot: d.Slot, Bus: firstNonEmpty(d.Bus, vmspec.DiskBusAHCI),
	}
	if err := s.Store.CreatePhysicalDiskAssignment(ctx, row); err != nil {
		return errConflict(err.Error())
	}
	return nil
}

func (s *Server) persistSpecPhysicalDisks(ctx context.Context, clusterID, workloadID, name string, spec vmspec.Spec) error {
	for _, d := range spec.Disks {
		if !isPhysicalSpecDisk(d) {
			continue
		}
		if err := s.persistPhysicalAssignment(ctx, clusterID, workloadID, name, d); err != nil {
			return err
		}
	}
	return nil
}

func (s *Server) releasePhysicalAssignment(ctx context.Context, clusterID, workloadID string, id physdisk.DeviceID) error {
	_ = physdisk.Release(id, workloadID)
	existing, err := s.Store.GetPhysicalDiskAssignment(ctx, clusterID, string(id))
	if err != nil {
		return err
	}
	if existing == nil {
		return nil
	}
	if existing.WorkloadID != workloadID {
		return errConflict("Physical disk " + string(id) + " is assigned to another VM.")
	}
	return s.Store.DeletePhysicalDiskAssignment(ctx, clusterID, string(id))
}

func (s *Server) releasePhysicalAssignments(ctx context.Context, clusterID, workloadID string) error {
	assigns, err := s.Store.ListPhysicalDiskAssignments(ctx, clusterID, workloadID)
	if err != nil {
		return err
	}
	for _, a := range assigns {
		if id, perr := physdisk.ParseDeviceID(a.DeviceID); perr == nil {
			_ = physdisk.Release(id, workloadID)
			if a.ByIDPath != "" {
				_ = physdisk.RevokeRuntimeAccess(a.ByIDPath)
			}
		}
		if err := s.Store.DeletePhysicalDiskAssignment(ctx, clusterID, a.DeviceID); err != nil {
			return err
		}
	}
	return nil
}

func isPhysicalSpecDisk(d vmspec.Disk) bool {
	if strings.EqualFold(strings.TrimSpace(d.Source), vmspec.DiskSourcePhysical) {
		return true
	}
	return strings.TrimSpace(d.DeviceID) != ""
}

func specHasPhysicalBoot(spec vmspec.Spec) bool {
	for _, d := range spec.Disks {
		if d.Role == vmspec.DiskRoleBoot && isPhysicalSpecDisk(d) {
			return true
		}
	}
	return false
}

func specHasPhysicalDisks(spec vmspec.Spec) bool {
	for _, d := range spec.Disks {
		if isPhysicalSpecDisk(d) {
			return true
		}
	}
	return false
}

func samePhysicalID(a, b string) bool {
	left, err := physdisk.ParseDeviceID(a)
	if err != nil {
		return strings.TrimSpace(a) == strings.TrimSpace(b)
	}
	right, err := physdisk.ParseDeviceID(b)
	if err != nil {
		return string(left) == strings.TrimSpace(b)
	}
	return left == right
}

func nextPhysicalSlot(spec vmspec.Spec) int {
	next := 0
	for _, d := range spec.Disks {
		if d.Slot >= next {
			next = d.Slot + 1
		}
	}
	return next
}

func physicalDiskJSON(d physdisk.Device) map[string]any {
	return map[string]any{
		"id": d.ID, "by_id_path": d.ByIDPath, "kernel_name": d.KernelName, "kernel_path": d.KernelPath,
		"model": d.Model, "vendor": d.Vendor, "serial": d.Serial, "size_bytes": d.SizeBytes,
		"transport": d.Transport, "rotational": d.Rotational, "removable": d.Removable,
		"partitions": d.Partitions, "fs_signatures": d.FSSignatures, "mounted": d.Mounted,
		"swap": d.Swap, "host_disk": d.HostDisk, "pool_kind": d.PoolKind, "pool_name": d.PoolName,
		"assigned_workload_id": d.AssignedID, "assigned_workload_name": d.AssignedName,
		"assigned_running": d.AssignedRunning, "existing_data": d.ExistingData,
		"eligible": d.Eligible, "reasons": d.Reasons, "display_name": d.DisplayName(),
	}
}

func physicalAssignmentJSON(a appdb.PhysicalDiskAssignment) map[string]any {
	return map[string]any{
		"id": a.ID, "workload_id": a.WorkloadID, "device_id": a.DeviceID, "by_id_path": a.ByIDPath,
		"kernel_name": a.KernelName, "model": a.Model, "serial": a.Serial, "size_bytes": a.SizeBytes,
		"role": a.Role, "slot": a.Slot, "bus": a.Bus,
	}
}
