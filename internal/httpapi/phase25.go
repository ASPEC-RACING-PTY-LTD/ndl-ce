package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/google/uuid"
	"github.com/no-dal/ndl-ce/internal/appdb"
	"github.com/no-dal/ndl-ce/internal/hostos/debian"
	"github.com/no-dal/ndl-ce/internal/rbac"
	"github.com/no-dal/ndl-ce/internal/storage"
)

const (
	lvmRollbackRun   = "stop the workload before LVM-thin rollback"
	lvmFlattenReason  = "LVM-thin snapshots do not use qcow2 overlay chains. Flatten is not applicable."
	lvmTemplateReason = "LVM-thin snapshots do not use qcow2 overlay chains. Template overlay is not applicable."
)

// LVMRPC is the typed agent surface for LVM argv.
type LVMRPC interface {
	LVMPool(ctx context.Context, op storage.LVMOp) (storage.LVMResult, error)
}

type lvmUnavailable struct{}

func (lvmUnavailable) LVMPool(context.Context, storage.LVMOp) (storage.LVMResult, error) {
	return storage.LVMResult{Status: storage.StatusUnavailable, Reason: storage.LVMMissing, Incremental: false, Capabilities: storage.LVMCapabilities()}, nil
}

func AdaptLVM(client any) LVMRPC {
	if v, ok := client.(LVMRPC); ok {
		return v
	}
	return lvmUnavailable{}
}

func (s *Server) lvm() LVMRPC {
	if s.LVM != nil {
		return s.LVM
	}
	return AdaptLVM(s.Agent)
}

func (s *Server) lvmRuntime(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.StorageRead)
	if err != nil {
		return
	}
	out := map[string]any{
		"backend": storage.BackendLVM, "incremental_send": false, "snapshots": true,
		"directory_default": true, "vgexport": "refused",
	}
	_, invRow, _ := s.cachedNode(r, p.User.ClusterID)
	parsed, _ := decodeInv(invRow)
	plat := s.hostPlatform(parsed)
	if plat.ID != "debian" || plat.VersionID != "13" || plat.Architecture != "amd64" {
		out["host_supported"] = false
		out["status"] = "unsupported"
		out["reason"] = debian.LVMUnsupportedHost
		writeJSON(w, http.StatusOK, out)
		return
	}
	out["host_supported"] = true
	out["status"] = "not_installed"
	out["packages"] = debian.LVMRuntimePackages
	out["argv"] = debian.LVMRuntimeInstallArgv(true)
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) createLVM(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.StoragePoolCreate)
	if err != nil {
		return
	}
	var req struct {
		Name  string   `json:"name"`
		Disks []string `json:"disks"`
	}
	if err := readJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request")
		return
	}
	if _, err := storage.ParseVGName(req.Name); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	var disks []string
	rootDev := s.hostRootDisk(r, p.User.ClusterID)
	for _, d := range req.Disks {
		loc, err := storage.ParseLVMDisk(d, rootDev)
		if err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		disks = append(disks, loc)
	}
	if len(disks) == 0 {
		writeErr(w, http.StatusBadRequest, "at least one extra disk is required")
		return
	}
	node, err := s.Store.GetNode(r.Context(), p.User.ClusterID)
	if err != nil || node == nil {
		writeErr(w, http.StatusFailedDependency, "local node is not enrolled")
		return
	}
	poolID := uuid.NewString()
	res, err := s.lvm().LVMPool(r.Context(), storage.LVMOp{Action: "create-pool", PoolID: poolID, Name: req.Name, Disks: disks, RootDevice: rootDev})
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if res.Status == storage.StatusFailed {
		writeErr(w, http.StatusBadGateway, res.Reason)
		return
	}
	guid := strings.TrimSpace(res.VGUUID)
	if strings.HasPrefix(guid, "pending-") {
		guid = ""
	}
	caps, _ := json.Marshal(storage.LVMCapabilities())
	backing, _ := json.Marshal(storage.BackingIdentity{
		FSUUID: guid, FSType: storage.BackendLVM, Device: req.Name, MountPoint: res.RootPath, ThinPool: storage.LVMThinPoolName,
	})
	status := res.Status
	if status == "" {
		status = storage.StatusUnavailable
	}
	row := appdb.StoragePool{
		ID: poolID, ClusterID: p.User.ClusterID, NodeID: node.ID, Name: req.Name,
		BackendType: storage.BackendLVM, Status: status, Reason: res.Reason, RootPath: res.RootPath,
		Backing: backing, Capabilities: caps, Warnings: res.Warnings, WarningText: res.WarningText,
	}
	if row.RootPath == "" {
		row.RootPath = storage.LVMMountRoot + "/" + req.Name
	}
	if err := s.Store.CreateStoragePool(r.Context(), row); err != nil {
		writeErr(w, http.StatusConflict, "could not record storage pool")
		return
	}
	if guid != "" {
		if err := s.Store.UpsertLVMVG(r.Context(), appdb.LVMVG{PoolID: poolID, VGUUID: guid, VGName: req.Name, ThinPool: storage.LVMThinPoolName}); err != nil {
			writeErr(w, http.StatusInternalServerError, "could not record volume group identity")
			return
		}
	}
	s.audit(r, p.User.ClusterID, p.User.ID, "storage.lvm.create", "ok", poolID)
	writeJSON(w, http.StatusCreated, poolJSON(row))
}

func (s *Server) createLVMVolume(ctx context.Context, clusterID string, pool appdb.StoragePool, class string, size int64) (appdb.Volume, error) {
	name := s.lvmVGName(ctx, pool)
	volID := uuid.NewString()
	res, err := s.lvm().LVMPool(ctx, storage.LVMOp{
		Action: "create-volume", PoolID: pool.ID, Name: name, VolumeID: volID, Class: class, SizeBytes: size,
	})
	if err != nil {
		return appdb.Volume{}, err
	}
	if res.Status == storage.StatusFailed || res.Status == storage.StatusUnavailable {
		return appdb.Volume{}, errUnprocessable(firstNonEmpty(res.Reason, storage.LVMMissing))
	}
	format := storage.FormatDirectory
	kind := storage.KindFilesystem
	if class == storage.ClassVMDisk {
		format = storage.FormatThinLV
		kind = storage.KindBlock
	}
	row := appdb.Volume{
		ID: volID, ClusterID: clusterID, NodeID: pool.NodeID, PoolID: pool.ID,
		Class: class, Kind: kind, Format: format, SizeBytes: size,
		Status: storage.StatusAvailable, BackendType: storage.BackendLVM, BackendRef: res.BackendRef,
	}
	if err := s.Store.CreateVolume(ctx, row); err != nil {
		return appdb.Volume{}, err
	}
	_ = s.Store.UpsertLVMLV(ctx, appdb.LVMLV{VolumeID: volID, LVUUID: res.LVUUID, LVName: volID})
	return row, nil
}

func (s *Server) createLVMSnapshot(w http.ResponseWriter, r *http.Request, p *principal, row appdb.Workload, vol appdb.Volume, pool appdb.StoragePool, name string, existing []appdb.Snapshot) {
	vg := s.lvmVGName(r.Context(), pool)
	tag := purposeTag(name)
	res, err := s.lvm().LVMPool(r.Context(), storage.LVMOp{
		Action: "snapshot", PoolID: pool.ID, Name: vg, VolumeID: vol.ID, Snapshot: tag,
	})
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if res.Status != storage.StatusAvailable {
		writeErr(w, http.StatusBadGateway, firstNonEmpty(res.Reason, storage.LVMMissing))
		return
	}
	parentID := ""
	if len(existing) > 0 {
		parentID = existing[len(existing)-1].ID
	}
	snap := appdb.Snapshot{
		ID: uuid.NewString(), ClusterID: p.User.ClusterID, WorkloadID: row.ID, VolumeID: vol.ID,
		Name: name, PurposeTag: tag, Mechanism: appdb.MechanismLVM, BackendRef: res.BackendRef,
		ParentID: parentID, ChainDepth: 0, Status: appdb.SnapshotAvailable,
	}
	if err := s.Store.CreateSnapshot(r.Context(), snap); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.audit(r, p.User.ClusterID, p.User.ID, "snapshot.create", "ok", snap.ID)
	writeJSON(w, http.StatusCreated, snapshotJSON(snap))
}

func (s *Server) lvmVGName(ctx context.Context, pool appdb.StoragePool) string {
	if v, _ := s.Store.GetLVMVG(ctx, pool.ID); v != nil && v.VGName != "" {
		return v.VGName
	}
	return pool.Name
}

func (s *Server) refreshLVM(ctx context.Context, clusterID string, pools []appdb.StoragePool) {
	obs := storage.Observation{}
	for _, p := range pools {
		res, err := s.lvm().LVMPool(ctx, storage.LVMOp{
			Action: "observe", PoolID: p.ID, Name: s.lvmVGName(ctx, p), VGUUID: lvmUUID(ctx, s.Store, p.ID),
		})
		seen := storage.ObservedPool{
			PoolID: p.ID, BackendType: storage.BackendLVM, RootPath: p.RootPath,
			Status: storage.StatusUnavailable, Capabilities: storage.LVMCapabilities(),
		}
		if err != nil {
			seen.Reason = err.Error()
		} else {
			seen.Status = res.Status
			seen.Reason = res.Reason
			seen.Warnings = res.Warnings
			seen.WarningText = res.WarningText
			seen.MetadataPercent = res.MetadataPercent
			seen.Capacity = res.Capacity
			seen.Backing = storage.BackingIdentity{
				FSUUID: firstNonEmpty(res.VGUUID, lvmUUID(ctx, s.Store, p.ID)), FSType: storage.BackendLVM,
				Device: s.lvmVGName(ctx, p), MountPoint: res.RootPath, ThinPool: storage.LVMThinPoolName,
				MetadataPercent: res.MetadataPercent,
			}
			if res.RootPath != "" {
				seen.RootPath = res.RootPath
			}
			if res.Status == storage.StatusUnavailable {
				seen.Capacity = storage.Capacity{}
			}
			if res.VGUUID != "" && res.VGUUID != "pending" {
				_ = s.Store.UpsertLVMVG(ctx, appdb.LVMVG{PoolID: p.ID, VGUUID: res.VGUUID, VGName: s.lvmVGName(ctx, p), ThinPool: storage.LVMThinPoolName})
			}
		}
		obs.Pools = append(obs.Pools, seen)
	}
	_, _, _ = appdb.ReconcileStorage(ctx, s.Store, clusterID, pools, obs)
}

func lvmUUID(ctx context.Context, st appdb.Store, poolID string) string {
	v, _ := st.GetLVMVG(ctx, poolID)
	if v == nil {
		return ""
	}
	return v.VGUUID
}
