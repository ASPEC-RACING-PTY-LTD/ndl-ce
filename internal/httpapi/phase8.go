package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"path"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/no-dal/ndl-ce/internal/agentrpc"
	"github.com/no-dal/ndl-ce/internal/appdb"
	"github.com/no-dal/ndl-ce/internal/guest"
	"github.com/no-dal/ndl-ce/internal/ndnet"
	"github.com/no-dal/ndl-ce/internal/qemu"
	"github.com/no-dal/ndl-ce/internal/rbac"
	"github.com/no-dal/ndl-ce/internal/secutil"
	"github.com/no-dal/ndl-ce/internal/storage"
	"github.com/no-dal/ndl-ce/internal/vmspec"
)

const vmDeleteConfirm = "delete"

// VMRPC is the privileged agent surface for product VMs.
type VMRPC interface {
	PrepareVM(ctx context.Context, req agentrpc.VMPrepareRequest) (qemu.Result, error)
	LifecycleVM(ctx context.Context, id, action string, autostart bool) (qemu.Observed, error)
	QueryPCIVM(ctx context.Context, id string) (qemu.Observed, error)
	SnapshotVM(ctx context.Context, req qemu.OverlayRequest) (qemu.OverlayResult, error)
	ApplyUSB(ctx context.Context, id string, usbs []vmspec.LaunchUSB) error
	HotplugUSB(ctx context.Context, id string, add bool, usb vmspec.LaunchUSB) error
	ApplyVFIO(ctx context.Context, id string, hosts []string) error
	GuestStatus(ctx context.Context, id string) (guest.Status, error)
}

type vmUnavailable struct{}

func (vmUnavailable) PrepareVM(context.Context, agentrpc.VMPrepareRequest) (qemu.Result, error) {
	return qemu.Result{}, errUnavailable("vm agent is unavailable")
}
func (vmUnavailable) LifecycleVM(context.Context, string, string, bool) (qemu.Observed, error) {
	return qemu.Observed{}, errUnavailable("vm agent is unavailable")
}
func (vmUnavailable) QueryPCIVM(context.Context, string) (qemu.Observed, error) {
	return qemu.Observed{}, errUnavailable("vm agent is unavailable")
}

func (vmUnavailable) SnapshotVM(context.Context, qemu.OverlayRequest) (qemu.OverlayResult, error) {
	return qemu.OverlayResult{}, errUnavailable("vm agent is unavailable")
}
func (vmUnavailable) ApplyUSB(context.Context, string, []vmspec.LaunchUSB) error {
	return errUnavailable("vm agent is unavailable")
}
func (vmUnavailable) HotplugUSB(context.Context, string, bool, vmspec.LaunchUSB) error {
	return errUnavailable("vm agent is unavailable")
}
func (vmUnavailable) ApplyVFIO(context.Context, string, []string) error {
	return errUnavailable("vm agent is unavailable")
}
func (vmUnavailable) GuestStatus(context.Context, string) (guest.Status, error) {
	return guest.Status{}, errUnavailable("vm agent is unavailable")
}

func AdaptVM(client any) VMRPC {
	if v, ok := client.(VMRPC); ok {
		return v
	}
	return vmUnavailable{}
}

func specJSON(w appdb.Workload) json.RawMessage {
	if len(w.SpecJSON) == 0 {
		return json.RawMessage(`{}`)
	}
	spec, err := vmspec.Parse(w.SpecJSON)
	if err != nil {
		return json.RawMessage(`{}`)
	}
	return vmspec.MustJSON(vmspec.Redact(spec))
}

func appliedJSON(w appdb.Workload) json.RawMessage {
	if len(w.AppliedJSON) == 0 {
		return json.RawMessage(`{}`)
	}
	return w.AppliedJSON
}

func (s *Server) requireAny(w http.ResponseWriter, r *http.Request, perms ...string) (*principal, error) {
	p, err := s.principal(r)
	if err != nil {
		writeErr(w, http.StatusUnauthorized, "not authenticated")
		return nil, err
	}
	for _, perm := range perms {
		if rbac.Authorize(p.Grants, perm) {
			if !s.enforceWriter(w, r, p.User.ClusterID) {
				return nil, errors.New("not cluster writer")
			}
			return p, nil
		}
	}
	writeErr(w, http.StatusForbidden, "forbidden")
	return nil, errors.New("forbidden")
}

func (s *Server) createVM(w http.ResponseWriter, r *http.Request, p *principal, req createWorkloadRequest) {
	if req.Name == "" {
		writeErr(w, http.StatusBadRequest, "name is required")
		return
	}
	node, local, err := s.placeCreate(r.Context(), p.User.ClusterID, req)
	if err != nil {
		writeErr(w, statusFor(err), err.Error())
		return
	}
	if !local {
		id := uuid.NewString()
		row := remotePlacedWorkload(p.User.ClusterID, node, req, id, vmspec.KindVM)
		if err := s.Store.CreateWorkload(r.Context(), row); err != nil {
			writeErr(w, http.StatusConflict, "could not record workload")
			return
		}
		s.recordPlacement(r.Context(), p.User.ClusterID, row.ID, req)
		s.audit(r, p.User.ClusterID, p.User.ID, "vm.create", "ok", row.ID)
		writeJSON(w, http.StatusCreated, s.workloadJSON(r.Context(), row))
		return
	}
	if s.VM == nil || s.Storage == nil {
		writeErr(w, http.StatusBadGateway, "vm agent is unavailable")
		return
	}
	if existing, _ := s.Store.GetWorkloadByName(r.Context(), p.User.ClusterID, req.Name); existing != nil {
		writeJSON(w, http.StatusOK, s.workloadJSON(r.Context(), *existing))
		return
	}
	key := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if key != "" {
		if existing, _ := s.Store.GetWorkloadByIdempotency(r.Context(), p.User.ClusterID, key); existing != nil {
			writeJSON(w, http.StatusOK, s.workloadJSON(r.Context(), *existing))
			return
		}
	}
	spec, err := specFromCreate(req)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	ids := s.planCreateIDs(r.Context(), p.User.ClusterID, node.ID, key, req.VolumeID)
	spec = vmspec.PersistNICs(ids.WorkloadID, spec)
	spec, _, err = vmspec.AllocatePCI(spec)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := vmspec.Validate(spec); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	resolved, vol, netw, convert, err := s.resolveVM(r.Context(), p.User.ClusterID, node.ID, ids, req, spec)
	if err != nil {
		writeErr(w, statusFor(err), err.Error())
		return
	}
	launch, err := vmspec.Compile(ids.WorkloadID, spec, resolved)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	for i := range spec.Disks {
		if spec.Disks[i].Role == vmspec.DiskRoleBoot {
			spec.Disks[i].VolumeID = vol.ID
		}
	}
	if req.DesiredPower == "" {
		req.DesiredPower = "stopped"
	}
	userData, _ := vmspec.RenderUserData(spec.NoCloud)
	op := s.startOpKeyed(r.Context(), p.User.ClusterID, node.ID, "vm.create", "creating", key, mustCreateMsg(ids), 20)
	_, err = s.VM.PrepareVM(r.Context(), agentrpc.VMPrepareRequest{
		Launch: launch, UserData: userData,
		SourcePath: convert.SourcePath, SourceFormat: convert.SourceFormat,
		DestPath: convert.DestPath, DestFormat: convert.DestFormat,
	})
	if err != nil {
		s.finishOp(r.Context(), op, "failed", err.Error(), 0)
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	applied, _ := json.Marshal(launch)
	row := appdb.Workload{
		ID: ids.WorkloadID, ClusterID: p.User.ClusterID, NodeID: node.ID,
		OwnerNodeID: node.ID, DesiredNodeID: node.ID, Name: spec.Name, Kind: vmspec.KindVM,
		Status: qemu.StatusStopped, DesiredPower: req.DesiredPower, CPUs: spec.CPUs,
		MemoryBytes: spec.MemoryBytes, SpecJSON: vmspec.MustJSON(spec), AppliedJSON: applied,
		Autostart: spec.Autostart, Firmware: spec.Firmware,
		MigrateBlockers: json.RawMessage(`[]`),
		IdempotencyKey:  key,
	}
	if err := s.Store.CreateWorkload(r.Context(), row); err != nil {
		s.finishOp(r.Context(), op, "failed", err.Error(), 0)
		writeErr(w, http.StatusConflict, "could not record workload")
		return
	}
	s.recordPlacement(r.Context(), p.User.ClusterID, row.ID, req)
	if err := s.Store.CreateWorkloadDisk(r.Context(), appdb.WorkloadDisk{
		ID: uuid.NewString(), ClusterID: p.User.ClusterID, WorkloadID: row.ID,
		VolumeID: vol.ID, Role: vmspec.DiskRoleBoot, Slot: 0, BusAddr: launch.Disks[0].PCIAddr,
		Format: firstNonEmpty(vol.Format, storage.FormatQCOW2),
	}); err != nil {
		writeErr(w, http.StatusInternalServerError, "could not record VM disk")
		return
	}
	for _, d := range spec.Disks {
		if d.Role != vmspec.DiskRoleData || d.VolumeID == "" || d.VolumeID == vol.ID {
			continue
		}
		if err := s.Store.CreateWorkloadDisk(r.Context(), appdb.WorkloadDisk{
			ID: uuid.NewString(), ClusterID: p.User.ClusterID, WorkloadID: row.ID,
			VolumeID: d.VolumeID, Role: vmspec.DiskRoleData, Slot: d.Slot, BusAddr: d.PCIAddr,
			ReadOnly: d.ReadOnly, Format: firstNonEmpty(d.Format, storage.FormatQCOW2),
		}); err != nil {
			writeErr(w, http.StatusInternalServerError, "could not record VM disk")
			return
		}
	}
	for i, n := range spec.NICs {
		pci := ""
		if i < len(launch.NICs) {
			pci = launch.NICs[i].PCIAddr
		}
		if err := s.Store.CreateWorkloadNIC(r.Context(), appdb.WorkloadNIC{
			ID: firstNonEmpty(n.ID, uuid.NewString()), ClusterID: p.User.ClusterID, WorkloadID: row.ID,
			NetworkID: netw.ID, MAC: n.MAC, PCIAddr: pci, Model: vmspec.NICModelVirtio,
		}); err != nil {
			writeErr(w, http.StatusInternalServerError, "could not record VM NIC")
			return
		}
	}
	if spec.NoCloud.Enable {
		_ = s.Store.UpsertVMCidata(r.Context(), appdb.VMCidata{
			WorkloadID: row.ID, ClusterID: p.User.ClusterID, UserDataSHA: launch.NoCloud.UserDataSHA, HasPassword: spec.NoCloud.HasPassword,
		})
	}
	if spec.Firmware == vmspec.FirmwareUEFI {
		_ = s.Store.UpsertVMFirmware(r.Context(), appdb.VMFirmware{
			WorkloadID: row.ID, ClusterID: p.User.ClusterID, Mode: spec.Firmware, VarsRef: launch.Firmware.VarsPath,
		})
	}
	if req.DesiredPower == "running" {
		if err := s.ensureVMStorageAvailable(r.Context(), p.User.ClusterID, row.ID); err != nil {
			_ = s.Store.UpdateWorkloadObserved(r.Context(), appdb.Workload{ID: row.ID, Status: qemu.StatusUnavailable, Reason: err.Error()})
			s.finishOp(r.Context(), op, "failed", err.Error(), 0)
			writeErr(w, statusFor(err), err.Error())
			return
		}
		if _, err := s.VM.LifecycleVM(r.Context(), row.ID, "start", spec.Autostart); err != nil {
			s.finishOp(r.Context(), op, "failed", err.Error(), 0)
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	s.finishOp(r.Context(), op, "succeeded", mustCreateMsg(ids), 100)
	s.audit(r, p.User.ClusterID, p.User.ID, "vm.create", "ok", row.ID)
	s.emitEvent(r.Context(), p.User.ClusterID, node.ID, "vm.created", map[string]string{"workload_id": row.ID})
	s.refreshWorkloads(r.Context(), p.User.ClusterID)
	updated, _ := s.Store.GetWorkload(r.Context(), p.User.ClusterID, row.ID)
	if updated == nil {
		updated = &row
	}
	writeJSON(w, http.StatusCreated, s.workloadJSON(r.Context(), *updated))
}

func specFromCreate(req createWorkloadRequest) (vmspec.Spec, error) {
	var spec vmspec.Spec
	if len(req.Spec) > 0 && string(req.Spec) != "null" {
		parsed, err := vmspec.Parse(req.Spec)
		if err != nil {
			return vmspec.Spec{}, err
		}
		spec = parsed
		spec.USBs = nil
		spec.PCIHosts = nil
	}
	if req.Name != "" {
		spec.Name = req.Name
	}
	if req.CPUs > 0 {
		spec.CPUs = req.CPUs
	}
	if req.MemoryBytes > 0 {
		spec.MemoryBytes = req.MemoryBytes
	}
	if req.Firmware != "" {
		spec.Firmware = req.Firmware
	}
	if req.SecureBoot {
		spec.SecureBoot = true
	}
	spec.Autostart = req.Autostart
	spec.Balloon = req.Balloon
	spec.USBs = nil
	spec.PCIHosts = nil
	if req.ISOLibraryID != "" {
		spec.ISOLibraryID = req.ISOLibraryID
	}
	if req.CloudImageID != "" {
		spec.CloudImageID = req.CloudImageID
	}
	if req.NoCloud.Enable || req.NoCloud.Username != "" || req.NoCloud.Hostname != "" || req.NoCloud.Password != "" || req.NoCloud.UserData != "" || len(req.NoCloud.SSHAuthorizedKeys) > 0 {
		spec.NoCloud = req.NoCloud
		spec.NoCloud.Enable = true
	}
	if req.NetworkID != "" && len(spec.NICs) == 0 {
		spec.NICs = []vmspec.NIC{{NetworkID: req.NetworkID}}
	}
	if req.VolumeID != "" {
		found := false
		for i := range spec.Disks {
			if spec.Disks[i].Role == vmspec.DiskRoleBoot {
				spec.Disks[i].VolumeID = req.VolumeID
				found = true
			}
		}
		if !found {
			spec.Disks = append([]vmspec.Disk{{Role: vmspec.DiskRoleBoot, VolumeID: req.VolumeID, Format: "qcow2"}}, spec.Disks...)
		}
	}
	return vmspec.Normalize(spec), nil
}

func (s *Server) resolveVM(ctx context.Context, clusterID, nodeID string, ids createIDs, req createWorkloadRequest, spec vmspec.Spec) (vmspec.Resolved, *appdb.Volume, *appdb.Network, qemu.ConvertRequest, error) {
	var convert qemu.ConvertRequest
	netID := req.NetworkID
	if netID == "" && len(spec.NICs) > 0 {
		netID = spec.NICs[0].NetworkID
	}
	netw, err := s.Store.GetNetwork(ctx, clusterID, netID)
	if err != nil || netw == nil {
		return vmspec.Resolved{}, nil, nil, convert, errNotFound("network is not found")
	}
	if netw.Status != ndnet.StatusAvailable && netw.Status != ndnet.StatusWarning {
		return vmspec.Resolved{}, nil, nil, convert, errConflict("an available network is required")
	}
	bridge := netw.BridgeName
	if bridge == "" {
		bridge, err = ndnet.BridgeName(netw.ID)
		if err != nil {
			return vmspec.Resolved{}, nil, nil, convert, err
		}
	}
	pool, err := s.pickQemuPool(ctx, clusterID, req.PoolID)
	if err != nil {
		return vmspec.Resolved{}, nil, nil, convert, err
	}
	mustExist := strings.TrimSpace(req.VolumeID) != ""
	for _, d := range spec.Disks {
		if d.Role == vmspec.DiskRoleBoot && strings.TrimSpace(d.VolumeID) != "" {
			mustExist = true
		}
	}
	vol, diskPath, err := s.ensureVMBootVolume(ctx, clusterID, nodeID, pool, ids.VolumeID, spec, mustExist)
	if err != nil {
		return vmspec.Resolved{}, nil, nil, convert, err
	}
	if vol.Status != storage.StatusAvailable && vol.Status != storage.StatusWarning {
		return vmspec.Resolved{}, nil, nil, convert, errConflict("storage is unavailable")
	}
	resolved := vmspec.Resolved{
		Accel: qemu.DetectAccel(),
		Disks: []vmspec.ResolvedDisk{{
			VolumeID: vol.ID, Role: vmspec.DiskRoleBoot, Path: diskPath,
			Format: storage.QEMUFormat(vol.BackendType, vol.Format), PCIAddr: spec.Disks[0].PCIAddr,
		}},
	}
	for _, d := range spec.Disks {
		if d.Role != vmspec.DiskRoleData || strings.TrimSpace(d.VolumeID) == "" || d.VolumeID == vol.ID {
			continue
		}
		extra, eerr := s.Store.GetVolume(ctx, clusterID, d.VolumeID)
		if eerr != nil {
			return vmspec.Resolved{}, nil, nil, convert, eerr
		}
		if extra == nil {
			return vmspec.Resolved{}, nil, nil, convert, errNotFound("data volume is not found")
		}
		if extra.Class != storage.ClassVMDisk {
			return vmspec.Resolved{}, nil, nil, convert, errConflict("data volume is not a vm-disk")
		}
		if extra.Status != storage.StatusAvailable && extra.Status != storage.StatusWarning {
			return vmspec.Resolved{}, nil, nil, convert, errConflict("storage is unavailable")
		}
		epool, perr := s.Store.GetStoragePool(ctx, clusterID, extra.PoolID)
		if perr != nil || epool == nil {
			return vmspec.Resolved{}, nil, nil, convert, errConflict("storage pool is not found")
		}
		epath, jerr := storage.HostVolumePath(epool.BackendType, epool.RootPath, extra.BackendRef)
		if jerr != nil {
			return vmspec.Resolved{}, nil, nil, convert, errConflict("volume locator is invalid")
		}
		resolved.Disks = append(resolved.Disks, vmspec.ResolvedDisk{
			VolumeID: extra.ID, Role: vmspec.DiskRoleData, Slot: d.Slot, Path: epath,
			Format: storage.QEMUFormat(extra.BackendType, firstNonEmpty(extra.Format, d.Format)), ReadOnly: d.ReadOnly, PCIAddr: d.PCIAddr,
		})
	}
	for i, n := range spec.NICs {
		resolved.NICs = append(resolved.NICs, vmspec.ResolvedNIC{
			ID: n.ID, NetworkID: n.NetworkID, BridgeName: bridge, MAC: n.MAC, Model: n.Model, PCIAddr: n.PCIAddr,
			TAPName: vmspec.TAPName(ids.WorkloadID, i),
		})
	}
	if spec.CloudImageID != "" {
		lib, lerr := s.Store.GetLibraryItem(ctx, clusterID, spec.CloudImageID)
		if lerr != nil || lib == nil {
			return vmspec.Resolved{}, nil, nil, convert, errNotFound("cloud image is not found")
		}
		if lib.Kind != storage.LibraryCloudImage {
			return vmspec.Resolved{}, nil, nil, convert, errConflict("library item is not a cloud image")
		}
		if lib.Status != storage.StatusAvailable {
			return vmspec.Resolved{}, nil, nil, convert, errConflict("cloud image is unavailable")
		}
		srcPool, perr := s.Store.GetStoragePool(ctx, clusterID, lib.PoolID)
		if perr != nil || srcPool == nil {
			return vmspec.Resolved{}, nil, nil, convert, errConflict("cloud image storage is unavailable")
		}
		src, jerr := storage.JoinUnder(srcPool.RootPath, lib.BackendRef)
		if jerr != nil {
			return vmspec.Resolved{}, nil, nil, convert, errConflict("cloud image locator is invalid")
		}
		convert = qemu.ConvertRequest{SourcePath: src, SourceFormat: "qcow2", DestPath: diskPath, DestFormat: storage.QEMUFormat(vol.BackendType, vol.Format)}
	}
	if spec.ISOLibraryID != "" {
		lib, lerr := s.Store.GetLibraryItem(ctx, clusterID, spec.ISOLibraryID)
		if lerr != nil || lib == nil {
			return vmspec.Resolved{}, nil, nil, convert, errNotFound("installation media is not found")
		}
		if lib.Kind != storage.LibraryISO {
			return vmspec.Resolved{}, nil, nil, convert, errConflict("library item is not installation media")
		}
		isoPool, perr := s.Store.GetStoragePool(ctx, clusterID, lib.PoolID)
		if perr != nil || isoPool == nil {
			return vmspec.Resolved{}, nil, nil, convert, errConflict("installation media storage is unavailable")
		}
		isoPath, jerr := storage.JoinUnder(isoPool.RootPath, lib.BackendRef)
		if jerr != nil {
			return vmspec.Resolved{}, nil, nil, convert, errConflict("iso locator is invalid")
		}
		resolved.ISOPath = isoPath
	}
	if spec.Firmware == vmspec.FirmwareUEFI {
		code, ferr := firmwareCodeForSpec(spec)
		if ferr != nil {
			return vmspec.Resolved{}, nil, nil, convert, ferr
		}
		resolved.FirmwareCode = code
	}
	_ = pool
	return resolved, vol, netw, convert, nil
}

func (s *Server) ensureVMBootVolume(ctx context.Context, clusterID, nodeID string, pool *appdb.StoragePool, volumeID string, spec vmspec.Spec, mustExist bool) (*appdb.Volume, string, error) {
	size := int64(vmspec.DefaultDiskBytes)
	format := storage.FormatQCOW2
	for _, d := range spec.Disks {
		if d.Role == vmspec.DiskRoleBoot {
			if d.VolumeID != "" {
				volumeID = d.VolumeID
			}
			if d.SizeBytes > 0 {
				size = d.SizeBytes
			}
			if d.Format != "" {
				format = d.Format
			}
		}
	}
	if volumeID == "" {
		volumeID = uuid.NewString()
	}
	existing, err := s.Store.GetVolume(ctx, clusterID, volumeID)
	if err != nil {
		return nil, "", err
	}
	if existing == nil && mustExist {
		return nil, "", errNotFound("volume is not found")
	}
	if existing != nil {
		if existing.Class != storage.ClassVMDisk {
			return nil, "", errConflict("volume is not a vm-disk")
		}
		p, err := s.Store.GetStoragePool(ctx, clusterID, existing.PoolID)
		if err != nil || p == nil {
			return nil, "", errConflict("storage pool is not found")
		}
		diskPath, err := storage.HostVolumePath(p.BackendType, p.RootPath, existing.BackendRef)
		if err != nil {
			return nil, "", errConflict("volume locator is invalid")
		}
		return existing, diskPath, nil
	}
	if pool.BackendType == storage.BackendZFS {
		row, err := s.createZFSVolume(ctx, clusterID, *pool, storage.ClassVMDisk, size)
		if err != nil {
			return nil, "", err
		}
		diskPath, err := storage.HostVolumePath(pool.BackendType, pool.RootPath, row.BackendRef)
		if err != nil {
			return nil, "", errConflict("volume locator is invalid")
		}
		return &row, diskPath, nil
	}
	if pool.BackendType == storage.BackendLVM {
		row, err := s.createLVMVolume(ctx, clusterID, *pool, storage.ClassVMDisk, size)
		if err != nil {
			return nil, "", err
		}
		diskPath, err := storage.HostVolumePath(pool.BackendType, pool.RootPath, row.BackendRef)
		if err != nil {
			return nil, "", errConflict("volume locator is invalid")
		}
		return &row, diskPath, nil
	}
	if pool.BackendType == storage.BackendISCSI {
		row, err := s.createISCSIVolume(ctx, clusterID, *pool, storage.ClassVMDisk, size)
		if err != nil {
			return nil, "", err
		}
		diskPath, err := storage.HostVolumePath(pool.BackendType, pool.RootPath, row.BackendRef)
		if err != nil {
			return nil, "", errConflict("volume locator is invalid")
		}
		return &row, diskPath, nil
	}
	hint := appdb.PoolHints([]appdb.StoragePool{*pool})[0]
	res, err := s.Storage.CreateDirectoryVolume(ctx, storage.CreateVolumeRequest{
		VolumeID: volumeID, PoolID: pool.ID, RootPath: pool.RootPath,
		Class: storage.ClassVMDisk, Size: size, Format: format,
	}, hint)
	if err != nil && !errors.Is(err, storage.ErrDuplicate) {
		return nil, "", err
	}
	backend := res.Handle.BackendRef
	if backend == "" {
		backend = path.Join("volumes", storage.ClassVMDisk, volumeID+".qcow2")
	}
	row := appdb.Volume{
		ID: volumeID, ClusterID: clusterID, NodeID: nodeID, PoolID: pool.ID,
		Class: storage.ClassVMDisk, Kind: storage.KindBlock, Format: format, SizeBytes: size,
		Status: storage.StatusAvailable, BackendType: storage.BackendDirectory, BackendRef: backend,
	}
	if err := s.Store.CreateVolume(ctx, row); err != nil {
		existing, gerr := s.Store.GetVolume(ctx, clusterID, volumeID)
		if gerr != nil {
			return nil, "", gerr
		}
		if existing != nil {
			row = *existing
		} else {
			return nil, "", err
		}
	}
	diskPath, err := storage.JoinUnder(pool.RootPath, row.BackendRef)
	if err != nil {
		return nil, "", errConflict("volume locator is invalid")
	}
	return &row, diskPath, nil
}

func (s *Server) vmLifecycle(w http.ResponseWriter, r *http.Request, p *principal, row appdb.Workload, action string) {
	perm := rbac.ComputeLifecycle
	switch action {
	case "start":
		perm = rbac.ComputeStart
	case "stop", "force-stop":
		perm = rbac.ComputeStop
	case "delete":
		perm = rbac.ComputeDelete
	case "clone":
		s.cloneVM(w, r, p, row)
		return
	}
	if action == "delete" {
		if !rbac.Authorize(p.Grants, rbac.ComputeDelete) {
			writeErr(w, http.StatusForbidden, "forbidden")
			return
		}
	} else if !rbac.Authorize(p.Grants, perm) && !rbac.Authorize(p.Grants, rbac.ComputeLifecycle) {
		writeErr(w, http.StatusForbidden, "forbidden")
		return
	}
	if !s.guardLocalApply(w, r, p.User.ClusterID, firstNonEmpty(row.DesiredNodeID, row.NodeID), action) {
		return
	}
	if s.VM == nil {
		writeErr(w, http.StatusBadGateway, "vm agent is unavailable")
		return
	}
	if action == "delete" {
		if strings.TrimSpace(r.Header.Get(confirmHeader)) != vmDeleteConfirm {
			writeErr(w, http.StatusConflict, "deleting a VM requires X-Nodal-Confirm: delete. Attached volumes are preserved.")
			return
		}
	}
	if action == "start" {
		if err := s.ensureVMStorageAvailable(r.Context(), p.User.ClusterID, row.ID); err != nil {
			_ = s.Store.UpdateWorkloadObserved(r.Context(), appdb.Workload{ID: row.ID, Status: qemu.StatusUnavailable, Reason: err.Error()})
			writeErr(w, http.StatusConflict, err.Error())
			return
		}
	}
	op := s.startOp(r.Context(), p.User.ClusterID, row.NodeID, "vm."+strings.ReplaceAll(action, "-", "_"), action, 40)
	agentAction := action
	if action == "force-stop" {
		agentAction = "force-stop"
	}
	if action == "delete" {
		if err := s.releaseWorkloadClaims(r.Context(), p.User.ClusterID, row.ID); err != nil {
			s.finishOp(r.Context(), op, "failed", err.Error(), 0)
			writeErr(w, statusFor(err), err.Error())
			return
		}
		_, _ = s.VM.LifecycleVM(r.Context(), row.ID, "stop", false)
		if _, err := s.VM.LifecycleVM(r.Context(), row.ID, "delete-runtime", false); err != nil {
			s.finishOp(r.Context(), op, "failed", err.Error(), 0)
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		if err := s.Store.DeleteWorkload(r.Context(), p.User.ClusterID, row.ID); err != nil {
			s.finishOp(r.Context(), op, "failed", err.Error(), 0)
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		s.finishOp(r.Context(), op, "succeeded", "deleted", 100)
		s.audit(r, p.User.ClusterID, p.User.ID, "vm.delete", "ok", row.ID)
		s.emitEvent(r.Context(), p.User.ClusterID, row.NodeID, "vm.deleted", map[string]string{"workload_id": row.ID})
		writeJSON(w, http.StatusOK, map[string]any{"id": row.ID, "deleted": true, "volumes_preserved": true})
		return
	}
	spec, _ := vmspec.Parse(row.SpecJSON)
	if action == "restart" {
		if _, err := s.VM.LifecycleVM(r.Context(), row.ID, "stop", spec.Autostart); err != nil {
			s.finishOp(r.Context(), op, "failed", err.Error(), 0)
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		if _, err := s.reprepareVM(r.Context(), p.User.ClusterID, row); err != nil {
			s.finishOp(r.Context(), op, "failed", err.Error(), 0)
			writeErr(w, statusFor(err), err.Error())
			return
		}
		agentAction = "start"
	}
	if action == "start" {
		running := row.UnitActive || row.Status == qemu.StatusRunning || row.Status == qemu.StatusStarting
		if !running {
			if _, err := s.reprepareVM(r.Context(), p.User.ClusterID, row); err != nil {
				s.finishOp(r.Context(), op, "failed", err.Error(), 0)
				writeErr(w, statusFor(err), err.Error())
				return
			}
		}
	}
	obs, err := s.VM.LifecycleVM(r.Context(), row.ID, agentAction, spec.Autostart)
	if err != nil {
		s.finishOp(r.Context(), op, "failed", err.Error(), 0)
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if latest, _ := s.Store.GetWorkload(r.Context(), p.User.ClusterID, row.ID); latest != nil {
		row = *latest
	}
	desired := row.DesiredPower
	switch action {
	case "start", "restart":
		desired = "running"
	case "stop", "force-stop":
		desired = "stopped"
	}
	_ = s.Store.UpdateWorkloadSpec(r.Context(), appdb.Workload{
		ID: row.ID, CPUs: row.CPUs, MemoryBytes: row.MemoryBytes, DesiredPower: desired,
		SpecJSON: row.SpecJSON, AppliedJSON: row.AppliedJSON, Autostart: spec.Autostart,
		PendingRestart: false, Firmware: row.Firmware,
	})
	_ = s.Store.UpdateWorkloadObserved(r.Context(), appdb.Workload{
		ID: row.ID, Status: firstNonEmpty(obs.Status, row.Status), Reason: obs.Reason, UnitActive: obs.UnitActive,
	})
	s.finishOp(r.Context(), op, "succeeded", action, 100)
	s.audit(r, p.User.ClusterID, p.User.ID, "vm."+strings.ReplaceAll(action, "-", "_"), "ok", row.ID)
	evt := "vm.updated"
	switch action {
	case "start":
		evt = "vm.started"
	case "stop", "force-stop":
		evt = "vm.stopped"
	}
	s.emitEvent(r.Context(), p.User.ClusterID, row.NodeID, evt, map[string]string{"workload_id": row.ID})
	s.refreshWorkloads(r.Context(), p.User.ClusterID)
	updated, _ := s.Store.GetWorkload(r.Context(), p.User.ClusterID, row.ID)
	if updated == nil {
		updated = &row
	}
	writeJSON(w, http.StatusOK, s.workloadJSON(r.Context(), *updated))
}

func (s *Server) patchVM(w http.ResponseWriter, r *http.Request, p *principal, row appdb.Workload, req patchWorkloadRequest) {
	if patchSpecChange(req) && !rbac.Authorize(p.Grants, rbac.ComputeModify) {
		writeErr(w, http.StatusForbidden, "forbidden")
		return
	}
	if req.DesiredPower != "" && !rbac.Authorize(p.Grants, rbac.ComputeLifecycle) {
		writeErr(w, http.StatusForbidden, "forbidden")
		return
	}
	prev, err := vmspec.Parse(row.SpecJSON)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	next := prev
	if req.Name != "" {
		next.Name = req.Name
	}
	if req.CPUs > 0 {
		next.CPUs = req.CPUs
	}
	if req.MemoryBytes > 0 {
		next.MemoryBytes = req.MemoryBytes
	}
	if req.Firmware != "" {
		next.Firmware = req.Firmware
	}
	if req.Autostart != nil {
		next.Autostart = *req.Autostart
	}
	if req.ISOLibraryID != nil {
		next.ISOLibraryID = *req.ISOLibraryID
	}
	if req.NoCloud != nil {
		next.NoCloud = *req.NoCloud
		next.NoCloud.Enable = true
		if req.NoCloud.Password == "" {
			next.NoCloud.HasPassword = prev.NoCloud.HasPassword
		}
	}
	next = vmspec.Normalize(next)
	next = vmspec.PersistNICs(row.ID, next)
	classes := vmspec.ClassifyEdit(prev, next)
	if vmspec.HasUnsupported(classes) {
		writeErr(w, http.StatusUnprocessableEntity, "this spec change is not supported in Phase 8")
		return
	}
	running := row.UnitActive || row.Status == qemu.StatusRunning || row.Status == qemu.StatusStarting
	if running && vmspec.RequiresStop(classes) {
		writeErr(w, http.StatusConflict, "this spec change requires the VM to be stopped")
		return
	}
	if err := vmspec.Validate(next); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	pending := running && vmspec.RequiresRestart(classes)
	desired := row.DesiredPower
	if req.DesiredPower != "" {
		desired = req.DesiredPower
	}
	if req.NoCloud != nil && s.VM != nil {
		if seed, serr := nocloudSeedForWrite(next); serr != nil {
			writeErr(w, http.StatusBadRequest, serr.Error())
			return
		} else if seed != "" {
			appliedLaunch, lerr := jsonToLaunch(row.AppliedJSON)
			if lerr != nil {
				writeErr(w, http.StatusBadRequest, "frozen launch config is invalid")
				return
			}
			if appliedLaunch.WorkloadID == "" {
				appliedLaunch.WorkloadID = row.ID
			}
			if _, perr := s.VM.PrepareVM(r.Context(), agentrpc.VMPrepareRequest{Launch: appliedLaunch, UserData: seed}); perr != nil && !errors.Is(perr, qemu.ErrAlreadyRunning) {
				writeErr(w, statusFor(perr), perr.Error())
				return
			}
		}
	}
	if err := s.Store.UpdateWorkloadSpec(r.Context(), appdb.Workload{
		ID: row.ID, CPUs: next.CPUs, MemoryBytes: next.MemoryBytes, DesiredPower: desired,
		SpecJSON: vmspec.MustJSON(next), AppliedJSON: row.AppliedJSON, Autostart: next.Autostart,
		PendingRestart: pending, Firmware: next.Firmware,
	}); err != nil {
		writeErr(w, http.StatusInternalServerError, "could not record VM spec")
		return
	}
	if req.Autostart != nil && s.VM != nil {
		_, _ = s.VM.LifecycleVM(r.Context(), row.ID, "autostart", next.Autostart)
	}
	s.audit(r, p.User.ClusterID, p.User.ID, "vm.update", "ok", row.ID)
	s.emitEvent(r.Context(), p.User.ClusterID, row.NodeID, "vm.updated", map[string]string{"workload_id": row.ID})
	updated, _ := s.Store.GetWorkload(r.Context(), p.User.ClusterID, row.ID)
	if updated == nil {
		updated = &row
	}
	out := s.workloadJSON(r.Context(), *updated)
	out["apply"] = classes
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) ensureVMStorageAvailable(ctx context.Context, clusterID, workloadID string) error {
	disks, err := s.Store.ListWorkloadDisks(ctx, clusterID, workloadID)
	if err != nil || len(disks) == 0 {
		return errConflict("VM storage is unavailable")
	}
	for _, d := range disks {
		if d.VolumeID == "" {
			continue
		}
		vol, err := s.Store.GetVolume(ctx, clusterID, d.VolumeID)
		if err != nil || vol == nil {
			return errConflict("storage is unavailable")
		}
		if vol.Status != storage.StatusAvailable && vol.Status != storage.StatusWarning {
			return errConflict("storage is unavailable")
		}
		pool, err := s.Store.GetStoragePool(ctx, clusterID, vol.PoolID)
		if err != nil || pool == nil || (pool.Status != storage.StatusAvailable && pool.Status != storage.StatusWarning) {
			return errConflict("storage is unavailable")
		}
	}
	return nil
}

func (s *Server) reprepareVM(ctx context.Context, clusterID string, row appdb.Workload) (vmspec.Launch, error) {
	spec, err := vmspec.Parse(row.SpecJSON)
	if err != nil {
		return vmspec.Launch{}, err
	}
	spec = vmspec.PersistNICs(row.ID, spec)
	spec, _, err = vmspec.AllocatePCI(spec)
	if err != nil {
		return vmspec.Launch{}, err
	}
	if err := vmspec.Validate(spec); err != nil {
		return vmspec.Launch{}, err
	}
	resolved, err := s.resolveStoredVM(ctx, clusterID, row, spec)
	if err != nil {
		return vmspec.Launch{}, err
	}
	launch, err := vmspec.Compile(row.ID, spec, resolved)
	if err != nil {
		return vmspec.Launch{}, err
	}
	userData, err := nocloudSeedForReprepare(spec)
	if err != nil {
		return vmspec.Launch{}, err
	}
	if _, err := s.VM.PrepareVM(ctx, agentrpc.VMPrepareRequest{Launch: launch, UserData: userData}); err != nil {
		return vmspec.Launch{}, err
	}
	applied, _ := json.Marshal(launch)
	_ = s.Store.UpdateWorkloadSpec(ctx, appdb.Workload{
		ID: row.ID, CPUs: spec.CPUs, MemoryBytes: spec.MemoryBytes, DesiredPower: row.DesiredPower,
		SpecJSON: vmspec.MustJSON(spec), AppliedJSON: applied, Autostart: spec.Autostart,
		PendingRestart: false, Firmware: spec.Firmware,
	})
	return launch, nil
}

func (s *Server) resolveStoredVM(ctx context.Context, clusterID string, row appdb.Workload, spec vmspec.Spec) (vmspec.Resolved, error) {
	disks, err := s.Store.ListWorkloadDisks(ctx, clusterID, row.ID)
	if err != nil || len(disks) == 0 {
		return vmspec.Resolved{}, errConflict("VM storage is unavailable")
	}
	netID := ""
	if len(spec.NICs) > 0 {
		netID = spec.NICs[0].NetworkID
	}
	netw, err := s.Store.GetNetwork(ctx, clusterID, netID)
	if err != nil || netw == nil {
		return vmspec.Resolved{}, errNotFound("network is not found")
	}
	bridge := netw.BridgeName
	if bridge == "" {
		bridge, err = ndnet.BridgeName(netw.ID)
		if err != nil {
			return vmspec.Resolved{}, err
		}
	}
	resolved := vmspec.Resolved{Accel: qemu.DetectAccel()}
	addVol := func(volumeID, role string, slot int, pci string, readOnly bool, format string) error {
		vol, err := s.Store.GetVolume(ctx, clusterID, volumeID)
		if err != nil || vol == nil {
			return errConflict("storage is unavailable")
		}
		if vol.Status != storage.StatusAvailable && vol.Status != storage.StatusWarning {
			return errConflict("storage is unavailable")
		}
		pool, err := s.Store.GetStoragePool(ctx, clusterID, vol.PoolID)
		if err != nil || pool == nil || (pool.Status != storage.StatusAvailable && pool.Status != storage.StatusWarning) {
			return errConflict("storage is unavailable")
		}
		diskPath, err := storage.HostVolumePath(pool.BackendType, pool.RootPath, vol.BackendRef)
		if err != nil {
			return errConflict("volume locator is invalid")
		}
		resolved.Disks = append(resolved.Disks, vmspec.ResolvedDisk{
			VolumeID: vol.ID, Role: role, Slot: slot, Path: diskPath,
			Format: storage.QEMUFormat(vol.BackendType, firstNonEmpty(format, vol.Format)), ReadOnly: readOnly, PCIAddr: pci,
		})
		return nil
	}
	bootDone := false
	for _, d := range spec.Disks {
		if d.Role == vmspec.DiskRoleCDROM || d.Role == vmspec.DiskRoleCIDATA || d.Role == vmspec.DiskRoleVars {
			continue
		}
		volID := d.VolumeID
		if volID == "" && d.Role == vmspec.DiskRoleBoot {
			for _, rowd := range disks {
				if rowd.Role == vmspec.DiskRoleBoot {
					volID = rowd.VolumeID
					if d.PCIAddr == "" {
						d.PCIAddr = rowd.BusAddr
					}
					break
				}
			}
		}
		if volID == "" {
			continue
		}
		if err := addVol(volID, d.Role, d.Slot, d.PCIAddr, d.ReadOnly, d.Format); err != nil {
			return vmspec.Resolved{}, err
		}
		if d.Role == vmspec.DiskRoleBoot {
			bootDone = true
		}
	}
	if !bootDone {
		for _, d := range disks {
			if d.Role != vmspec.DiskRoleBoot {
				continue
			}
			if err := addVol(d.VolumeID, d.Role, d.Slot, d.BusAddr, d.ReadOnly, d.Format); err != nil {
				return vmspec.Resolved{}, err
			}
		}
	}
	for i, n := range spec.NICs {
		resolved.NICs = append(resolved.NICs, vmspec.ResolvedNIC{
			ID: n.ID, NetworkID: n.NetworkID, BridgeName: bridge, MAC: n.MAC, Model: n.Model, PCIAddr: n.PCIAddr,
			TAPName: vmspec.TAPName(row.ID, i),
		})
	}
	if spec.ISOLibraryID != "" {
		lib, lerr := s.Store.GetLibraryItem(ctx, clusterID, spec.ISOLibraryID)
		if lerr != nil || lib == nil {
			return vmspec.Resolved{}, errNotFound("installation media is not found")
		}
		if lib.Kind != storage.LibraryISO {
			return vmspec.Resolved{}, errConflict("library item is not installation media")
		}
		isoPool, perr := s.Store.GetStoragePool(ctx, clusterID, lib.PoolID)
		if perr != nil || isoPool == nil {
			return vmspec.Resolved{}, errConflict("installation media storage is unavailable")
		}
		isoPath, jerr := storage.JoinUnder(isoPool.RootPath, lib.BackendRef)
		if jerr != nil {
			return vmspec.Resolved{}, errConflict("iso locator is invalid")
		}
		resolved.ISOPath = isoPath
	}
	if spec.Firmware == vmspec.FirmwareUEFI {
		code, ferr := firmwareCodeForSpec(spec)
		if ferr != nil {
			return vmspec.Resolved{}, ferr
		}
		resolved.FirmwareCode = code
	}
	return resolved, nil
}

func (s *Server) createVMConsole(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.ComputeConsole)
	if err != nil {
		return
	}
	row, err := s.Store.GetWorkload(r.Context(), p.User.ClusterID, r.PathValue("id"))
	if err != nil || row == nil || row.Kind != vmspec.KindVM {
		writeErr(w, http.StatusNotFound, "workload not found")
		return
	}
	var req createTermRequest
	if r.ContentLength > 0 {
		_ = readJSON(r, &req)
	}
	mode := strings.ToLower(strings.TrimSpace(req.Mode))
	if mode == "" {
		mode = "serial"
	}
	if mode != "serial" && mode != "vnc" {
		writeErr(w, http.StatusBadRequest, "console mode must be serial or vnc")
		return
	}
	sock, err := (&qemu.Engine{}).ConsoleSocket(row.ID, mode)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	ticket, err := secutil.RandomHex(24)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	sess := appdb.IOSession{
		ID: uuid.NewString(), ClusterID: p.User.ClusterID, UserID: p.User.ID,
		TargetKind: appdb.IOTargetVM, TargetID: row.ID, Kind: appdb.IOKindConsole, CWD: mode,
		TicketHash: secutil.HashSHA256(ticket), State: appdb.IOStatePending,
		ExpiresAt: s.now().Add(ioTicketTTL),
	}
	if err := s.Store.CreateIOSession(r.Context(), sess); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.audit(r, p.User.ClusterID, p.User.ID, "compute.console", "ok", sess.ID)
	writeJSON(w, http.StatusCreated, map[string]any{
		"id": sess.ID, "target_kind": sess.TargetKind, "target_id": sess.TargetID,
		"kind": sess.Kind, "mode": mode, "state": sess.State,
		"expires_at": sess.ExpiresAt.UTC().Format(time.RFC3339),
		"ticket":     ticket,
		"ws_path":    "/api/v1/io/sessions/" + sess.ID + "/ws",
		"backend":    "unix",
		"note":       "backend socket paths are locators, not credentials",
	})
	_ = sock
}

// nocloudSeedForWrite returns user-data only when the spec still carries a
// password or raw user-data. Persisted specs are redacted, so start/restart
// must not overwrite the private cidata seed with a password-less render.
func nocloudSeedForWrite(spec vmspec.Spec) (string, error) {
	if spec.NoCloud.Password == "" && strings.TrimSpace(spec.NoCloud.UserData) == "" {
		return "", nil
	}
	return vmspec.RenderUserData(spec.NoCloud)
}

func nocloudSeedForReprepare(spec vmspec.Spec) (string, error) {
	return nocloudSeedForWrite(spec)
}

func jsonToLaunch(raw json.RawMessage) (vmspec.Launch, error) {
	var launch vmspec.Launch
	if len(raw) == 0 {
		return launch, nil
	}
	if err := json.Unmarshal(raw, &launch); err != nil {
		return vmspec.Launch{}, err
	}
	return launch, nil
}
