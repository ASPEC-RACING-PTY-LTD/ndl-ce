package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/google/uuid"
	"github.com/no-dal/ndl-ce/internal/appdb"
	"github.com/no-dal/ndl-ce/internal/features"
	"github.com/no-dal/ndl-ce/internal/hostos"
	"github.com/no-dal/ndl-ce/internal/rbac"
	"github.com/no-dal/ndl-ce/internal/storage"
)

const distSnapReason = "RBD volumes do not use qcow2 overlay chains. Snapshots are not available on a mapped RBD."

// DistributedRPC is the typed agent surface for RBD attach, map, and OSD create.
type DistributedRPC interface {
	Distributed(ctx context.Context, op storage.DistributedOp) (storage.DistributedResult, error)
}

type distributedUnavailable struct{}

func (distributedUnavailable) Distributed(context.Context, storage.DistributedOp) (storage.DistributedResult, error) {
	return storage.DistributedResult{
		Status: storage.StatusUnavailable, Reason: storage.DistributedMissing,
		Incremental: false, Capabilities: storage.DistributedCapabilities(),
	}, nil
}

func AdaptDistributed(client any) DistributedRPC {
	if v, ok := client.(DistributedRPC); ok {
		return v
	}
	return distributedUnavailable{}
}

func (s *Server) distributed() DistributedRPC {
	if s.Distributed != nil {
		return s.Distributed
	}
	return AdaptDistributed(s.Agent)
}

func (s *Server) distFeatureEnabled(r *http.Request, clusterID string) bool {
	mod, _ := features.Lookup(features.IDDistStorage)
	row := s.featureRow(r, clusterID, mod)
	return row.Enabled
}

func (s *Server) distributedRuntime(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.StorageRead)
	if err != nil {
		return
	}
	osdProc, _ := storage.ObserveOSD(s.OSDProcs)
	osds, _ := s.Store.ListDistributedOSDs(r.Context(), p.User.ClusterID)
	var items []map[string]any
	for _, o := range osds {
		items = append(items, map[string]any{
			"id": o.ID, "disk": o.Disk, "status": o.Status, "reason": o.Reason, "pool_id": o.PoolID,
		})
	}
	out := map[string]any{
		"backend":           storage.BackendDistributed,
		"incremental_send":  false,
		"directory_default": true,
		"keys_in_list_json": false,
		"feature_enabled":   s.distFeatureEnabled(r, p.User.ClusterID),
		"osd_process":       osdProc,
		"osd_started":       false,
		"osds":              items,
		"reason":            storage.OSDNotStarted,
		"vm_disk_rbd":       true,
	}
	mod, _ := features.Lookup(features.IDDistStorage)
	row := s.featureRow(r, p.User.ClusterID, mod)
	if row.RuntimeStatus == appdb.FeatureRunning && osdProc {
		out["osd_started"] = true
	}
	_, invRow, _ := s.cachedNode(r, p.User.ClusterID)
	parsed, _ := decodeInv(invRow)
	plat := s.hostPlatform(parsed)
	if plat.ID != "debian" || plat.VersionID != "13" || plat.Architecture != "amd64" {
		out["host_supported"] = false
		out["status"] = "unsupported"
		out["reason"] = storage.DistributedUnsup
		writeJSON(w, http.StatusOK, out)
		return
	}
	out["host_supported"] = true
	out["status"] = "not_installed"
	if row.Enabled {
		out["status"] = "enabled"
		out["reason"] = row.Reason
		if row.Reason == "" {
			out["reason"] = storage.OSDNotStarted
		}
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) attachDistributed(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.StoragePoolCreate)
	if err != nil {
		return
	}
	if !s.distFeatureEnabled(r, p.User.ClusterID) {
		writeErr(w, http.StatusUnprocessableEntity, "enable the distributed_storage feature before attaching a cluster")
		return
	}
	var req struct {
		Name     string `json:"name"`
		Locator  string `json:"locator"`
		User     string `json:"user"`
		CephxKey string `json:"cephx_key"`
	}
	if err := readJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request")
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		writeErr(w, http.StatusBadRequest, "name is required")
		return
	}
	mons, pool, err := storage.ParseDistributedLocator(req.Locator)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	user, err := storage.ParseCephUser(firstNonEmpty(req.User, storage.DefaultCephUser))
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	key, err := storage.ParseCephxKey(req.CephxKey)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	node, err := s.Store.GetNode(r.Context(), p.User.ClusterID)
	if err != nil || node == nil {
		writeErr(w, http.StatusFailedDependency, "local node is not enrolled")
		return
	}
	poolID := uuid.NewString()
	keyring, kerr := storage.KeyringPath(poolID)
	if kerr != nil {
		writeErr(w, http.StatusBadRequest, kerr.Error())
		return
	}
	locator := strings.Join(mons, ",") + "/" + pool
	res, aerr := s.distributed().Distributed(r.Context(), storage.DistributedOp{
		Action: "attach", PoolID: poolID, Locator: locator, CephPool: pool, CephUser: user,
		CephxKey: key, Keyring: keyring,
	})
	if aerr != nil {
		writeErr(w, http.StatusBadRequest, aerr.Error())
		return
	}
	if res.Status == storage.StatusFailed {
		writeErr(w, http.StatusBadGateway, res.Reason)
		return
	}
	caps, _ := json.Marshal(storage.DistributedCapabilities())
	backing, _ := json.Marshal(storage.BackingIdentity{
		FSType: storage.BackendDistributed, Device: locator, MountPoint: res.RootPath, Shared: true,
	})
	status := res.Status
	if status == "" {
		status = storage.StatusUnavailable
	}
	row := appdb.StoragePool{
		ID: poolID, ClusterID: p.User.ClusterID, NodeID: node.ID, Name: name,
		BackendType: storage.BackendDistributed, Status: status, Reason: res.Reason, RootPath: res.RootPath,
		Backing: backing, Capabilities: caps, Warnings: res.Warnings, WarningText: res.WarningText,
	}
	if err := s.Store.CreateStoragePool(r.Context(), row); err != nil {
		writeErr(w, http.StatusConflict, "could not record storage pool")
		return
	}
	if err := s.Store.UpsertDistributedPool(r.Context(), appdb.DistributedPool{
		PoolID: poolID, ClusterID: p.User.ClusterID, Locator: locator, CephPool: pool, CephUser: user,
	}); err != nil {
		writeErr(w, http.StatusInternalServerError, "could not record distributed pool")
		return
	}
	if err := s.Store.UpsertDistributedSecret(r.Context(), poolID, key); err != nil {
		writeErr(w, http.StatusInternalServerError, "could not record distributed credentials")
		return
	}
	s.audit(r, p.User.ClusterID, p.User.ID, "storage.distributed.attach", "ok", poolID)
	out := poolJSON(row)
	writeJSON(w, http.StatusCreated, out)
}

func (s *Server) createDistributedVolume(ctx context.Context, clusterID string, pool appdb.StoragePool, class string, size int64) (appdb.Volume, error) {
	if class != storage.ClassVMDisk {
		return appdb.Volume{}, errUnprocessable("distributed pools store VM disk RBDs")
	}
	if pool.Status == storage.StatusUnavailable {
		return appdb.Volume{}, errUnprocessable(firstNonEmpty(pool.Reason, storage.ClusterDownMsg))
	}
	dp, _ := s.Store.GetDistributedPool(ctx, pool.ID)
	if dp == nil {
		return appdb.Volume{}, errUnprocessable("distributed pool locators are missing")
	}
	key, _ := s.Store.DistributedSecret(ctx, pool.ID)
	volID := uuid.NewString()
	keyring, err := storage.KeyringPath(pool.ID)
	if err != nil {
		return appdb.Volume{}, err
	}
	res, err := s.distributed().Distributed(ctx, storage.DistributedOp{
		Action: "create-volume", PoolID: pool.ID, Locator: dp.Locator, CephPool: dp.CephPool,
		CephUser: dp.CephUser, CephxKey: key, Keyring: keyring, VolumeID: volID,
		Class: class, SizeBytes: size, Image: volID,
	})
	if err != nil {
		return appdb.Volume{}, err
	}
	if res.Status != storage.StatusAvailable {
		return appdb.Volume{}, errUnprocessable(firstNonEmpty(res.Reason, storage.ClusterDownMsg))
	}
	row := appdb.Volume{
		ID: volID, ClusterID: clusterID, NodeID: pool.NodeID, PoolID: pool.ID,
		Class: storage.ClassVMDisk, Kind: storage.KindBlock, Format: storage.FormatRaw, SizeBytes: size,
		Status: storage.StatusAvailable, BackendType: storage.BackendDistributed, BackendRef: res.BackendRef,
	}
	if err := s.Store.CreateVolume(ctx, row); err != nil {
		return appdb.Volume{}, err
	}
	return row, nil
}

func (s *Server) createDistributedOSD(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.StoragePoolCreate)
	if err != nil {
		return
	}
	if strings.TrimSpace(r.Header.Get(confirmHeader)) != storage.StartOSDConfirm {
		writeErr(w, http.StatusUnprocessableEntity, "OSD bring-up requires X-Nodal-Confirm: start-ceph-osd")
		return
	}
	if !s.distFeatureEnabled(r, p.User.ClusterID) {
		writeErr(w, http.StatusUnprocessableEntity, "enable the distributed_storage feature before OSD bring-up")
		return
	}
	var req struct {
		Disk   string `json:"disk"`
		PoolID string `json:"pool_id"`
	}
	if err := readJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request")
		return
	}
	rootDev := s.hostRootDisk(r, p.User.ClusterID)
	disk, err := storage.ParseOSDDisk(req.Disk, rootDev)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	node, err := s.Store.GetNode(r.Context(), p.User.ClusterID)
	if err != nil || node == nil {
		writeErr(w, http.StatusFailedDependency, "local node is not enrolled")
		return
	}
	res, aerr := s.distributed().Distributed(r.Context(), storage.DistributedOp{
		Action: "osd-create", PoolID: strings.TrimSpace(req.PoolID), Disk: disk, RootDevice: rootDev,
	})
	if aerr != nil {
		writeErr(w, http.StatusBadRequest, aerr.Error())
		return
	}
	if res.Status == storage.StatusFailed {
		writeErr(w, http.StatusBadGateway, res.Reason)
		return
	}
	row := appdb.DistributedOSD{
		ID: uuid.NewString(), ClusterID: p.User.ClusterID, NodeID: node.ID, PoolID: strings.TrimSpace(req.PoolID),
		Disk: disk, Status: appdb.FeatureNotStarted, Reason: firstNonEmpty(res.Reason, storage.OSDNotStarted),
	}
	if res.OSDStarted {
		row.Status = appdb.FeatureRunning
	}
	if err := s.Store.CreateDistributedOSD(r.Context(), row); err != nil {
		writeErr(w, http.StatusInternalServerError, "could not record OSD")
		return
	}
	s.audit(r, p.User.ClusterID, p.User.ID, "storage.distributed.osd", "ok", row.ID)
	writeJSON(w, http.StatusCreated, map[string]any{
		"id": row.ID, "disk": row.Disk, "status": row.Status, "reason": row.Reason,
		"osd_started": res.OSDStarted, "argv": res.Argv,
	})
}

func (s *Server) startDistributedOSD(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.StoragePoolCreate)
	if err != nil {
		return
	}
	if strings.TrimSpace(r.Header.Get(confirmHeader)) != storage.StartOSDConfirm {
		writeErr(w, http.StatusUnprocessableEntity, "starting ceph-osd requires X-Nodal-Confirm: start-ceph-osd")
		return
	}
	if !s.distFeatureEnabled(r, p.User.ClusterID) {
		writeErr(w, http.StatusUnprocessableEntity, "enable the distributed_storage feature before starting ceph-osd")
		return
	}
	res, uerr := s.updater().HostUpdate(r.Context(), hostos.UpdateRequest{
		Action: hostos.UpdateOSDStart, Channel: hostos.ChannelStable,
	})
	if uerr != nil {
		writeErr(w, http.StatusBadGateway, uerr.Error())
		return
	}
	mod, _ := features.Lookup(features.IDDistStorage)
	row := s.featureRow(r, p.User.ClusterID, mod)
	osdProc, _ := storage.ObserveOSD(s.OSDProcs)
	if res.Supported && res.Status != "failed" && osdProc {
		row.RuntimeStatus = appdb.FeatureRunning
		row.Reason = res.Reason
	} else {
		row.RuntimeStatus = appdb.FeatureNotStarted
		row.Reason = firstNonEmpty(res.Reason, "ceph-osd was not started. This host cannot run the Debian ceph-osd unit.")
	}
	row.UpdatedAt = s.now()
	if err := s.Store.UpsertFeature(r.Context(), row); err != nil {
		writeErr(w, http.StatusInternalServerError, "could not record OSD status")
		return
	}
	s.audit(r, p.User.ClusterID, p.User.ID, "storage.distributed.osd.start", "ok", features.IDDistStorage)
	s.distributedRuntime(w, r)
}

func (s *Server) stopDistributedOSD(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.StoragePoolCreate)
	if err != nil {
		return
	}
	res, uerr := s.updater().HostUpdate(r.Context(), hostos.UpdateRequest{
		Action: hostos.UpdateOSDStop, Channel: hostos.ChannelStable,
	})
	if uerr != nil {
		writeErr(w, http.StatusBadGateway, uerr.Error())
		return
	}
	mod, _ := features.Lookup(features.IDDistStorage)
	row := s.featureRow(r, p.User.ClusterID, mod)
	row.RuntimeStatus = appdb.FeatureNotStarted
	row.Reason = storage.OSDStopVMMsg
	if res.Reason != "" {
		row.Reason = res.Reason + " Virtual machines and system containers were not stopped."
	}
	row.UpdatedAt = s.now()
	if err := s.Store.UpsertFeature(r.Context(), row); err != nil {
		writeErr(w, http.StatusInternalServerError, "could not record OSD status")
		return
	}
	s.audit(r, p.User.ClusterID, p.User.ID, "storage.distributed.osd.stop", "ok", features.IDDistStorage)
	s.distributedRuntime(w, r)
}

func (s *Server) refreshDistributed(ctx context.Context, clusterID string, pools []appdb.StoragePool) {
	obs := storage.Observation{}
	for _, p := range pools {
		dp, _ := s.Store.GetDistributedPool(ctx, p.ID)
		op := storage.DistributedOp{Action: "observe", PoolID: p.ID}
		key, _ := s.Store.DistributedSecret(ctx, p.ID)
		op.CephxKey = key
		if dp != nil {
			op.Locator = dp.Locator
			op.CephPool = dp.CephPool
			op.CephUser = dp.CephUser
		}
		if kr, err := storage.KeyringPath(p.ID); err == nil {
			op.Keyring = kr
		}
		res, err := s.distributed().Distributed(ctx, op)
		seen := storage.ObservedPool{
			PoolID: p.ID, BackendType: storage.BackendDistributed, RootPath: p.RootPath,
			Status: storage.StatusUnavailable, Capabilities: storage.DistributedCapabilities(),
			Reason: storage.ClusterDownMsg,
		}
		if err != nil {
			seen.Reason = err.Error()
		} else {
			seen.Status = res.Status
			seen.Reason = res.Reason
			seen.Warnings = res.Warnings
			seen.WarningText = res.WarningText
			if res.RootPath != "" {
				seen.RootPath = res.RootPath
			}
			if res.Status == storage.StatusUnavailable {
				seen.Capacity = storage.Capacity{}
			}
		}
		obs.Pools = append(obs.Pools, seen)
		if seen.Status == storage.StatusUnavailable {
			vols, _ := s.Store.ListVolumes(ctx, clusterID, p.ID)
			for _, v := range vols {
				v.Status = storage.StatusUnavailable
				v.AllocatedBytes = nil
				_ = s.Store.UpdateVolumeObserved(ctx, v)
			}
		}
	}
	_, _, _ = appdb.ReconcileStorage(ctx, s.Store, clusterID, pools, obs)
}
