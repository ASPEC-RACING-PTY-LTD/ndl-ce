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
	"github.com/no-dal/ndl-ce/internal/appdb"
	"github.com/no-dal/ndl-ce/internal/lxc"
	"github.com/no-dal/ndl-ce/internal/ndnet"
	"github.com/no-dal/ndl-ce/internal/rbac"
	"github.com/no-dal/ndl-ce/internal/storage"
	"github.com/no-dal/ndl-ce/internal/vmspec"
)

// WorkloadRPC is the privileged agent surface for system containers.
type WorkloadRPC interface {
	CreateCT(ctx context.Context, spec lxc.Spec) (lxc.Result, error)
	LifecycleCT(ctx context.Context, req lxc.LifecycleRequest) (lxc.Result, error)
	GetWorkloads(ctx context.Context, hints []lxc.Hint) (lxc.Observation, error)
}

type createWorkloadRequest struct {
	Name         string          `json:"name"`
	Kind         string          `json:"kind"`
	ImagePin     string          `json:"image_pin"`
	CPUs         int             `json:"cpus"`
	MemoryBytes  int64           `json:"memory_bytes"`
	PoolID       string          `json:"pool_id"`
	NetworkID    string          `json:"network_id"`
	VolumeID     string          `json:"volume_id"`
	Privileged   bool            `json:"privileged"`
	DesiredPower string          `json:"desired_power"`
	Firmware     string          `json:"firmware"`
	Autostart    bool            `json:"autostart"`
	Balloon      bool            `json:"balloon"`
	ISOLibraryID string          `json:"iso_library_id"`
	CloudImageID string          `json:"cloud_image_id"`
	NoCloud      vmspec.NoCloud  `json:"nocloud"`
	Spec         json.RawMessage `json:"spec"`
	QEMUArgs     []string        `json:"qemu_args"`
	Command      string          `json:"command"`
}

type patchWorkloadRequest struct {
	Name         string          `json:"name"`
	CPUs         int             `json:"cpus"`
	MemoryBytes  int64           `json:"memory_bytes"`
	DesiredPower string          `json:"desired_power"`
	Firmware     string          `json:"firmware"`
	Autostart    *bool           `json:"autostart"`
	ISOLibraryID *string         `json:"iso_library_id"`
	NoCloud      *vmspec.NoCloud `json:"nocloud"`
}

type cloneWorkloadRequest struct {
	Name string `json:"name"`
}

type createIDs struct {
	WorkloadID string `json:"workload_id"`
	VolumeID   string `json:"volume_id"`
}

func (s *Server) listWorkloads(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.ComputeRead)
	if err != nil {
		return
	}
	s.refreshWorkloads(r.Context(), p.User.ClusterID)
	items, err := s.Store.ListWorkloads(r.Context(), p.User.ClusterID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := make([]map[string]any, 0, len(items))
	for _, item := range items {
		out = append(out, s.workloadJSON(r.Context(), item))
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": out, "image_pins": lxc.AllowedPins})
}

func (s *Server) getWorkload(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.ComputeRead)
	if err != nil {
		return
	}
	s.refreshWorkloads(r.Context(), p.User.ClusterID)
	row, err := s.Store.GetWorkload(r.Context(), p.User.ClusterID, r.PathValue("id"))
	if err != nil || row == nil {
		writeErr(w, http.StatusNotFound, "workload not found")
		return
	}
	writeJSON(w, http.StatusOK, s.workloadJSON(r.Context(), *row))
}

func (s *Server) createWorkload(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.ComputeCreate)
	if err != nil {
		return
	}
	var req createWorkloadRequest
	if err := readJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request")
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	req.Kind = normalizeKind(req.Kind)
	req.ImagePin = strings.TrimSpace(req.ImagePin)
	if len(req.QEMUArgs) > 0 || strings.TrimSpace(req.Command) != "" {
		writeErr(w, http.StatusBadRequest, "raw QEMU arguments are not allowed")
		return
	}
	if req.Kind == vmspec.KindVM {
		s.createVM(w, r, p, req)
		return
	}
	if req.Name == "" || req.ImagePin == "" {
		writeErr(w, http.StatusBadRequest, "name and image_pin are required")
		return
	}
	if req.Kind != lxc.KindSystemContainer {
		writeErr(w, http.StatusBadRequest, "kind must be system-container")
		return
	}
	if err := lxc.ValidatePin(req.ImagePin); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if req.Privileged && !hasRole(p, rbac.Admin) {
		s.audit(r, p.User.ClusterID, p.User.ID, "workload.create.privileged", "denied", "operator cannot create privileged containers")
		writeErr(w, http.StatusForbidden, "only admin may create privileged containers")
		return
	}
	node, err := s.Store.GetNode(r.Context(), p.User.ClusterID)
	if err != nil || node == nil {
		writeErr(w, http.StatusFailedDependency, "local node is not enrolled")
		return
	}
	if s.Workloads == nil || s.Storage == nil {
		writeErr(w, http.StatusBadGateway, "workload agent is unavailable")
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
	ids := s.planCreateIDs(r.Context(), p.User.ClusterID, node.ID, key, req.VolumeID)
	pool, netw, rootfs, volRow, err := s.prepareRoot(r.Context(), p.User.ClusterID, node.ID, req, ids.VolumeID)
	if err != nil {
		writeErr(w, statusFor(err), err.Error())
		return
	}
	if req.CPUs < 1 {
		req.CPUs = lxc.DefaultCPUs
	}
	if req.MemoryBytes < 1 {
		req.MemoryBytes = lxc.DefaultMemoryBytes
	}
	if req.DesiredPower == "" {
		req.DesiredPower = "running"
	}
	mac := lxc.MACFromUUID(ids.WorkloadID)
	op := s.startOpKeyed(r.Context(), p.User.ClusterID, node.ID, "workload.create", "creating", key, mustCreateMsg(ids), 20)
	if req.Privileged {
		s.audit(r, p.User.ClusterID, p.User.ID, "workload.create.privileged", "ok", ids.WorkloadID)
	}
	res, err := s.Workloads.CreateCT(r.Context(), lxc.Spec{
		WorkloadID: ids.WorkloadID, Name: req.Name, ImagePin: req.ImagePin,
		CPUs: req.CPUs, MemoryBytes: req.MemoryBytes, VolumeID: ids.VolumeID,
		RootfsPath: rootfs, NetworkID: netw.ID, BridgeName: netw.BridgeName,
		MAC: mac, Privileged: req.Privileged, UIDMap: lxc.DefaultUIDMap, GIDMap: lxc.DefaultGIDMap,
	})
	if err != nil {
		s.finishOp(r.Context(), op, "failed", mustCreateMsg(ids), 0)
		s.audit(r, p.User.ClusterID, p.User.ID, "workload.create", "denied", err.Error())
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if existing, _ := s.Store.GetWorkload(r.Context(), p.User.ClusterID, ids.WorkloadID); existing != nil {
		s.finishOp(r.Context(), op, "succeeded", mustCreateMsg(ids), 100)
		writeJSON(w, http.StatusOK, s.workloadJSON(r.Context(), *existing))
		return
	}
	row := appdb.Workload{
		ID: ids.WorkloadID, ClusterID: p.User.ClusterID, NodeID: node.ID,
		OwnerNodeID: node.ID, DesiredNodeID: node.ID, Name: req.Name, Kind: lxc.KindSystemContainer,
		Status: res.Status, DesiredPower: req.DesiredPower, ImagePin: req.ImagePin,
		ImageVerified: res.ImageVerified, CPUs: req.CPUs, MemoryBytes: req.MemoryBytes,
		Privileged: req.Privileged, UIDMap: lxc.DefaultUIDMap, GIDMap: lxc.DefaultGIDMap,
		Devices: json.RawMessage(`[]`), MigrateBlockers: json.RawMessage(`["offline migrate is Phase 32"]`),
		IdempotencyKey: key,
	}
	if err := s.Store.CreateWorkload(r.Context(), row); err != nil {
		if existing, _ := s.Store.GetWorkloadByName(r.Context(), p.User.ClusterID, req.Name); existing != nil {
			s.finishOp(r.Context(), op, "succeeded", mustCreateMsg(ids), 100)
			writeJSON(w, http.StatusOK, s.workloadJSON(r.Context(), *existing))
			return
		}
		s.finishOp(r.Context(), op, "failed", err.Error(), 0)
		writeErr(w, http.StatusConflict, "could not record workload")
		return
	}
	if volRow != nil {
		_ = s.Store.CreateWorkloadDisk(r.Context(), appdb.WorkloadDisk{
			ID: uuid.NewString(), ClusterID: p.User.ClusterID, WorkloadID: row.ID, VolumeID: volRow.ID, Role: "root",
		})
	}
	_ = s.Store.CreateWorkloadNIC(r.Context(), appdb.WorkloadNIC{
		ID: uuid.NewString(), ClusterID: p.User.ClusterID, WorkloadID: row.ID,
		NetworkID: netw.ID, MAC: firstNonEmpty(res.MAC, mac),
	})
	_ = pool
	s.finishOp(r.Context(), op, "succeeded", mustCreateMsg(ids), 100)
	s.audit(r, p.User.ClusterID, p.User.ID, "workload.create", "ok", row.ID)
	s.emitEvent(r.Context(), p.User.ClusterID, node.ID, "workload.created", map[string]string{"workload_id": row.ID, "kind": row.Kind})
	writeJSON(w, http.StatusCreated, s.workloadJSON(r.Context(), row))
}

func (s *Server) planCreateIDs(ctx context.Context, clusterID, nodeID, key, volumeID string) createIDs {
	ids := createIDs{WorkloadID: uuid.NewString(), VolumeID: strings.TrimSpace(volumeID)}
	if key != "" {
		if op, _ := s.Store.GetOperationByIdempotency(ctx, clusterID, key); op != nil {
			var prev createIDs
			if json.Unmarshal([]byte(op.Message), &prev) == nil {
				if prev.WorkloadID != "" {
					ids.WorkloadID = prev.WorkloadID
				}
				if prev.VolumeID != "" {
					ids.VolumeID = prev.VolumeID
				}
			}
		}
	}
	if ids.VolumeID == "" {
		ids.VolumeID = uuid.NewString()
	}
	if key != "" {
		_ = s.Store.UpsertOperation(ctx, appdb.Operation{
			ID: uuid.NewString(), ClusterID: clusterID, NodeID: nodeID, Kind: "workload.create",
			State: "running", IdempotencyKey: key, Message: mustCreateMsg(ids), Stage: "planning",
			UpdatedAt: time.Now().UTC(),
		})
	}
	return ids
}

func mustCreateMsg(ids createIDs) string {
	b, _ := json.Marshal(ids)
	return string(b)
}

func (s *Server) prepareRoot(ctx context.Context, clusterID, nodeID string, req createWorkloadRequest, volumeID string) (*appdb.StoragePool, *appdb.Network, string, *appdb.Volume, error) {
	pools, err := s.Store.ListStoragePools(ctx, clusterID)
	if err != nil {
		return nil, nil, "", nil, err
	}
	var pool *appdb.StoragePool
	if req.PoolID != "" {
		pool, err = s.Store.GetStoragePool(ctx, clusterID, req.PoolID)
		if err != nil || pool == nil {
			return nil, nil, "", nil, errNotFound("storage pool is not found")
		}
	} else {
		for i := range pools {
			if pools[i].Status == storage.StatusAvailable || pools[i].Status == storage.StatusWarning {
				cp := pools[i]
				pool = &cp
				break
			}
		}
	}
	if pool == nil || (pool.Status != storage.StatusAvailable && pool.Status != storage.StatusWarning) {
		return nil, nil, "", nil, errConflict("an available storage pool is required")
	}
	netw, err := s.Store.GetNetwork(ctx, clusterID, req.NetworkID)
	if err != nil || netw == nil {
		return nil, nil, "", nil, errNotFound("network is not found")
	}
	if netw.Status != ndnet.StatusAvailable && netw.Status != ndnet.StatusWarning {
		return nil, nil, "", nil, errConflict("an available network is required")
	}
	if existing, _ := s.Store.GetVolume(ctx, clusterID, volumeID); existing != nil {
		return pool, netw, path.Join(pool.RootPath, existing.BackendRef), existing, nil
	}
	hint := appdb.PoolHints([]appdb.StoragePool{*pool})[0]
	res, err := s.Storage.CreateDirectoryVolume(ctx, storage.CreateVolumeRequest{
		VolumeID: volumeID, PoolID: pool.ID, RootPath: pool.RootPath,
		Class: storage.ClassContainerRoot, Size: lxc.DefaultRootSize, Format: storage.FormatDirectory,
	}, hint)
	if err != nil && !errors.Is(err, storage.ErrDuplicate) {
		return nil, nil, "", nil, err
	}
	backend := res.Handle.BackendRef
	if backend == "" {
		backend = path.Join("volumes", storage.ClassContainerRoot, volumeID)
	}
	row := appdb.Volume{
		ID: volumeID, ClusterID: clusterID, NodeID: nodeID, PoolID: pool.ID,
		Class: storage.ClassContainerRoot, Kind: storage.KindFilesystem, Format: storage.FormatDirectory,
		SizeBytes: lxc.DefaultRootSize, Status: storage.StatusAvailable,
		BackendType: storage.BackendDirectory, BackendRef: backend,
	}
	if existing, _ := s.Store.GetVolume(ctx, clusterID, volumeID); existing == nil {
		if err := s.Store.CreateVolume(ctx, row); err != nil {
			if existing, _ = s.Store.GetVolume(ctx, clusterID, volumeID); existing == nil {
				return nil, nil, "", nil, err
			}
			row = *existing
		}
	} else {
		row = *existing
	}
	return pool, netw, path.Join(pool.RootPath, row.BackendRef), &row, nil
}

func (s *Server) lifecycleWorkload(action string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p, err := s.require(w, r, rbac.ComputeLifecycle)
		if err != nil {
			return
		}
		row, err := s.Store.GetWorkload(r.Context(), p.User.ClusterID, r.PathValue("id"))
		if err != nil || row == nil {
			writeErr(w, http.StatusNotFound, "workload not found")
			return
		}
		if row.Kind == vmspec.KindVM {
			s.vmLifecycle(w, r, p, *row, action)
			return
		}
		if s.Workloads == nil {
			writeErr(w, http.StatusBadGateway, "workload agent is unavailable")
			return
		}
		req := lxc.LifecycleRequest{WorkloadID: row.ID, Action: action}
		if action == "clone" {
			if row.Privileged && !hasRole(p, rbac.Admin) {
				s.audit(r, p.User.ClusterID, p.User.ID, "workload.clone.privileged", "denied", "operator cannot clone privileged containers")
				writeErr(w, http.StatusForbidden, "only admin may clone privileged containers")
				return
			}
			var body cloneWorkloadRequest
			_ = readJSON(r, &body)
			clone, err := s.prepareClone(r.Context(), p.User.ClusterID, *row, body.Name)
			if err != nil {
				writeErr(w, statusFor(err), err.Error())
				return
			}
			req = clone
		}
		op := s.startOp(r.Context(), p.User.ClusterID, row.NodeID, "workload."+action, action, 40)
		res, err := s.Workloads.LifecycleCT(r.Context(), req)
		if err != nil {
			s.finishOp(r.Context(), op, "failed", err.Error(), 0)
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		switch action {
		case "start":
			_ = s.Store.UpdateWorkloadSpec(r.Context(), appdb.Workload{ID: row.ID, DesiredPower: "running"})
		case "stop", "delete":
			_ = s.Store.UpdateWorkloadSpec(r.Context(), appdb.Workload{ID: row.ID, DesiredPower: "stopped"})
		case "clone":
			if err := s.recordClone(r.Context(), p.User.ClusterID, *row, req, res); err != nil {
				s.finishOp(r.Context(), op, "failed", err.Error(), 0)
				writeErr(w, http.StatusConflict, err.Error())
				return
			}
			s.finishOp(r.Context(), op, "succeeded", "cloned", 100)
			if row.Privileged {
				s.audit(r, p.User.ClusterID, p.User.ID, "workload.clone.privileged", "ok", req.CloneID)
			}
			s.audit(r, p.User.ClusterID, p.User.ID, "workload.clone", "ok", req.CloneID)
			cloned, _ := s.Store.GetWorkload(r.Context(), p.User.ClusterID, req.CloneID)
			if cloned != nil {
				writeJSON(w, http.StatusCreated, s.workloadJSON(r.Context(), *cloned))
				return
			}
		}
		s.finishOp(r.Context(), op, "succeeded", action, 100)
		s.audit(r, p.User.ClusterID, p.User.ID, "workload."+action, "ok", row.ID)
		s.refreshWorkloads(r.Context(), p.User.ClusterID)
		updated, _ := s.Store.GetWorkload(r.Context(), p.User.ClusterID, row.ID)
		if updated == nil {
			updated = row
		}
		writeJSON(w, http.StatusOK, s.workloadJSON(r.Context(), *updated))
	}
}

func (s *Server) patchWorkload(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.ComputeLifecycle)
	if err != nil {
		return
	}
	row, err := s.Store.GetWorkload(r.Context(), p.User.ClusterID, r.PathValue("id"))
	if err != nil || row == nil {
		writeErr(w, http.StatusNotFound, "workload not found")
		return
	}
	if row.Kind == vmspec.KindVM {
		s.patchVM(w, r, p, *row)
		return
	}
	var req patchWorkloadRequest
	if err := readJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request")
		return
	}
	next := *row
	if req.CPUs > 0 {
		next.CPUs = req.CPUs
	}
	if req.MemoryBytes > 0 {
		next.MemoryBytes = req.MemoryBytes
	}
	if req.DesiredPower != "" {
		next.DesiredPower = req.DesiredPower
	}
	if err := s.Store.UpdateWorkloadSpec(r.Context(), next); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if s.Workloads != nil {
		disks, _ := s.Store.ListWorkloadDisks(r.Context(), p.User.ClusterID, row.ID)
		nics, _ := s.Store.ListWorkloadNICs(r.Context(), p.User.ClusterID, row.ID)
		rootfs := ""
		volID := ""
		if len(disks) > 0 {
			if vol, _ := s.Store.GetVolume(r.Context(), p.User.ClusterID, disks[0].VolumeID); vol != nil {
				if pool, _ := s.Store.GetStoragePool(r.Context(), p.User.ClusterID, vol.PoolID); pool != nil {
					rootfs = path.Join(pool.RootPath, vol.BackendRef)
					volID = vol.ID
				}
			}
		}
		bridge := ""
		mac := ""
		netID := ""
		if len(nics) > 0 {
			mac = nics[0].MAC
			netID = nics[0].NetworkID
			if netw, _ := s.Store.GetNetwork(r.Context(), p.User.ClusterID, nics[0].NetworkID); netw != nil {
				bridge = netw.BridgeName
			}
		}
		_, _ = s.Workloads.CreateCT(r.Context(), lxc.Spec{
			WorkloadID: row.ID, Name: next.Name, ImagePin: next.ImagePin, CPUs: next.CPUs,
			MemoryBytes: next.MemoryBytes, VolumeID: volID, RootfsPath: rootfs, NetworkID: netID,
			BridgeName: bridge, MAC: mac, Privileged: next.Privileged, UIDMap: next.UIDMap, GIDMap: next.GIDMap,
			GPUDevices: s.gpuDeviceNodes(r.Context(), p.User.ClusterID, row.ID),
		})
		if next.DesiredPower == "stopped" {
			_, _ = s.Workloads.LifecycleCT(r.Context(), lxc.LifecycleRequest{WorkloadID: row.ID, Action: "stop"})
		} else if next.DesiredPower == "running" {
			_, _ = s.Workloads.LifecycleCT(r.Context(), lxc.LifecycleRequest{WorkloadID: row.ID, Action: "start"})
		}
	}
	s.audit(r, p.User.ClusterID, p.User.ID, "workload.update", "ok", row.ID)
	s.refreshWorkloads(r.Context(), p.User.ClusterID)
	updated, _ := s.Store.GetWorkload(r.Context(), p.User.ClusterID, row.ID)
	if updated == nil {
		updated = &next
	}
	writeJSON(w, http.StatusOK, s.workloadJSON(r.Context(), *updated))
}

func (s *Server) prepareClone(ctx context.Context, clusterID string, src appdb.Workload, name string) (lxc.LifecycleRequest, error) {
	if s.Storage == nil {
		return lxc.LifecycleRequest{}, errUnavailable("storage agent is unavailable")
	}
	name = strings.TrimSpace(name)
	if name == "" {
		name = src.Name + "-clone"
	}
	disks, err := s.Store.ListWorkloadDisks(ctx, clusterID, src.ID)
	if err != nil || len(disks) == 0 {
		return lxc.LifecycleRequest{}, errConflict("source has no root volume")
	}
	vol, err := s.Store.GetVolume(ctx, clusterID, disks[0].VolumeID)
	if err != nil || vol == nil {
		return lxc.LifecycleRequest{}, errConflict("source volume is unavailable")
	}
	pool, err := s.Store.GetStoragePool(ctx, clusterID, vol.PoolID)
	if err != nil || pool == nil {
		return lxc.LifecycleRequest{}, errConflict("source pool is unavailable")
	}
	nics, _ := s.Store.ListWorkloadNICs(ctx, clusterID, src.ID)
	cloneID := uuid.NewString()
	cloneVol := uuid.NewString()
	hint := appdb.PoolHints([]appdb.StoragePool{*pool})[0]
	res, err := s.Storage.CreateDirectoryVolume(ctx, storage.CreateVolumeRequest{
		VolumeID: cloneVol, PoolID: pool.ID, RootPath: pool.RootPath,
		Class: storage.ClassContainerRoot, Size: lxc.DefaultRootSize, Format: storage.FormatDirectory,
	}, hint)
	if err != nil && !errors.Is(err, storage.ErrDuplicate) {
		return lxc.LifecycleRequest{}, err
	}
	backend := res.Handle.BackendRef
	if backend == "" {
		backend = path.Join("volumes", storage.ClassContainerRoot, cloneVol)
	}
	_ = s.Store.CreateVolume(ctx, appdb.Volume{
		ID: cloneVol, ClusterID: clusterID, NodeID: src.NodeID, PoolID: pool.ID,
		Class: storage.ClassContainerRoot, Kind: storage.KindFilesystem, Format: storage.FormatDirectory,
		SizeBytes: lxc.DefaultRootSize, Status: storage.StatusAvailable,
		BackendType: storage.BackendDirectory, BackendRef: backend,
	})
	req := lxc.LifecycleRequest{
		WorkloadID: src.ID, Action: "clone", CloneID: cloneID, CloneVolumeID: cloneVol,
		CloneRootfsPath: path.Join(pool.RootPath, backend), CloneMAC: lxc.MACFromUUID(cloneID), CloneName: name,
	}
	if len(nics) > 0 {
		_ = nics
	}
	return req, nil
}

func (s *Server) recordClone(ctx context.Context, clusterID string, src appdb.Workload, req lxc.LifecycleRequest, res lxc.Result) error {
	row := src
	row.ID = req.CloneID
	row.Name = req.CloneName
	row.Status = res.Status
	row.DesiredPower = "stopped"
	row.Privileged = src.Privileged
	row.IdempotencyKey = ""
	if err := s.Store.CreateWorkload(ctx, row); err != nil {
		return err
	}
	_ = s.Store.CreateWorkloadDisk(ctx, appdb.WorkloadDisk{
		ID: uuid.NewString(), ClusterID: clusterID, WorkloadID: row.ID, VolumeID: req.CloneVolumeID, Role: "root",
	})
	nics, _ := s.Store.ListWorkloadNICs(ctx, clusterID, src.ID)
	if len(nics) > 0 {
		_ = s.Store.CreateWorkloadNIC(ctx, appdb.WorkloadNIC{
			ID: uuid.NewString(), ClusterID: clusterID, WorkloadID: row.ID,
			NetworkID: nics[0].NetworkID, MAC: firstNonEmpty(res.MAC, req.CloneMAC),
		})
	}
	return nil
}

func (s *Server) refreshWorkloads(ctx context.Context, clusterID string) {
	if s.Workloads == nil {
		return
	}
	items, err := s.Store.ListWorkloads(ctx, clusterID)
	if err != nil || len(items) == 0 {
		return
	}
	obs, err := s.Workloads.GetWorkloads(ctx, appdb.WorkloadHints(items))
	if err != nil {
		return
	}
	_, _, _ = appdb.ReconcileWorkloads(ctx, s.Store, clusterID, items, obs)
}

func (s *Server) workloadJSON(ctx context.Context, w appdb.Workload) map[string]any {
	disks, _ := s.Store.ListWorkloadDisks(ctx, w.ClusterID, w.ID)
	nics, _ := s.Store.ListWorkloadNICs(ctx, w.ClusterID, w.ID)
	diskOut := make([]map[string]any, 0, len(disks))
	for _, d := range disks {
		diskOut = append(diskOut, map[string]any{"id": d.ID, "volume_id": d.VolumeID, "role": d.Role, "slot": d.Slot, "pci_addr": d.BusAddr, "read_only": d.ReadOnly, "format": d.Format})
	}
	nicOut := make([]map[string]any, 0, len(nics))
	for _, n := range nics {
		nicOut = append(nicOut, map[string]any{"id": n.ID, "network_id": n.NetworkID, "mac": n.MAC, "ipv4": n.IPv4, "pci_addr": n.PCIAddr, "model": n.Model})
	}
	var pid any
	if w.PID != nil {
		pid = *w.PID
	}
	devices := json.RawMessage(`[]`)
	if len(w.Devices) > 0 {
		devices = w.Devices
	}
	blockers := json.RawMessage(`[]`)
	if len(w.MigrateBlockers) > 0 {
		blockers = w.MigrateBlockers
	}
	return map[string]any{
		"id": w.ID, "node_id": w.NodeID, "name": w.Name, "kind": w.Kind,
		"status": w.Status, "reason": w.Reason, "desired_power": w.DesiredPower,
		"image_pin": w.ImagePin, "image_verified": w.ImageVerified,
		"cpus": w.CPUs, "memory_bytes": w.MemoryBytes, "privileged": w.Privileged,
		"uid_map": w.UIDMap, "gid_map": w.GIDMap, "pid": pid, "unit_active": w.UnitActive,
		"migrate_ready": w.MigrateReady, "migrate_blockers": blockers, "devices": devices,
		"warnings": w.Warnings, "disks": diskOut, "nics": nicOut,
		"autostart": w.Autostart, "pending_restart": w.PendingRestart, "firmware": w.Firmware,
		"spec": specJSON(w), "applied": appliedJSON(w),
		"created_at": w.CreatedAt.UTC().Format(time.RFC3339),
	}
}

func (s *Server) startOpKeyed(ctx context.Context, clusterID, nodeID, kind, stage, key, message string, progress int) appdb.Operation {
	op := appdb.Operation{
		ID: uuid.NewString(), ClusterID: clusterID, NodeID: nodeID, Kind: kind,
		State: "running", Stage: stage, Message: message, IdempotencyKey: key,
		Progress: &progress, UpdatedAt: time.Now().UTC(),
	}
	if existing, _ := s.Store.GetOperationByIdempotency(ctx, clusterID, key); existing != nil && key != "" {
		op.ID = existing.ID
		op.CreatedAt = existing.CreatedAt
	}
	_ = s.Store.UpsertOperation(ctx, op)
	return op
}

func normalizeKind(kind string) string {
	kind = strings.TrimSpace(kind)
	if kind == "" || kind == "system_container" {
		return lxc.KindSystemContainer
	}
	return kind
}

func hasRole(p *principal, role string) bool {
	for _, r := range p.Roles {
		if r == role {
			return true
		}
	}
	return false
}

type statusError struct {
	status int
	msg    string
}

func (e statusError) Error() string { return e.msg }

func errNotFound(msg string) error { return statusError{status: http.StatusNotFound, msg: msg} }
func errConflict(msg string) error { return statusError{status: http.StatusConflict, msg: msg} }
func errUnprocessable(msg string) error {
	return statusError{status: http.StatusUnprocessableEntity, msg: msg}
}
func errUnavailable(msg string) error {
	return statusError{status: http.StatusBadGateway, msg: msg}
}

func statusFor(err error) int {
	var se statusError
	if errors.As(err, &se) {
		return se.status
	}
	return http.StatusBadRequest
}
