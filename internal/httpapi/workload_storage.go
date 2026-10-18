package httpapi

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/google/uuid"
	"github.com/no-dal/ndl-ce/internal/appdb"
	"github.com/no-dal/ndl-ce/internal/lxc"
	"github.com/no-dal/ndl-ce/internal/rbac"
	"github.com/no-dal/ndl-ce/internal/storage"
)

// Workload storage: extra folders bind-mounted into a system container.
//
// These endpoints only edit the container's mount list. They never stop,
// restart or rebuild the container, and never delete, format or re-own
// existing data. Removing a mount drops the config line; the folder and
// its files stay on the host. A new folder on a pool is created as a
// storage volume the operator owns, so no cleanup ever removes it.

// mountPoolBackends are pools whose container filesystems are folders a
// container can bind-mount. Block-backed pools (LVM, iSCSI, Ceph) are not.
var mountPoolBackends = map[string]bool{
	storage.BackendDirectory: true,
	storage.BackendZFS:       true,
	storage.BackendNFS:       true,
	storage.BackendSMB:       true,
}

type workloadMountRequest struct {
	// Source is an existing host folder, or empty with PoolID set to make a
	// new folder on that pool.
	Source    string `json:"source"`
	Target    string `json:"target"`
	ReadOnly  bool   `json:"read_only"`
	PoolID    string `json:"pool_id"`
	SizeBytes int64  `json:"size_bytes"`
	Label     string `json:"label"`
}

func (s *Server) loadSystemContainer(w http.ResponseWriter, r *http.Request, p *principal) (*appdb.Workload, bool) {
	wl, err := s.Store.GetWorkload(r.Context(), p.User.ClusterID, r.PathValue("id"))
	if err != nil || wl == nil {
		writeErr(w, http.StatusNotFound, "workload not found")
		return nil, false
	}
	if wl.Kind != "system-container" {
		writeErr(w, http.StatusUnprocessableEntity, "storage mounts are available for system containers")
		return nil, false
	}
	if s.Workloads == nil {
		writeErr(w, http.StatusBadGateway, "workload agent is unavailable")
		return nil, false
	}
	return wl, true
}

func mountJSON(m lxc.Mount, pools map[string]appdb.StoragePool, vol *appdb.Volume) map[string]any {
	out := map[string]any{
		"source": m.Source, "target": m.Target, "read_only": m.ReadOnly,
		"label": m.Label, "pool_id": m.PoolID,
	}
	if pool, ok := pools[m.PoolID]; ok {
		out["pool_name"] = pool.Name
	}
	if vol != nil {
		out["volume_id"] = vol.ID
		out["size_bytes"] = vol.SizeBytes
	}
	return out
}

// mountVolumes maps each pool mount's host folder to the volume behind it.
// Mounts on a plain host folder have no volume.
func (s *Server) mountVolumes(ctx context.Context, clusterID string, mounts []lxc.Mount, pools map[string]appdb.StoragePool) map[string]*appdb.Volume {
	out := map[string]*appdb.Volume{}
	seen := map[string]bool{}
	for _, m := range mounts {
		pool, ok := pools[m.PoolID]
		if m.PoolID == "" || !ok || seen[m.PoolID] {
			continue
		}
		seen[m.PoolID] = true
		vols, err := s.Store.ListVolumes(ctx, clusterID, m.PoolID)
		if err != nil {
			continue
		}
		for i := range vols {
			if host := volumeHostFolder(vols[i], pool); host != "" {
				out[host] = &vols[i]
			}
		}
	}
	return out
}

func (s *Server) workloadStorageBody(ctx context.Context, clusterID string, res lxc.Result) map[string]any {
	pools, _ := s.Store.ListStoragePools(ctx, clusterID)
	byID := map[string]appdb.StoragePool{}
	eligible := make([]map[string]any, 0)
	for _, pool := range pools {
		byID[pool.ID] = pool
		if !mountPoolBackends[pool.BackendType] {
			continue
		}
		if pool.Status != storage.StatusAvailable && pool.Status != storage.StatusWarning {
			continue
		}
		eligible = append(eligible, poolJSON(pool))
	}
	vols := s.mountVolumes(ctx, clusterID, res.Mounts, byID)
	mounts := make([]map[string]any, 0, len(res.Mounts))
	for _, m := range res.Mounts {
		mounts = append(mounts, mountJSON(m, byID, vols[m.Source]))
	}
	return map[string]any{
		"mounts":           mounts,
		"pools":            eligible,
		"mapped_root_uid":  res.MappedRootUID,
		"restart_required": res.RestartRequired,
	}
}

func (s *Server) getWorkloadStorage(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.ComputeRead)
	if err != nil {
		return
	}
	wl, ok := s.loadSystemContainer(w, r, p)
	if !ok {
		return
	}
	res, err := s.Workloads.LifecycleCT(r.Context(), lxc.LifecycleRequest{WorkloadID: wl.ID, Action: lxc.ActionMountsGet})
	if err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, s.workloadStorageBody(r.Context(), p.User.ClusterID, res))
}

// putWorkloadMounts replaces the container's mount list. Mounts already on
// the container keep their source; a new mount either names an existing
// host folder or asks for a new folder on a pool.
func (s *Server) putWorkloadMounts(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.ComputeModify)
	if err != nil {
		return
	}
	wl, ok := s.loadSystemContainer(w, r, p)
	if !ok {
		return
	}
	var req struct {
		Mounts []workloadMountRequest `json:"mounts"`
	}
	if err := readJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request")
		return
	}
	if len(req.Mounts) > lxc.MaxMounts {
		writeErr(w, http.StatusBadRequest, fmt.Sprintf("a container can have at most %d mounts", lxc.MaxMounts))
		return
	}
	// Validate everything that does not create storage before creating any.
	mounts := make([]lxc.Mount, 0, len(req.Mounts))
	for _, m := range req.Mounts {
		mounts = append(mounts, lxc.Mount{
			Source: strings.TrimSpace(m.Source), Target: m.Target, ReadOnly: m.ReadOnly,
			PoolID: m.PoolID, Label: m.Label,
		})
	}
	for i, m := range req.Mounts {
		if strings.TrimSpace(m.Source) != "" {
			continue
		}
		if strings.TrimSpace(m.PoolID) == "" {
			writeErr(w, http.StatusBadRequest, "each mount needs a host folder or a storage pool")
			return
		}
		// Placeholder so target and duplicate checks run before any pool
		// storage is created.
		mounts[i].Source = "/srv/ndl-pending/" + uuid.NewString()
	}
	if _, err := lxc.NormalizeMounts(mounts); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	for i, m := range req.Mounts {
		if strings.TrimSpace(m.Source) != "" {
			continue
		}
		vol, host, err := s.createMountVolume(r.Context(), p.User.ClusterID, m)
		if err != nil {
			writeErr(w, statusFor(err), err.Error())
			return
		}
		s.audit(r, p.User.ClusterID, p.User.ID, "storage.volume.create", "ok", vol.ID)
		mounts[i].Source = host
		mounts[i].Create = true
	}
	res, err := s.Workloads.LifecycleCT(r.Context(), lxc.LifecycleRequest{
		WorkloadID: wl.ID, Action: lxc.ActionMountsSet, Mounts: mounts,
	})
	if err != nil {
		writeErr(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	s.audit(r, p.User.ClusterID, p.User.ID, "workload.storage.mounts", "ok", wl.ID)
	s.emitEvent(r.Context(), p.User.ClusterID, wl.NodeID, "workload.storage.updated", map[string]string{
		"workload_id": wl.ID, "mounts": fmt.Sprint(len(res.Mounts)),
	})
	writeJSON(w, http.StatusOK, s.workloadStorageBody(r.Context(), p.User.ClusterID, res))
}

// createMountVolume makes a new filesystem volume on a pool for a mount.
// It is owned by the operator so orphan cleanup never removes it.
func (s *Server) createMountVolume(ctx context.Context, clusterID string, m workloadMountRequest) (appdb.Volume, string, error) {
	pool, err := s.Store.GetStoragePool(ctx, clusterID, m.PoolID)
	if err != nil || pool == nil {
		return appdb.Volume{}, "", errNotFound("storage pool not found")
	}
	if !mountPoolBackends[pool.BackendType] {
		return appdb.Volume{}, "", errUnprocessable("this pool provides block storage; pick a directory, ZFS, NFS or SMB pool")
	}
	if pool.Status != storage.StatusAvailable && pool.Status != storage.StatusWarning {
		return appdb.Volume{}, "", errConflict("storage pool is unavailable")
	}
	class := storage.ClassContainerRoot
	if invalidVolumeSize(pool.BackendType, class, m.SizeBytes) {
		return appdb.Volume{}, "", errUnprocessable("size is required and must fit the pool")
	}
	var row appdb.Volume
	if pool.BackendType == storage.BackendZFS {
		row, err = s.createZFSVolume(ctx, clusterID, *pool, class, m.SizeBytes)
		if err != nil {
			return appdb.Volume{}, "", err
		}
	} else {
		if s.Storage == nil {
			return appdb.Volume{}, "", fmt.Errorf("storage agent is unavailable")
		}
		volID := uuid.NewString()
		hint := appdb.PoolHints([]appdb.StoragePool{*pool})[0]
		res, err := s.Storage.CreateDirectoryVolume(ctx, storage.CreateVolumeRequest{
			VolumeID: volID, PoolID: pool.ID, RootPath: pool.RootPath, Class: class, Size: m.SizeBytes,
			Owner: storage.VolumeOwnerName, OwnerKind: storage.VolumeKindOperator,
		}, hint)
		if err != nil {
			return appdb.Volume{}, "", errUnprocessable(err.Error())
		}
		row = appdb.Volume{
			ID: volID, ClusterID: clusterID, NodeID: pool.NodeID, PoolID: pool.ID,
			Class: res.Handle.Class, Kind: res.Handle.Kind, Format: res.Handle.Format, SizeBytes: m.SizeBytes,
			Status: storage.StatusAvailable, BackendType: res.Handle.BackendType, BackendRef: res.Handle.BackendRef,
			XattrState: res.XattrState, AllocatedBytes: &res.Allocated,
		}
		if pool.BackendType == storage.BackendNFS || pool.BackendType == storage.BackendSMB {
			row.BackendType = pool.BackendType
		}
		if err := s.Store.CreateVolume(ctx, row); err != nil {
			return appdb.Volume{}, "", fmt.Errorf("could not record volume")
		}
	}
	row.Owner = storage.VolumeOwnerName
	row.OwnerKind = storage.VolumeKindOperator
	if err := s.Store.UpdateVolumeOwner(ctx, row); err != nil {
		return appdb.Volume{}, "", fmt.Errorf("could not record volume owner")
	}
	// Directory, NFS and SMB pools return a path relative to the pool root.
	host := volumeHostFolder(row, *pool)
	if host == "" {
		return appdb.Volume{}, "", errUnprocessable("the pool did not return a folder for this volume")
	}
	s.emitEvent(ctx, clusterID, pool.NodeID, "storage.volume.created", map[string]string{"volume_id": row.ID})
	return row, host, nil
}

// volumeHostFolder is the absolute host folder of a filesystem volume, or
// "" when it has none. ZFS returns its mountpoint; directory, NFS and SMB
// pools return a path relative to the pool root.
func volumeHostFolder(vol appdb.Volume, pool appdb.StoragePool) string {
	if strings.HasPrefix(vol.BackendRef, "/") {
		return vol.BackendRef
	}
	host, err := storage.HostVolumePath(vol.BackendType, pool.RootPath, vol.BackendRef)
	if err != nil || !strings.HasPrefix(host, "/") {
		return ""
	}
	return host
}

// resizeWorkloadMount grows or shrinks the pool volume behind one mount.
// The mount list itself is unchanged, so nothing needs a restart.
func (s *Server) resizeWorkloadMount(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.ComputeModify)
	if err != nil {
		return
	}
	wl, ok := s.loadSystemContainer(w, r, p)
	if !ok {
		return
	}
	var req struct {
		Target    string `json:"target"`
		SizeBytes int64  `json:"size_bytes"`
	}
	if err := readJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request")
		return
	}
	if req.SizeBytes < lxc.MinRootSize {
		writeErr(w, http.StatusBadRequest, "size must be at least 1 GB")
		return
	}
	ctx := r.Context()
	res, err := s.Workloads.LifecycleCT(ctx, lxc.LifecycleRequest{WorkloadID: wl.ID, Action: lxc.ActionMountsGet})
	if err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	var mount *lxc.Mount
	for i := range res.Mounts {
		if res.Mounts[i].Target == strings.TrimSpace(req.Target) {
			mount = &res.Mounts[i]
		}
	}
	if mount == nil {
		writeErr(w, http.StatusNotFound, "mount not found")
		return
	}
	if mount.PoolID == "" {
		writeErr(w, http.StatusUnprocessableEntity, "this mount is a host folder; only folders made on a storage pool can be resized")
		return
	}
	pool, err := s.Store.GetStoragePool(ctx, p.User.ClusterID, mount.PoolID)
	if err != nil || pool == nil {
		writeErr(w, http.StatusNotFound, "storage pool not found")
		return
	}
	vol := s.mountVolumes(ctx, p.User.ClusterID, []lxc.Mount{*mount}, map[string]appdb.StoragePool{pool.ID: *pool})[mount.Source]
	if vol == nil {
		writeErr(w, http.StatusNotFound, "the storage volume behind this mount was not found")
		return
	}
	running := wl.UnitActive || strings.EqualFold(wl.Status, lxc.StatusRunning)
	if err := s.resizePoolVolume(context.WithoutCancel(ctx), p.User.ClusterID, *vol, *pool, req.SizeBytes, running, nil); err != nil {
		s.audit(r, p.User.ClusterID, p.User.ID, "storage.volume.resize", "failed", vol.ID)
		writeErr(w, statusFor(err), err.Error())
		return
	}
	s.audit(r, p.User.ClusterID, p.User.ID, "storage.volume.resize", "ok", vol.ID)
	writeJSON(w, http.StatusOK, s.workloadStorageBody(ctx, p.User.ClusterID, res))
}
