package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"path"
	"strings"

	"github.com/google/uuid"
	"github.com/no-dal/ndl-ce/internal/appdb"
	"github.com/no-dal/ndl-ce/internal/gpu"
	"github.com/no-dal/ndl-ce/internal/inventory"
	"github.com/no-dal/ndl-ce/internal/qemu"
	"github.com/no-dal/ndl-ce/internal/rbac"
	"github.com/no-dal/ndl-ce/internal/storage"
	"github.com/no-dal/ndl-ce/internal/vmspec"
)

func firmwareCodeForSpec(spec vmspec.Spec) (string, error) {
	if spec.Firmware != vmspec.FirmwareUEFI {
		return "", nil
	}
	if spec.SecureBoot {
		code := qemu.DetectSecbootFirmware()
		if code == "" {
			return "", errConflict("secure boot firmware is unavailable on this host")
		}
		return code, nil
	}
	code := qemu.DetectFirmware()
	if code == "" {
		return "", errConflict("uefi firmware is unavailable on this host")
	}
	return code, nil
}

func (s *Server) cloneVM(w http.ResponseWriter, r *http.Request, p *principal, src appdb.Workload) {
	if !rbac.Authorize(p.Grants, rbac.ComputeCreate) {
		writeErr(w, http.StatusForbidden, "forbidden")
		return
	}
	var body struct {
		Name string `json:"name"`
	}
	_ = readJSON(r, &body)
	cloned, err := s.cloneVMRow(r.Context(), p.User.ClusterID, src, body.Name)
	if err != nil {
		writeErr(w, statusFor(err), err.Error())
		return
	}
	s.audit(r, p.User.ClusterID, p.User.ID, "vm.clone", "ok", cloned.ID)
	writeJSON(w, http.StatusCreated, s.workloadJSON(r.Context(), *cloned))
}

func (s *Server) cloneVMRow(ctx context.Context, clusterID string, src appdb.Workload, name string) (*appdb.Workload, error) {
	if s.VM == nil || s.Backup == nil || s.Storage == nil {
		return nil, errUnavailable("vm agent is unavailable")
	}
	vol, pool, tip, err := s.bootVolumeLocator(ctx, clusterID, src)
	if err != nil {
		return nil, err
	}
	spec, err := vmspec.Parse(src.SpecJSON)
	if err != nil {
		spec = vmspec.Spec{Name: src.Name, CPUs: src.CPUs, MemoryBytes: src.MemoryBytes, Firmware: src.Firmware}
	}
	for _, d := range spec.Disks {
		if d.Role == vmspec.DiskRoleData && d.VolumeID != "" && d.VolumeID != vol.ID {
			return nil, errUnprocessable("clone of additional data disks is not implemented")
		}
	}
	newID := uuid.NewString()
	newVolID := uuid.NewString()
	if strings.TrimSpace(name) == "" {
		name = src.Name + "-clone"
	}
	hint := appdb.PoolHints([]appdb.StoragePool{*pool})[0]
	res, err := s.Storage.CreateDirectoryVolume(ctx, storage.CreateVolumeRequest{
		VolumeID: newVolID, PoolID: pool.ID, RootPath: pool.RootPath,
		Class: storage.ClassVMDisk, Size: vol.SizeBytes, Format: firstNonEmpty(vol.Format, storage.FormatQCOW2),
	}, hint)
	if err != nil && !strings.Contains(err.Error(), "duplicate") {
		return nil, err
	}
	backend := res.Handle.BackendRef
	if backend == "" {
		backend = path.Join("volumes", storage.ClassVMDisk, newVolID+".qcow2")
	}
	newVol := appdb.Volume{
		ID: newVolID, ClusterID: clusterID, NodeID: src.NodeID, PoolID: pool.ID,
		Class: storage.ClassVMDisk, Kind: storage.KindBlock, Format: firstNonEmpty(vol.Format, storage.FormatQCOW2),
		SizeBytes: vol.SizeBytes, Status: storage.StatusAvailable, BackendType: pool.BackendType, BackendRef: backend,
	}
	if err := s.Store.CreateVolume(ctx, newVol); err != nil {
		return nil, err
	}
	dest, err := storage.JoinUnder(pool.RootPath, newVol.BackendRef)
	if err != nil {
		s.rollbackAdoptedVM(ctx, clusterID, "", newVolID, "")
		return nil, errConflict("volume locator is invalid")
	}
	if _, err := s.Backup.CopyBackup(ctx, qemu.BackupCopy, tip, dest); err != nil {
		s.rollbackAdoptedVM(ctx, clusterID, "", newVolID, dest)
		return nil, err
	}
	spec.Name = name
	spec.USBs = nil
	spec.PCIHosts = nil
	for i := range spec.Disks {
		if spec.Disks[i].Role == vmspec.DiskRoleBoot || spec.Disks[i].VolumeID == vol.ID {
			spec.Disks[i].VolumeID = newVolID
		}
	}
	if len(spec.Disks) == 0 {
		spec.Disks = []vmspec.Disk{{Role: vmspec.DiskRoleBoot, VolumeID: newVolID, Format: "qcow2"}}
	}
	for i := range spec.NICs {
		spec.NICs[i].MAC = ""
		spec.NICs[i].ID = ""
	}
	spec = vmspec.PersistNICs(newID, spec)
	spec, _, err = vmspec.AllocatePCI(spec)
	if err != nil {
		s.rollbackAdoptedVM(ctx, clusterID, "", newVolID, dest)
		return nil, err
	}
	row := appdb.Workload{
		ID: newID, ClusterID: clusterID, NodeID: src.NodeID, OwnerNodeID: src.OwnerNodeID, DesiredNodeID: src.DesiredNodeID,
		Name: spec.Name, Kind: vmspec.KindVM, Status: qemu.StatusStopped, DesiredPower: "stopped",
		CPUs: spec.CPUs, MemoryBytes: spec.MemoryBytes, SpecJSON: vmspec.MustJSON(spec),
		Autostart: spec.Autostart, Firmware: spec.Firmware,
		MigrateBlockers: json.RawMessage(`[]`),
	}
	if err := s.Store.CreateWorkload(ctx, row); err != nil {
		s.rollbackAdoptedVM(ctx, clusterID, "", newVolID, dest)
		return nil, err
	}
	if err := s.Store.CreateWorkloadDisk(ctx, appdb.WorkloadDisk{
		ID: uuid.NewString(), ClusterID: clusterID, WorkloadID: newID, VolumeID: newVolID,
		Role: vmspec.DiskRoleBoot, Slot: 0, Format: firstNonEmpty(vol.Format, storage.FormatQCOW2),
	}); err != nil {
		return nil, errInternal("could not record VM disk")
	}
	nics, _ := s.Store.ListWorkloadNICs(ctx, clusterID, src.ID)
	for i, n := range nics {
		mac := ""
		if i < len(spec.NICs) {
			mac = spec.NICs[i].MAC
		}
		if err := s.Store.CreateWorkloadNIC(ctx, appdb.WorkloadNIC{
			ID: uuid.NewString(), ClusterID: clusterID, WorkloadID: newID,
			NetworkID: n.NetworkID, Model: n.Model, MAC: mac,
		}); err != nil {
			return nil, errInternal("could not record VM NIC")
		}
	}
	if _, err := s.reprepareVM(ctx, clusterID, row); err != nil {
		s.rollbackAdoptedVM(ctx, clusterID, newID, newVolID, dest)
		return nil, err
	}
	if err := s.ensureVMStorageAvailable(ctx, clusterID, newID); err != nil {
		_ = s.Store.UpdateWorkloadObserved(ctx, appdb.Workload{ID: newID, Status: qemu.StatusUnavailable, Reason: err.Error()})
		return nil, err
	}
	if _, err := s.VM.LifecycleVM(ctx, newID, "start", spec.Autostart); err != nil {
		return nil, err
	}
	_ = s.Store.UpdateWorkloadSpec(ctx, appdb.Workload{
		ID: newID, CPUs: spec.CPUs, MemoryBytes: spec.MemoryBytes, DesiredPower: "running",
		SpecJSON: vmspec.MustJSON(spec), Autostart: spec.Autostart, Firmware: spec.Firmware,
	})
	_ = s.Store.UpdateWorkloadObserved(ctx, appdb.Workload{ID: newID, Status: qemu.StatusRunning})
	cloned, _ := s.Store.GetWorkload(ctx, clusterID, newID)
	if cloned == nil {
		return &row, nil
	}
	return cloned, nil
}

func (s *Server) importVM(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.ComputeCreate)
	if err != nil {
		return
	}
	if !hasRole(p, rbac.Admin) {
		writeErr(w, http.StatusForbidden, "import is privileged")
		return
	}
	var req struct {
		Name        string `json:"name"`
		LibraryID   string `json:"library_id"`
		PoolID      string `json:"pool_id"`
		NetworkID   string `json:"network_id"`
		Firmware    string `json:"firmware"`
		CPUs        int    `json:"cpus"`
		MemoryBytes int64  `json:"memory_bytes"`
	}
	if err := readJSON(r, &req); err != nil || req.LibraryID == "" || req.NetworkID == "" {
		writeErr(w, http.StatusBadRequest, "library_id and network_id are required")
		return
	}
	created, err := s.importVMRow(r.Context(), p.User.ClusterID, req.Name, req.LibraryID, req.PoolID, req.NetworkID, req.Firmware, req.CPUs, req.MemoryBytes)
	if err != nil {
		writeErr(w, statusFor(err), err.Error())
		return
	}
	s.audit(r, p.User.ClusterID, p.User.ID, "vm.import", "ok", created.ID)
	writeJSON(w, http.StatusCreated, s.workloadJSON(r.Context(), *created))
}

func (s *Server) importVMRow(ctx context.Context, clusterID, name, libraryID, poolID, networkID, firmware string, cpus int, memory int64) (*appdb.Workload, error) {
	if s.VM == nil || s.Backup == nil || s.Storage == nil {
		return nil, errUnavailable("vm agent is unavailable")
	}
	lib, err := s.Store.GetLibraryItem(ctx, clusterID, libraryID)
	if err != nil || lib == nil {
		return nil, errNotFound("library item is not found")
	}
	libPool, err := s.Store.GetStoragePool(ctx, clusterID, lib.PoolID)
	if err != nil || libPool == nil {
		return nil, errConflict("library storage is unavailable")
	}
	src, err := storage.JoinUnder(libPool.RootPath, lib.BackendRef)
	if err != nil {
		return nil, errConflict("library locator is invalid")
	}
	node, err := s.Store.GetNode(ctx, clusterID)
	if err != nil || node == nil {
		return nil, errUnprocessable("local node is not enrolled")
	}
	pool, err := s.Store.GetStoragePool(ctx, clusterID, poolID)
	if err != nil || pool == nil {
		pools, _ := s.Store.ListStoragePools(ctx, clusterID)
		if len(pools) == 0 {
			return nil, errUnprocessable("no storage pool is available")
		}
		pool = &pools[0]
	}
	if strings.TrimSpace(name) == "" {
		name = "imported"
	}
	if cpus < 1 {
		cpus = 2
	}
	if memory < 64<<20 {
		memory = vmspec.DefaultMemory
	}
	if firmware == "" {
		firmware = vmspec.FirmwareBIOS
	}
	newID := uuid.NewString()
	newVolID := uuid.NewString()
	hint := appdb.PoolHints([]appdb.StoragePool{*pool})[0]
	res, err := s.Storage.CreateDirectoryVolume(ctx, storage.CreateVolumeRequest{
		VolumeID: newVolID, PoolID: pool.ID, RootPath: pool.RootPath,
		Class: storage.ClassVMDisk, Size: vmspec.DefaultDiskBytes, Format: storage.FormatQCOW2,
	}, hint)
	if err != nil && !strings.Contains(err.Error(), "duplicate") {
		return nil, err
	}
	backend := res.Handle.BackendRef
	if backend == "" {
		backend = path.Join("volumes", storage.ClassVMDisk, newVolID+".qcow2")
	}
	newVol := appdb.Volume{
		ID: newVolID, ClusterID: clusterID, NodeID: node.ID, PoolID: pool.ID,
		Class: storage.ClassVMDisk, Kind: storage.KindBlock, Format: storage.FormatQCOW2,
		SizeBytes: vmspec.DefaultDiskBytes, Status: storage.StatusAvailable, BackendType: pool.BackendType, BackendRef: backend,
	}
	if err := s.Store.CreateVolume(ctx, newVol); err != nil {
		return nil, err
	}
	dest, err := storage.JoinUnder(pool.RootPath, newVol.BackendRef)
	if err != nil {
		s.rollbackAdoptedVM(ctx, clusterID, "", newVolID, "")
		return nil, errConflict("volume locator is invalid")
	}
	if _, err := s.Backup.CopyBackup(ctx, qemu.BackupCopy, src, dest); err != nil {
		s.rollbackAdoptedVM(ctx, clusterID, "", newVolID, dest)
		return nil, err
	}
	spec := vmspec.Spec{
		Name: name, CPUs: cpus, MemoryBytes: memory, Firmware: firmware,
		Disks: []vmspec.Disk{{Role: vmspec.DiskRoleBoot, VolumeID: newVolID, Format: "qcow2"}},
		NICs:  []vmspec.NIC{{NetworkID: networkID}},
	}
	spec = vmspec.PersistNICs(newID, spec)
	spec, _, err = vmspec.AllocatePCI(spec)
	if err != nil {
		s.rollbackAdoptedVM(ctx, clusterID, "", newVolID, dest)
		return nil, err
	}
	row := appdb.Workload{
		ID: newID, ClusterID: clusterID, NodeID: node.ID, OwnerNodeID: node.ID, DesiredNodeID: node.ID,
		Name: spec.Name, Kind: vmspec.KindVM, Status: qemu.StatusStopped, DesiredPower: "stopped",
		CPUs: spec.CPUs, MemoryBytes: spec.MemoryBytes, SpecJSON: vmspec.MustJSON(spec),
		Firmware: spec.Firmware, MigrateBlockers: json.RawMessage(`[]`),
	}
	if err := s.Store.CreateWorkload(ctx, row); err != nil {
		s.rollbackAdoptedVM(ctx, clusterID, "", newVolID, dest)
		return nil, err
	}
	if err := s.Store.CreateWorkloadDisk(ctx, appdb.WorkloadDisk{
		ID: uuid.NewString(), ClusterID: clusterID, WorkloadID: newID, VolumeID: newVolID,
		Role: vmspec.DiskRoleBoot, Slot: 0, Format: storage.FormatQCOW2,
	}); err != nil {
		return nil, errInternal("could not record VM disk")
	}
	if err := s.Store.CreateWorkloadNIC(ctx, appdb.WorkloadNIC{
		ID: uuid.NewString(), ClusterID: clusterID, WorkloadID: newID, NetworkID: networkID, Model: vmspec.NICModelVirtio,
		MAC: spec.NICs[0].MAC,
	}); err != nil {
		return nil, errInternal("could not record VM NIC")
	}
	if _, err := s.reprepareVM(ctx, clusterID, row); err != nil {
		s.rollbackAdoptedVM(ctx, clusterID, newID, newVolID, dest)
		return nil, err
	}
	created, _ := s.Store.GetWorkload(ctx, clusterID, newID)
	if created == nil {
		return &row, nil
	}
	return created, nil
}

func (s *Server) exportVM(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.ComputeRead)
	if err != nil {
		return
	}
	if !rbac.Authorize(p.Grants, rbac.StorageImageUpload) && !hasRole(p, rbac.Admin) {
		writeErr(w, http.StatusForbidden, "forbidden")
		return
	}
	row, err := s.Store.GetWorkload(r.Context(), p.User.ClusterID, r.PathValue("id"))
	if err != nil || row == nil || row.Kind != vmspec.KindVM {
		writeErr(w, http.StatusNotFound, "workload not found")
		return
	}
	var body struct {
		DisplayName string `json:"display_name"`
	}
	_ = readJSON(r, &body)
	item, err := s.exportVMRow(r.Context(), p.User.ClusterID, *row, body.DisplayName)
	if err != nil {
		writeErr(w, statusFor(err), err.Error())
		return
	}
	s.audit(r, p.User.ClusterID, p.User.ID, "vm.export", "ok", row.ID)
	writeJSON(w, http.StatusCreated, map[string]any{"id": item.ID, "kind": item.Kind, "display_name": item.DisplayName, "backend_ref": item.BackendRef})
}

func (s *Server) exportVMRow(ctx context.Context, clusterID string, src appdb.Workload, display string) (*appdb.LibraryItem, error) {
	if s.Backup == nil {
		return nil, errUnavailable("vm agent is unavailable")
	}
	_, pool, tip, err := s.bootVolumeLocator(ctx, clusterID, src)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(display) == "" {
		display = src.Name + ".qcow2"
	}
	itemID := uuid.NewString()
	ref := path.Join("library", storage.LibraryDiskImage, itemID+".qcow2")
	dest, err := storage.JoinUnder(pool.RootPath, ref)
	if err != nil {
		return nil, errConflict("export locator is invalid")
	}
	if _, err := s.Backup.CopyBackup(ctx, qemu.BackupCopy, tip, dest); err != nil {
		return nil, err
	}
	item := appdb.LibraryItem{
		ID: itemID, ClusterID: clusterID, PoolID: pool.ID, Kind: storage.LibraryDiskImage,
		DisplayName: display, BackendRef: ref, Status: storage.StatusAvailable,
	}
	if err := s.Store.CreateLibraryItem(ctx, item); err != nil {
		return nil, err
	}
	return &item, nil
}

func (s *Server) listTemplates(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.ComputeRead)
	if err != nil {
		return
	}
	items, err := s.Store.ListVMTemplates(r.Context(), p.User.ClusterID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := make([]map[string]any, 0, len(items))
	for _, t := range items {
		out = append(out, templateJSON(t))
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": out})
}

func (s *Server) createTemplate(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.ComputeCreate)
	if err != nil {
		return
	}
	var req struct {
		WorkloadID string `json:"workload_id"`
		Name       string `json:"name"`
	}
	if err := readJSON(r, &req); err != nil || req.WorkloadID == "" {
		writeErr(w, http.StatusBadRequest, "workload_id is required")
		return
	}
	row, err := s.Store.GetWorkload(r.Context(), p.User.ClusterID, req.WorkloadID)
	if err != nil || row == nil || row.Kind != vmspec.KindVM {
		writeErr(w, http.StatusNotFound, "workload not found")
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		name = row.Name + "-template"
	}
	if s.VM == nil {
		writeErr(w, http.StatusUnprocessableEntity, "template snapshot is unavailable")
		return
	}
	vol, pool, tip, locErr := s.bootVolumeLocator(r.Context(), p.User.ClusterID, *row)
	if locErr != nil || vol == nil || pool == nil {
		writeErr(w, http.StatusUnprocessableEntity, "template snapshot is unavailable")
		return
	}
	overlayRel := path.Join("volumes", storage.ClassVMDisk, vol.ID+"-tmpl.qcow2")
	overlay, jerr := storage.JoinUnder(pool.RootPath, overlayRel)
	if jerr != nil {
		writeErr(w, http.StatusUnprocessableEntity, "template snapshot is unavailable")
		return
	}
	res, serr := s.VM.SnapshotVM(r.Context(), qemu.OverlayRequest{
		WorkloadID: row.ID, Action: "create", OverlayPath: overlay, BackingPath: tip,
	})
	if serr != nil {
		writeErr(w, statusFor(serr), serr.Error())
		return
	}
	if err := s.Store.UpdateVolumeLocator(r.Context(), p.User.ClusterID, vol.ID, overlayRel); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	snap := appdb.Snapshot{
		ID: uuid.NewString(), ClusterID: p.User.ClusterID, WorkloadID: row.ID, VolumeID: vol.ID,
		Name: name, PurposeTag: "template", Mechanism: res.Mechanism, BackendRef: overlayRel, Status: "available",
	}
	if err := s.Store.CreateSnapshot(r.Context(), snap); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	spec, _ := vmspec.Parse(row.SpecJSON)
	tmpl := appdb.VMTemplate{
		ID: uuid.NewString(), ClusterID: p.User.ClusterID, Name: name,
		SourceWorkloadID: row.ID, SnapshotID: snap.ID, SpecJSON: vmspec.MustJSON(vmspec.Redact(spec)),
	}
	if err := s.Store.CreateVMTemplate(r.Context(), tmpl); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.audit(r, p.User.ClusterID, p.User.ID, "vm.template", "ok", tmpl.ID)
	writeJSON(w, http.StatusCreated, templateJSON(tmpl))
}

func (s *Server) deployTemplate(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.ComputeCreate)
	if err != nil {
		return
	}
	tmpl, err := s.Store.GetVMTemplate(r.Context(), p.User.ClusterID, r.PathValue("id"))
	if err != nil || tmpl == nil {
		writeErr(w, http.StatusNotFound, "template not found")
		return
	}
	var body struct {
		Name string `json:"name"`
	}
	_ = readJSON(r, &body)
	src, err := s.Store.GetWorkload(r.Context(), p.User.ClusterID, tmpl.SourceWorkloadID)
	if err != nil || src == nil {
		writeErr(w, http.StatusConflict, "template source workload is gone")
		return
	}
	if strings.TrimSpace(tmpl.SnapshotID) == "" {
		writeErr(w, http.StatusUnprocessableEntity, "template has no snapshot; deploy would clone the live source")
		return
	}
	name := strings.TrimSpace(body.Name)
	if name == "" {
		name = tmpl.Name
	}
	cloned, err := s.cloneVMRow(r.Context(), p.User.ClusterID, *src, name)
	if err != nil {
		writeErr(w, statusFor(err), err.Error())
		return
	}
	out := s.workloadJSON(r.Context(), *cloned)
	out["deploy_source"] = "live_workload"
	out["note"] = "deploy clones the live source workload"
	writeJSON(w, http.StatusCreated, out)
}

func templateJSON(t appdb.VMTemplate) map[string]any {
	return map[string]any{
		"id": t.ID, "name": t.Name, "source_workload_id": t.SourceWorkloadID,
		"snapshot_id": t.SnapshotID, "created_at": t.CreatedAt.UTC().Format("2006-01-02T15:04:05Z"),
	}
}

func (s *Server) listNodeUSB(w http.ResponseWriter, r *http.Request) {
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
	claims, _ := s.Store.ListUSBAttachments(r.Context(), p.User.ClusterID, "")
	items := make([]map[string]any, 0, len(parsed.USB))
	for _, u := range parsed.USB {
		row := map[string]any{"address": u.Address, "vendor": u.Vendor, "product": u.Product, "name": u.Name}
		for _, c := range claims {
			if c.Address == u.Address {
				row["claimed_by"] = c.WorkloadID
			}
		}
		items = append(items, row)
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) listNodePCI(w http.ResponseWriter, r *http.Request) {
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
	items := make([]map[string]any, 0, len(parsed.PCI))
	gpuIDs := map[string]struct{}{}
	for _, g := range parsed.GPUs {
		gpuIDs[strings.ToLower(g.ID)] = struct{}{}
		if g.PCI != "" {
			gpuIDs[strings.ToLower(g.PCI)] = struct{}{}
		}
	}
	for _, d := range parsed.PCI {
		id := strings.ToLower(d.Address)
		if _, isGPU := gpuIDs[id]; isGPU {
			continue
		}
		row := map[string]any{
			"id": d.Address, "vendor": d.Vendor, "device": d.Device, "class": d.Class,
			"driver": d.Driver, "iommu_group": d.IOMMUGroup,
		}
		for _, a := range assigns {
			if strings.EqualFold(a.GPUID, d.Address) {
				row["claimed_by"] = a.WorkloadID
			}
		}
		items = append(items, row)
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "iommu": parsed.IOMMU})
}

func (s *Server) attachUSB(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.ComputeModify)
	if err != nil {
		return
	}
	row, err := s.Store.GetWorkload(r.Context(), p.User.ClusterID, r.PathValue("id"))
	if err != nil || row == nil || row.Kind != vmspec.KindVM {
		writeErr(w, http.StatusNotFound, "workload not found")
		return
	}
	var req struct {
		Address string `json:"address"`
	}
	if err := readJSON(r, &req); err != nil || req.Address == "" {
		writeErr(w, http.StatusBadRequest, "address is required")
		return
	}
	_, invRow, err := s.cachedNode(r, p.User.ClusterID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	parsed, _ := decodeInv(invRow)
	var found *inventory.USBDevice
	for i := range parsed.USB {
		if parsed.USB[i].Address == req.Address {
			found = &parsed.USB[i]
			break
		}
	}
	if found == nil {
		writeErr(w, http.StatusUnprocessableEntity, "usb device is not in inventory")
		return
	}
	usb := vmspec.USB{Address: found.Address, Vendor: found.Vendor, Product: found.Product}
	if err := vmspec.ValidateUSB(usb); err != nil {
		writeErr(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	att := appdb.USBAttachment{
		ID: uuid.NewString(), ClusterID: p.User.ClusterID, WorkloadID: row.ID,
		Address: usb.Address, Vendor: usb.Vendor, Product: usb.Product, Exclusive: true,
	}
	if err := s.Store.CreateUSBAttachment(r.Context(), att); err != nil {
		writeErr(w, http.StatusConflict, err.Error())
		return
	}
	spec, _ := vmspec.Parse(row.SpecJSON)
	spec.USBs = append(spec.USBs, usb)
	if err := s.Store.UpdateWorkloadSpec(r.Context(), appdb.Workload{ID: row.ID, SpecJSON: vmspec.MustJSON(spec), Firmware: row.Firmware, CPUs: row.CPUs, MemoryBytes: row.MemoryBytes}); err != nil {
		writeErr(w, http.StatusInternalServerError, "could not record USB spec")
		return
	}
	launchUSB := vmspec.LaunchUSB{Address: usb.Address, Vendor: strings.ToLower(usb.Vendor), Product: strings.ToLower(usb.Product), ID: vmspec.USBDeviceID(usb.Address)}
	if s.VM != nil {
		if err := s.VM.ApplyUSB(r.Context(), row.ID, []vmspec.LaunchUSB{launchUSB}); err != nil {
			_ = s.Store.DeleteUSBAttachment(r.Context(), p.User.ClusterID, att.ID)
			writeErr(w, http.StatusConflict, err.Error())
			return
		}
	}
	s.audit(r, p.User.ClusterID, p.User.ID, "vm.usb.attach", "ok", row.ID)
	writeJSON(w, http.StatusCreated, map[string]any{"id": att.ID, "address": att.Address, "vendor": att.Vendor, "product": att.Product})
}

func (s *Server) attachPCI(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.ComputeGPUAssign)
	if err != nil {
		return
	}
	row, err := s.Store.GetWorkload(r.Context(), p.User.ClusterID, r.PathValue("id"))
	if err != nil || row == nil || row.Kind != vmspec.KindVM {
		writeErr(w, http.StatusNotFound, "workload not found")
		return
	}
	var req struct {
		PCI string `json:"pci"`
	}
	if err := readJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "pci is required")
		return
	}
	id, err := gpu.ParseGPUID(req.PCI)
	if err != nil {
		writeErr(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	_, invRow, err := s.cachedNode(r, p.User.ClusterID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	parsed, _ := decodeInv(invRow)
	found := false
	group := ""
	for _, d := range parsed.PCI {
		if strings.EqualFold(d.Address, id) {
			found = true
			group = d.IOMMUGroup
			break
		}
	}
	if !found {
		writeErr(w, http.StatusUnprocessableEntity, "pci device is not in inventory")
		return
	}
	for _, g := range parsed.GPUs {
		if strings.EqualFold(g.ID, id) || strings.EqualFold(g.PCI, id) {
			writeErr(w, http.StatusUnprocessableEntity, "GPU devices must be assigned through the GPU API")
			return
		}
	}
	if parsed.IOMMU.Status != inventory.StatusAvailable {
		writeErr(w, http.StatusUnprocessableEntity, "IOMMU is unavailable; VFIO cannot be assigned")
		return
	}
	if row.Status == qemu.StatusRunning {
		writeErr(w, http.StatusUnprocessableEntity, "stop the VM before PCI passthrough")
		return
	}
	a := appdb.GPUAssignment{
		ID: uuid.NewString(), ClusterID: p.User.ClusterID, GPUID: id, WorkloadID: row.ID,
		Mode: gpu.ModeVFIO, Exclusive: true, IOMMUGroup: group, Status: gpu.StatusAssigned,
	}
	if err := s.Store.CreateGPUAssignment(r.Context(), a); err != nil {
		writeErr(w, http.StatusConflict, err.Error())
		return
	}
	spec, _ := vmspec.Parse(row.SpecJSON)
	foundHost := false
	for _, h := range spec.PCIHosts {
		if strings.EqualFold(h, id) {
			foundHost = true
			break
		}
	}
	if !foundHost {
		spec.PCIHosts = append(spec.PCIHosts, id)
	}
	if err := s.Store.UpdateWorkloadSpec(r.Context(), appdb.Workload{ID: row.ID, SpecJSON: vmspec.MustJSON(spec), Firmware: row.Firmware, CPUs: row.CPUs, MemoryBytes: row.MemoryBytes}); err != nil {
		writeErr(w, http.StatusInternalServerError, "could not record PCI spec")
		return
	}
	if s.VM != nil {
		hosts := pciGroupHosts(id, parsed)
		if err := s.VM.ApplyVFIO(r.Context(), row.ID, hosts); err != nil {
			_ = s.Store.DeleteGPUAssignment(r.Context(), p.User.ClusterID, a.ID)
			_ = s.Store.UpdateWorkloadSpec(r.Context(), appdb.Workload{
				ID: row.ID, SpecJSON: row.SpecJSON, Firmware: row.Firmware, CPUs: row.CPUs, MemoryBytes: row.MemoryBytes,
			})
			writeErr(w, http.StatusConflict, err.Error())
			return
		}
	}
	s.audit(r, p.User.ClusterID, p.User.ID, "vm.pci.attach", "ok", row.ID)
	writeJSON(w, http.StatusCreated, map[string]any{"id": a.ID, "pci": id, "iommu_group": group})
}

func (s *Server) rollbackAdoptedVM(ctx context.Context, clusterID, workloadID, volumeID, dest string) {
	if workloadID != "" {
		_ = s.Store.DeleteWorkload(ctx, clusterID, workloadID)
	}
	if dest != "" && s.Backup != nil {
		_, _ = s.Backup.CopyBackup(ctx, qemu.BackupDelete, "", dest)
	}
	if volumeID != "" {
		_ = s.Store.DeleteVolume(ctx, clusterID, volumeID)
	}
}

func pciGroupHosts(id string, inv inventory.Inventory) []string {
	id = strings.ToLower(strings.TrimSpace(id))
	group := ""
	for _, d := range inv.PCI {
		if strings.EqualFold(d.Address, id) {
			group = d.IOMMUGroup
			break
		}
	}
	hosts := []string{id}
	if group == "" {
		return hosts
	}
	seen := map[string]struct{}{id: {}}
	for _, d := range inv.PCI {
		if d.IOMMUGroup != group {
			continue
		}
		addr := strings.ToLower(strings.TrimSpace(d.Address))
		if addr == "" {
			continue
		}
		if _, ok := seen[addr]; ok {
			continue
		}
		seen[addr] = struct{}{}
		hosts = append(hosts, addr)
	}
	return hosts
}

func vfioHostsForWorkload(ctx context.Context, s *Server, clusterID, workloadID string) []string {
	assigns, _ := s.Store.ListGPUAssignments(ctx, clusterID)
	hosts := make([]string, 0, len(assigns))
	seen := map[string]struct{}{}
	for _, a := range assigns {
		if a.WorkloadID != workloadID || a.Mode != gpu.ModeVFIO {
			continue
		}
		id := strings.ToLower(strings.TrimSpace(a.GPUID))
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		hosts = append(hosts, id)
	}
	return hosts
}
