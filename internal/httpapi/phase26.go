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

const iscsiSnapReason = "iSCSI LUNs do not use qcow2 overlay chains. Snapshots are not available on a raw LUN."

// DatastoreRPC is the typed agent surface for mount and iSCSI login.
type DatastoreRPC interface {
	Datastore(ctx context.Context, op storage.DatastoreOp) (storage.DatastoreResult, error)
}

type datastoreUnavailable struct{}

func (datastoreUnavailable) Datastore(context.Context, storage.DatastoreOp) (storage.DatastoreResult, error) {
	return storage.DatastoreResult{Status: storage.StatusUnavailable, Reason: storage.NFSMissing, Incremental: false, Capabilities: storage.NFSCapabilities()}, nil
}

func AdaptDatastore(client any) DatastoreRPC {
	if v, ok := client.(DatastoreRPC); ok {
		return v
	}
	return datastoreUnavailable{}
}

func (s *Server) datastore() DatastoreRPC {
	if s.Datastore != nil {
		return s.Datastore
	}
	return AdaptDatastore(s.Agent)
}

func (s *Server) datastoreRuntime(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.StorageRead)
	if err != nil {
		return
	}
	out := map[string]any{
		"nfs": true, "smb": true, "iscsi": true, "incremental_send": false,
		"directory_default": true, "passwords_in_unit_files": false,
	}
	_, invRow, _ := s.cachedNode(r, p.User.ClusterID)
	parsed, _ := decodeInv(invRow)
	plat := s.hostPlatform(parsed)
	if plat.ID != "debian" || plat.VersionID != "13" || plat.Architecture != "amd64" {
		out["host_supported"] = false
		out["status"] = "unsupported"
		out["reason"] = storage.DatastoreUnsup
		writeJSON(w, http.StatusOK, out)
		return
	}
	out["host_supported"] = true
	out["status"] = "not_installed"
	out["packages"] = debian.DatastoreRuntimePackages
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) createNFS(w http.ResponseWriter, r *http.Request) {
	s.createDatastore(w, r, storage.BackendNFS)
}

func (s *Server) createSMB(w http.ResponseWriter, r *http.Request) {
	s.createDatastore(w, r, storage.BackendSMB)
}

func (s *Server) createISCSI(w http.ResponseWriter, r *http.Request) {
	s.createDatastore(w, r, storage.BackendISCSI)
}

func (s *Server) createDatastore(w http.ResponseWriter, r *http.Request, kind string) {
	p, err := s.require(w, r, rbac.StoragePoolCreate)
	if err != nil {
		return
	}
	var req struct {
		Name     string `json:"name"`
		Locator  string `json:"locator"`
		Portal   string `json:"portal"`
		IQN      string `json:"iqn"`
		Username string `json:"username"`
		Password string `json:"password"`
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
	locator := strings.TrimSpace(req.Locator)
	iqn := strings.TrimSpace(req.IQN)
	portal := strings.TrimSpace(req.Portal)
	switch kind {
	case storage.BackendNFS:
		if _, _, err := storage.ParseNFSLocator(locator); err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
	case storage.BackendSMB:
		if _, _, err := storage.ParseSMBLocator(locator); err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
	case storage.BackendISCSI:
		if iqn == "" {
			iqn = locator
		}
		if _, err := storage.ParseIQN(iqn); err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		if _, err := storage.ParsePortal(portal); err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		locator = iqn
	}
	node, err := s.Store.GetNode(r.Context(), p.User.ClusterID)
	if err != nil || node == nil {
		writeErr(w, http.StatusFailedDependency, "local node is not enrolled")
		return
	}
	poolID := uuid.NewString()
	user, pass := strings.TrimSpace(req.Username), req.Password
	if kind == storage.BackendISCSI && (user != "" || strings.TrimSpace(pass) != "") {
		writeErr(w, http.StatusUnprocessableEntity, "CHAP authentication is unsupported")
		return
	}
	if kind == storage.BackendSMB {
		if storedUser, storedPass, _ := s.Store.DatastoreSecret(r.Context(), poolID); storedUser != "" {
			user, pass = storedUser, storedPass
		}
	}
	res, err := s.datastore().Datastore(r.Context(), storage.DatastoreOp{
		Action: "mount", PoolID: poolID, Kind: kind, Locator: locator, Portal: portal, IQN: iqn, Username: user, Password: pass,
	})
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if res.Status == storage.StatusFailed {
		writeErr(w, http.StatusBadGateway, res.Reason)
		return
	}
	caps, _ := json.Marshal(storage.NFSCapabilities())
	if kind == storage.BackendISCSI {
		caps, _ = json.Marshal(storage.ISCSICapabilities())
	}
	if kind == storage.BackendSMB {
		caps, _ = json.Marshal(storage.SMBCapabilities())
	}
	backing, _ := json.Marshal(storage.BackingIdentity{
		FSUUID: iqn, FSType: kind, Device: firstNonEmpty(portal, locator), MountPoint: res.RootPath, Shared: true,
	})
	status := res.Status
	if status == "" {
		status = storage.StatusUnavailable
	}
	row := appdb.StoragePool{
		ID: poolID, ClusterID: p.User.ClusterID, NodeID: node.ID, Name: name,
		BackendType: kind, Status: status, Reason: res.Reason, RootPath: res.RootPath,
		Backing: backing, Capabilities: caps, Warnings: res.Warnings, WarningText: res.WarningText,
	}
	if row.RootPath == "" && kind != storage.BackendISCSI {
		row.RootPath, _ = storage.DatastoreMountPath(kind, poolID)
	}
	if err := s.Store.CreateStoragePool(r.Context(), row); err != nil {
		writeErr(w, http.StatusConflict, "could not record storage pool")
		return
	}
	_ = s.Store.UpsertDatastore(r.Context(), appdb.Datastore{PoolID: poolID, Kind: kind, Locator: locator, Portal: portal, IQN: iqn})
	if kind == storage.BackendSMB {
		_ = s.Store.UpsertDatastoreSecret(r.Context(), poolID, user, pass)
	}
	s.audit(r, p.User.ClusterID, p.User.ID, "storage."+kind+".create", "ok", poolID)
	writeJSON(w, http.StatusCreated, poolJSON(row))
}

func (s *Server) createISCSIVolume(ctx context.Context, clusterID string, pool appdb.StoragePool, class string, size int64) (appdb.Volume, error) {
	if class != storage.ClassVMDisk {
		return appdb.Volume{}, errUnprocessable("iSCSI pools store one VM disk LUN")
	}
	existing, _ := s.Store.ListVolumes(ctx, clusterID, pool.ID)
	if len(existing) > 0 {
		return appdb.Volume{}, errUnprocessable("iSCSI pool already has a LUN volume")
	}
	ds, _ := s.Store.GetDatastore(ctx, pool.ID)
	op := storage.DatastoreOp{Action: "mount", PoolID: pool.ID, Kind: storage.BackendISCSI}
	if ds != nil {
		op.Locator, op.Portal, op.IQN = ds.Locator, ds.Portal, ds.IQN
	}
	res, err := s.datastore().Datastore(ctx, op)
	if err != nil {
		return appdb.Volume{}, err
	}
	if res.Status != storage.StatusAvailable {
		return appdb.Volume{}, errUnprocessable(firstNonEmpty(res.Reason, storage.ISCSIMissing))
	}
	ref := firstNonEmpty(res.BackendRef, res.RootPath)
	if strings.TrimSpace(ref) == "" {
		return appdb.Volume{}, errUnprocessable("iSCSI device is not present")
	}
	if _, err := storage.HostVolumePath(storage.BackendISCSI, firstNonEmpty(pool.RootPath, ref), ref); err != nil {
		return appdb.Volume{}, err
	}
	volID := uuid.NewString()
	row := appdb.Volume{
		ID: volID, ClusterID: clusterID, NodeID: pool.NodeID, PoolID: pool.ID,
		Class: class, Kind: storage.KindBlock, Format: storage.FormatRaw, SizeBytes: size,
		Status: storage.StatusAvailable, BackendType: storage.BackendISCSI, BackendRef: ref,
	}
	if err := s.Store.CreateVolume(ctx, row); err != nil {
		return appdb.Volume{}, err
	}
	return row, nil
}

func (s *Server) refreshDatastores(ctx context.Context, clusterID string, pools []appdb.StoragePool) {
	obs := storage.Observation{}
	for _, p := range pools {
		ds, _ := s.Store.GetDatastore(ctx, p.ID)
		op := storage.DatastoreOp{Action: "observe", PoolID: p.ID, Kind: p.BackendType}
		if ds != nil {
			op.Locator = ds.Locator
			op.Portal = ds.Portal
			op.IQN = ds.IQN
		}
		res, err := s.datastore().Datastore(ctx, op)
		seen := storage.ObservedPool{
			PoolID: p.ID, BackendType: p.BackendType, RootPath: p.RootPath,
			Status: storage.StatusUnavailable, Capabilities: storage.NFSCapabilities(),
		}
		if p.BackendType == storage.BackendISCSI {
			seen.Capabilities = storage.ISCSICapabilities()
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
	}
	_, _, _ = appdb.ReconcileStorage(ctx, s.Store, clusterID, pools, obs)
}
