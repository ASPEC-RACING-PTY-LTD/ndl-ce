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

// ZFSRPC is the typed agent surface for zpool/zfs argv.
type ZFSRPC interface {
	ZFSPool(ctx context.Context, op storage.ZFSOp) (storage.ZFSResult, error)
}

type zfsUnavailable struct{}

func (zfsUnavailable) ZFSPool(context.Context, storage.ZFSOp) (storage.ZFSResult, error) {
	return storage.ZFSResult{Status: storage.StatusUnavailable, Reason: storage.ZFSMissing}, nil
}

func AdaptZFS(client any) ZFSRPC {
	if v, ok := client.(ZFSRPC); ok {
		return v
	}
	return zfsUnavailable{}
}

func (s *Server) zfs() ZFSRPC {
	if s.ZFS != nil {
		return s.ZFS
	}
	return AdaptZFS(s.Agent)
}

func (s *Server) zfsRuntime(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.StorageRead)
	if err != nil {
		return
	}
	out := map[string]any{
		"backend": storage.BackendZFS, "incremental_send": true, "snapshots": true,
		"directory_default": true, "force_import": "refused",
	}
	_, invRow, _ := s.cachedNode(r, p.User.ClusterID)
	parsed, _ := decodeInv(invRow)
	plat := s.hostPlatform(parsed)
	if plat.ID != "debian" || plat.VersionID != "13" || plat.Architecture != "amd64" {
		out["host_supported"] = false
		out["status"] = "unsupported"
		out["reason"] = debian.ZFSUnsupportedHost
		writeJSON(w, http.StatusOK, out)
		return
	}
	out["host_supported"] = true
	out["status"] = "not_installed"
	out["packages"] = debian.ZFSRuntimePackages
	out["argv"] = debian.ZFSRuntimeInstallArgv(true)
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) importZFS(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.StoragePoolCreate)
	if err != nil {
		return
	}
	var req struct {
		GUID  string `json:"guid"`
		Name  string `json:"name"`
		Force bool   `json:"force"`
	}
	if err := readJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request")
		return
	}
	if err := storage.RefuseForceImport(req.Force); err != nil {
		writeErr(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	guid, err := storage.ParseZPoolGUID(req.GUID)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if existing, _ := s.Store.GetZFSPoolByGUID(r.Context(), guid); existing != nil {
		writeErr(w, http.StatusConflict, "zpool guid is already imported")
		return
	}
	node, err := s.Store.GetNode(r.Context(), p.User.ClusterID)
	if err != nil || node == nil {
		writeErr(w, http.StatusFailedDependency, "local node is not enrolled")
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		name = "zfs-" + guid[len(guid)-4:]
	}
	if _, err := storage.ParseZFSName(name); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	poolID := uuid.NewString()
	res, err := s.zfs().ZFSPool(r.Context(), storage.ZFSOp{Action: "import", PoolID: poolID, Name: name, GUID: guid})
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if res.Status == storage.StatusFailed || res.Status == storage.StatusUnavailable && res.Reason != "" && !strings.Contains(res.Reason, "not installed") {
		if res.Status == storage.StatusFailed {
			writeErr(w, http.StatusBadGateway, res.Reason)
			return
		}
	}
	caps, _ := json.Marshal(storage.ZFSCapabilities())
	backing, _ := json.Marshal(storage.BackingIdentity{FSUUID: guid, FSType: storage.BackendZFS, Device: name, MountPoint: res.RootPath})
	status := res.Status
	if status == "" {
		status = storage.StatusUnavailable
	}
	row := appdb.StoragePool{
		ID: poolID, ClusterID: p.User.ClusterID, NodeID: node.ID, Name: name,
		BackendType: storage.BackendZFS, Status: status, Reason: res.Reason, RootPath: res.RootPath,
		Backing: backing, Capabilities: caps,
	}
	if row.RootPath == "" {
		row.RootPath = storage.ZFSMountRoot + "/" + guid
	}
	if err := s.Store.CreateStoragePool(r.Context(), row); err != nil {
		writeErr(w, http.StatusConflict, "could not record storage pool")
		return
	}
	_ = s.Store.UpsertZFSPool(r.Context(), appdb.ZFSPool{PoolID: poolID, ZPoolGUID: guid, ZPoolName: name})
	s.audit(r, p.User.ClusterID, p.User.ID, "storage.zfs.import", "ok", poolID)
	writeJSON(w, http.StatusCreated, poolJSON(row))
}

func (s *Server) createZFS(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.StoragePoolCreate)
	if err != nil {
		return
	}
	var req struct {
		Name  string   `json:"name"`
		Disks []string `json:"disks"`
		Force bool     `json:"force"`
	}
	if err := readJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request")
		return
	}
	if err := storage.RefuseForceImport(req.Force); err != nil {
		writeErr(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	if _, err := storage.ParseZFSName(req.Name); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	var disks []string
	rootDev := s.hostRootDisk(r, p.User.ClusterID)
	for _, d := range req.Disks {
		loc, err := storage.ParseDiskLocator(d, rootDev)
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
	res, err := s.zfs().ZFSPool(r.Context(), storage.ZFSOp{Action: "create-pool", PoolID: poolID, Name: req.Name, Disks: disks, RootDevice: rootDev})
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if res.Status == storage.StatusFailed {
		writeErr(w, http.StatusBadGateway, res.Reason)
		return
	}
	guid := res.GUID
	if guid == "" {
		guid = "pending"
	}
	caps, _ := json.Marshal(storage.ZFSCapabilities())
	backing, _ := json.Marshal(storage.BackingIdentity{FSUUID: guid, FSType: storage.BackendZFS, Device: req.Name, MountPoint: res.RootPath})
	status := res.Status
	if status == "" {
		status = storage.StatusUnavailable
	}
	row := appdb.StoragePool{
		ID: poolID, ClusterID: p.User.ClusterID, NodeID: node.ID, Name: req.Name,
		BackendType: storage.BackendZFS, Status: status, Reason: res.Reason, RootPath: res.RootPath,
		Backing: backing, Capabilities: caps,
	}
	if row.RootPath == "" {
		row.RootPath = storage.ZFSMountRoot + "/" + poolID
	}
	if err := s.Store.CreateStoragePool(r.Context(), row); err != nil {
		writeErr(w, http.StatusConflict, "could not record storage pool")
		return
	}
	if guid != "pending" {
		_ = s.Store.UpsertZFSPool(r.Context(), appdb.ZFSPool{PoolID: poolID, ZPoolGUID: guid, ZPoolName: req.Name})
	}
	s.audit(r, p.User.ClusterID, p.User.ID, "storage.zfs.create", "ok", poolID)
	writeJSON(w, http.StatusCreated, poolJSON(row))
}

func (s *Server) createZFSVolume(ctx context.Context, clusterID string, pool appdb.StoragePool, class string, size int64) (appdb.Volume, error) {
	zfs, _ := s.Store.GetZFSPool(ctx, pool.ID)
	name := pool.Name
	if zfs != nil {
		name = zfs.ZPoolName
	}
	volID := uuid.NewString()
	res, err := s.zfs().ZFSPool(ctx, storage.ZFSOp{
		Action: "create-volume", PoolID: pool.ID, Name: name, VolumeID: volID, Class: class, SizeBytes: size,
	})
	if err != nil {
		return appdb.Volume{}, err
	}
	if res.Status == storage.StatusFailed || res.Status == storage.StatusUnavailable {
		return appdb.Volume{}, errUnprocessable(firstNonEmpty(res.Reason, storage.ZFSMissing))
	}
	format := storage.FormatDataset
	kind := storage.KindFilesystem
	if class == storage.ClassVMDisk {
		format = storage.FormatZvol
		kind = storage.KindBlock
	}
	row := appdb.Volume{
		ID: volID, ClusterID: clusterID, NodeID: pool.NodeID, PoolID: pool.ID,
		Class: class, Kind: kind, Format: format, SizeBytes: size,
		Status: storage.StatusAvailable, BackendType: storage.BackendZFS, BackendRef: res.BackendRef,
	}
	if err := s.Store.CreateVolume(ctx, row); err != nil {
		return appdb.Volume{}, err
	}
	_ = s.Store.UpsertZFSDataset(ctx, appdb.ZFSDataset{VolumeID: volID, DatasetGUID: res.GUID, DatasetName: res.Dataset})
	return row, nil
}

func (s *Server) createZFSSnapshot(w http.ResponseWriter, r *http.Request, p *principal, row appdb.Workload, vol appdb.Volume, pool appdb.StoragePool, name string, existing []appdb.Snapshot) {
	poolName := s.zfsPoolName(r.Context(), pool)
	tag := purposeTag(name)
	res, err := s.zfs().ZFSPool(r.Context(), storage.ZFSOp{
		Action: "snapshot", PoolID: pool.ID, Name: poolName, VolumeID: vol.ID, Snapshot: tag,
	})
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if res.Status != storage.StatusAvailable {
		writeErr(w, http.StatusBadGateway, firstNonEmpty(res.Reason, storage.ZFSMissing))
		return
	}
	parentID := ""
	if len(existing) > 0 {
		parentID = existing[len(existing)-1].ID
	}
	snap := appdb.Snapshot{
		ID: uuid.NewString(), ClusterID: p.User.ClusterID, WorkloadID: row.ID, VolumeID: vol.ID,
		Name: name, PurposeTag: tag, Mechanism: appdb.MechanismZFS, BackendRef: res.BackendRef,
		ParentID: parentID, ChainDepth: 0, Status: appdb.SnapshotAvailable,
	}
	if err := s.Store.CreateSnapshot(r.Context(), snap); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.audit(r, p.User.ClusterID, p.User.ID, "snapshot.create", "ok", snap.ID)
	writeJSON(w, http.StatusCreated, snapshotJSON(snap))
}

func (s *Server) zfsPoolName(ctx context.Context, pool appdb.StoragePool) string {
	if z, _ := s.Store.GetZFSPool(ctx, pool.ID); z != nil && z.ZPoolName != "" {
		return z.ZPoolName
	}
	return pool.Name
}

func (s *Server) snapshotTag(ref string) string {
	if i := strings.LastIndex(ref, "@"); i >= 0 && i+1 < len(ref) {
		return ref[i+1:]
	}
	return ""
}

func (s *Server) hostRootDisk(r *http.Request, clusterID string) string {
	_, invRow, _ := s.cachedNode(r, clusterID)
	parsed, _ := decodeInv(invRow)
	for _, d := range parsed.BlockDevices {
		if d.MountHint != "/" {
			continue
		}
		n := strings.TrimSpace(d.Name)
		if n == "" {
			continue
		}
		if strings.HasPrefix(n, "/dev/") {
			return n
		}
		return "/dev/" + n
	}
	return ""
}
