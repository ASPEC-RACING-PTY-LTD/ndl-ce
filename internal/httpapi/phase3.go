package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/no-dal/ndl-ce/internal/appdb"
	"github.com/no-dal/ndl-ce/internal/rbac"
	"github.com/no-dal/ndl-ce/internal/storage"
)

// StorageRPC is the privileged agent surface for Directory storage.
type StorageRPC interface {
	CreateDirectoryPool(ctx context.Context, req storage.CreatePoolRequest, existing []string) (storage.CreatePoolResult, error)
	CreateDirectoryVolume(ctx context.Context, req storage.CreateVolumeRequest, hint storage.PoolHint) (storage.CreateVolumeResult, error)
	GetStorage(ctx context.Context, hints []storage.PoolHint) (storage.Observation, error)
	UploadLibrary(ctx context.Context, begin storage.BeginUploadRequest, hint storage.PoolHint, r io.Reader, expectedSHA string) (storage.UploadResult, error)
}

func (s *Server) listPools(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.StorageRead)
	if err != nil {
		return
	}
	s.refreshStorage(r.Context(), p.User.ClusterID)
	pools, err := s.Store.ListStoragePools(r.Context(), p.User.ClusterID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	items := make([]map[string]any, 0, len(pools))
	for _, pool := range pools {
		items = append(items, poolJSON(pool))
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "default_path": storage.DefaultPoolPath})
}

func (s *Server) createPool(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.StoragePoolCreate)
	if err != nil {
		return
	}
	var req struct {
		Name    string `json:"name"`
		Path    string `json:"path"`
		Create  *bool  `json:"create"`
		Backend string `json:"backend_type"`
	}
	if err := readJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request")
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		req.Name = storage.DefaultPoolName
	}
	if req.Path == "" {
		req.Path = storage.DefaultPoolPath
	}
	if req.Backend != "" && req.Backend != storage.BackendDirectory {
		writeErr(w, http.StatusBadRequest, "Phase 3 supports the Directory backend only")
		return
	}
	node, err := s.Store.GetNode(r.Context(), p.User.ClusterID)
	if err != nil || node == nil {
		writeErr(w, http.StatusFailedDependency, "local node is not enrolled")
		return
	}
	if s.Storage == nil {
		writeErr(w, http.StatusBadGateway, "storage agent is unavailable")
		return
	}
	existing, _ := s.Store.ListStoragePools(r.Context(), p.User.ClusterID)
	var roots []string
	for _, pool := range existing {
		roots = append(roots, pool.RootPath)
	}
	create := true
	if req.Create != nil {
		create = *req.Create
	}
	poolID := uuid.NewString()
	op := s.startOp(r.Context(), p.User.ClusterID, node.ID, "pool.create", "validating", 10)
	res, err := s.Storage.CreateDirectoryPool(r.Context(), storage.CreatePoolRequest{
		PoolID: poolID, Name: req.Name, RootPath: req.Path, Create: create,
	}, roots)
	if err != nil {
		s.finishOp(r.Context(), op, "failed", err.Error(), 0)
		s.audit(r, p.User.ClusterID, p.User.ID, "storage.pool.create", "denied", err.Error())
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	backing, _ := json.Marshal(res.Backing)
	caps, _ := json.Marshal(res.Capabilities)
	row := appdb.StoragePool{
		ID: poolID, ClusterID: p.User.ClusterID, NodeID: node.ID, Name: req.Name,
		BackendType: storage.BackendDirectory, Status: res.Status, RootPath: res.RootPath,
		Backing: backing, Warnings: res.Warnings, WarningText: res.WarningText,
		Capabilities: caps, UsableBytes: res.Capacity.UsableBytes, AllocatedBytes: res.Capacity.AllocatedBytes,
		ProvisionedBytes: res.Capacity.ProvisionedBytes, TotalBytes: res.Capacity.TotalBytes, Adopted: res.Adopted,
	}
	if err := s.Store.CreateStoragePool(r.Context(), row); err != nil {
		s.finishOp(r.Context(), op, "failed", err.Error(), 0)
		writeErr(w, http.StatusConflict, "could not record storage pool")
		return
	}
	s.finishOp(r.Context(), op, "succeeded", "directory pool created", 100)
	s.audit(r, p.User.ClusterID, p.User.ID, "storage.pool.create", "ok", poolID)
	s.emitEvent(r.Context(), p.User.ClusterID, node.ID, "storage.pool.created", map[string]string{"pool_id": poolID})
	writeJSON(w, http.StatusCreated, poolJSON(row))
}

func (s *Server) getPool(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.StorageRead)
	if err != nil {
		return
	}
	s.refreshStorage(r.Context(), p.User.ClusterID)
	pool, err := s.Store.GetStoragePool(r.Context(), p.User.ClusterID, r.PathValue("id"))
	if err != nil || pool == nil {
		writeErr(w, http.StatusNotFound, "pool not found")
		return
	}
	writeJSON(w, http.StatusOK, poolJSON(*pool))
}

func (s *Server) listVolumes(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.StorageRead)
	if err != nil {
		return
	}
	s.refreshStorage(r.Context(), p.User.ClusterID)
	items, err := s.Store.ListVolumes(r.Context(), p.User.ClusterID, r.URL.Query().Get("pool_id"))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := make([]map[string]any, 0, len(items))
	for _, v := range items {
		out = append(out, volumeJSON(v))
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": out})
}

func (s *Server) createVolume(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.StorageVolumeCreate)
	if err != nil {
		return
	}
	var req struct {
		PoolID string `json:"pool_id"`
		Class  string `json:"class"`
		Size   int64  `json:"size_bytes"`
		Format string `json:"format"`
	}
	if err := readJSON(r, &req); err != nil || req.PoolID == "" {
		writeErr(w, http.StatusBadRequest, "pool_id, class, and size_bytes are required")
		return
	}
	pool, err := s.Store.GetStoragePool(r.Context(), p.User.ClusterID, req.PoolID)
	if err != nil || pool == nil {
		writeErr(w, http.StatusNotFound, "pool not found")
		return
	}
	if pool.Status == storage.StatusUnavailable {
		writeErr(w, http.StatusConflict, "storage pool is unavailable")
		return
	}
	if s.Storage == nil {
		writeErr(w, http.StatusBadGateway, "storage agent is unavailable")
		return
	}
	volID := uuid.NewString()
	hint := appdb.PoolHints([]appdb.StoragePool{*pool})[0]
	op := s.startOp(r.Context(), p.User.ClusterID, pool.NodeID, "volume.create", "allocating", 20)
	res, err := s.Storage.CreateDirectoryVolume(r.Context(), storage.CreateVolumeRequest{
		VolumeID: volID, PoolID: pool.ID, RootPath: pool.RootPath, Class: req.Class, Size: req.Size, Format: req.Format,
	}, hint)
	if err != nil {
		s.finishOp(r.Context(), op, "failed", err.Error(), 0)
		s.audit(r, p.User.ClusterID, p.User.ID, "storage.volume.create", "denied", err.Error())
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	row := appdb.Volume{
		ID: volID, ClusterID: p.User.ClusterID, NodeID: pool.NodeID, PoolID: pool.ID,
		Class: res.Handle.Class, Kind: res.Handle.Kind, Format: res.Handle.Format, SizeBytes: req.Size,
		Status: storage.StatusAvailable, BackendType: res.Handle.BackendType, BackendRef: res.Handle.BackendRef,
		XattrState: res.XattrState, AllocatedBytes: &res.Allocated,
	}
	if err := s.Store.CreateVolume(r.Context(), row); err != nil {
		s.finishOp(r.Context(), op, "failed", err.Error(), 0)
		writeErr(w, http.StatusConflict, "could not record volume")
		return
	}
	s.finishOp(r.Context(), op, "succeeded", "volume created", 100)
	s.audit(r, p.User.ClusterID, p.User.ID, "storage.volume.create", "ok", volID)
	s.emitEvent(r.Context(), p.User.ClusterID, pool.NodeID, "storage.volume.created", map[string]string{"volume_id": volID})
	writeJSON(w, http.StatusCreated, volumeJSON(row))
}

func (s *Server) getVolume(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.StorageRead)
	if err != nil {
		return
	}
	s.refreshStorage(r.Context(), p.User.ClusterID)
	v, err := s.Store.GetVolume(r.Context(), p.User.ClusterID, r.PathValue("id"))
	if err != nil || v == nil {
		writeErr(w, http.StatusNotFound, "volume not found")
		return
	}
	writeJSON(w, http.StatusOK, volumeJSON(*v))
}

func (s *Server) listImages(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.StorageRead)
	if err != nil {
		return
	}
	s.refreshStorage(r.Context(), p.User.ClusterID)
	items, err := s.Store.ListLibraryItems(r.Context(), p.User.ClusterID, r.URL.Query().Get("pool_id"))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := make([]map[string]any, 0, len(items))
	for _, item := range items {
		out = append(out, libraryJSON(item))
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": out})
}

func (s *Server) uploadImage(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.StorageImageUpload)
	if err != nil {
		return
	}
	if s.Storage == nil {
		writeErr(w, http.StatusBadGateway, "storage agent is unavailable")
		return
	}
	poolID, kind, display, body, closer, err := readUpload(r)
	if closer != nil {
		defer closer()
	}
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	pool, err := s.Store.GetStoragePool(r.Context(), p.User.ClusterID, poolID)
	if err != nil || pool == nil {
		writeErr(w, http.StatusNotFound, "pool not found")
		return
	}
	if pool.Status == storage.StatusUnavailable {
		writeErr(w, http.StatusConflict, "storage pool is unavailable")
		return
	}
	itemID := uuid.NewString()
	hint := appdb.PoolHints([]appdb.StoragePool{*pool})[0]
	existingItems, _ := s.Store.ListLibraryItems(r.Context(), p.User.ClusterID, pool.ID)
	var reject []string
	for _, item := range existingItems {
		if item.ChecksumSHA256 != "" {
			reject = append(reject, item.ChecksumSHA256)
		}
	}
	op := s.startOp(r.Context(), p.User.ClusterID, pool.NodeID, "image.upload", "receiving", 15)
	res, err := s.Storage.UploadLibrary(r.Context(), storage.BeginUploadRequest{
		ItemID: itemID, PoolID: pool.ID, Kind: kind, DisplayName: display, MaxBytes: storage.DefaultLibraryMax,
		RejectChecksums: reject,
	}, hint, body, "")
	if err != nil {
		s.finishOp(r.Context(), op, "failed", err.Error(), 0)
		s.audit(r, p.User.ClusterID, p.User.ID, "storage.image.upload", "denied", err.Error())
		if errors.Is(err, storage.ErrDuplicate) {
			writeErr(w, http.StatusConflict, "an identical image already exists in this pool")
			return
		}
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if existing, _ := s.Store.GetLibraryByChecksum(r.Context(), pool.ID, res.SHA256); existing != nil {
		s.finishOp(r.Context(), op, "succeeded", "duplicate checksum", 100)
		s.audit(r, p.User.ClusterID, p.User.ID, "storage.image.upload", "ok", existing.ID)
		writeJSON(w, http.StatusOK, libraryJSON(*existing))
		return
	}
	row := appdb.LibraryItem{
		ID: res.ItemID, ClusterID: p.User.ClusterID, NodeID: pool.NodeID, PoolID: pool.ID,
		Kind: res.Kind, DisplayName: res.DisplayName, BackendRef: res.BackendRef,
		SizeBytes: res.SizeBytes, ChecksumSHA256: res.SHA256, Status: storage.StatusAvailable,
	}
	if err := s.Store.CreateLibraryItem(r.Context(), row); err != nil {
		s.finishOp(r.Context(), op, "failed", err.Error(), 0)
		writeErr(w, http.StatusConflict, "could not record library item")
		return
	}
	s.finishOp(r.Context(), op, "succeeded", "image uploaded", 100)
	s.audit(r, p.User.ClusterID, p.User.ID, "storage.image.upload", "ok", row.ID)
	s.emitEvent(r.Context(), p.User.ClusterID, pool.NodeID, "storage.image.uploaded", map[string]string{"item_id": row.ID})
	writeJSON(w, http.StatusCreated, libraryJSON(row))
}

func (s *Server) getImage(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.StorageRead)
	if err != nil {
		return
	}
	s.refreshStorage(r.Context(), p.User.ClusterID)
	item, err := s.Store.GetLibraryItem(r.Context(), p.User.ClusterID, r.PathValue("id"))
	if err != nil || item == nil {
		writeErr(w, http.StatusNotFound, "image not found")
		return
	}
	writeJSON(w, http.StatusOK, libraryJSON(*item))
}

func readUpload(r *http.Request) (poolID, kind, display string, body io.Reader, closer func(), err error) {
	r.Body = http.MaxBytesReader(nil, r.Body, storage.DefaultLibraryMax)
	ct := r.Header.Get("Content-Type")
	if strings.HasPrefix(ct, "multipart/") {
		file, hdr, ferr := r.FormFile("file")
		if ferr != nil {
			return "", "", "", nil, nil, errors.New("file is required")
		}
		return strings.TrimSpace(r.FormValue("pool_id")), strings.TrimSpace(r.FormValue("kind")),
			storage.DisplayName(firstNonEmpty(r.FormValue("filename"), hdr.Filename)),
			file, func() { _ = file.Close() }, nil
	}
	poolID = strings.TrimSpace(r.URL.Query().Get("pool_id"))
	kind = strings.TrimSpace(r.URL.Query().Get("kind"))
	display = storage.DisplayName(r.URL.Query().Get("filename"))
	if poolID == "" || kind == "" {
		return "", "", "", nil, nil, errors.New("pool_id and kind are required")
	}
	return poolID, kind, display, r.Body, nil, nil
}

func firstNonEmpty(v ...string) string {
	for _, s := range v {
		if strings.TrimSpace(s) != "" {
			return s
		}
	}
	return ""
}

func (s *Server) refreshStorage(ctx context.Context, clusterID string) {
	if s.Storage == nil {
		return
	}
	pools, err := s.Store.ListStoragePools(ctx, clusterID)
	if err != nil || len(pools) == 0 {
		return
	}
	obs, err := s.Storage.GetStorage(ctx, appdb.PoolHints(pools))
	if err != nil {
		return
	}
	_, _, _ = appdb.ReconcileStorage(ctx, s.Store, clusterID, pools, obs)
}

func (s *Server) startOp(ctx context.Context, clusterID, nodeID, kind, stage string, progress int) appdb.Operation {
	op := appdb.Operation{
		ID: uuid.NewString(), ClusterID: clusterID, NodeID: nodeID, Kind: kind,
		State: "running", Stage: stage, Progress: &progress, UpdatedAt: time.Now().UTC(),
	}
	_ = s.Store.UpsertOperation(ctx, op)
	return op
}

func (s *Server) finishOp(ctx context.Context, op appdb.Operation, state, message string, progress int) {
	op.State = state
	op.Progress = &progress
	if looksLikeCreateIDs(op.Message) && !looksLikeCreateIDs(message) {
		op.Stage = message
	} else {
		op.Message = message
		if state == "succeeded" {
			op.Stage = "done"
		}
	}
	if state == "succeeded" && looksLikeCreateIDs(op.Message) {
		op.Stage = "done"
	}
	op.UpdatedAt = time.Now().UTC()
	_ = s.Store.UpsertOperation(ctx, op)
}

func looksLikeCreateIDs(message string) bool {
	return strings.Contains(message, `"workload_id"`)
}

func (s *Server) emitEvent(ctx context.Context, clusterID, nodeID, typ string, payload map[string]string) {
	body, _ := json.Marshal(payload)
	e := appdb.Event{ID: uuid.NewString(), ClusterID: clusterID, NodeID: nodeID, Type: typ, Payload: body, CreatedAt: time.Now().UTC()}
	_ = s.Store.InsertEvent(ctx, e)
	if s.Hub != nil {
		s.Hub.Publish(e)
	}
}

func poolJSON(p appdb.StoragePool) map[string]any {
	var caps any = json.RawMessage(`{}`)
	if len(p.Capabilities) > 0 {
		caps = json.RawMessage(p.Capabilities)
	}
	return map[string]any{
		"id": p.ID, "node_id": p.NodeID, "name": p.Name, "backend_type": p.BackendType,
		"status": p.Status, "reason": p.Reason, "locator": p.RootPath,
		"warnings": p.Warnings, "warning_text": p.WarningText, "capabilities": caps,
		"usable_bytes": p.UsableBytes, "allocated_bytes": p.AllocatedBytes,
		"provisioned_bytes": p.ProvisionedBytes, "total_bytes": p.TotalBytes,
		"adopted": p.Adopted, "created_at": p.CreatedAt.UTC().Format(time.RFC3339),
		"storage_classes": []string{
			storage.ClassVMDisk, storage.ClassContainerRoot, storage.ClassISO,
			storage.ClassTemplate, storage.ClassBackupStaging,
		},
	}
}

func volumeJSON(v appdb.Volume) map[string]any {
	return map[string]any{
		"id": v.ID, "pool_id": v.PoolID, "class": v.Class, "kind": v.Kind, "format": v.Format,
		"size_bytes": v.SizeBytes, "status": v.Status, "backend_type": v.BackendType,
		"backend_ref": v.BackendRef, "xattr_state": v.XattrState, "allocated_bytes": v.AllocatedBytes,
		"created_at": v.CreatedAt.UTC().Format(time.RFC3339),
	}
}

func libraryJSON(item appdb.LibraryItem) map[string]any {
	return map[string]any{
		"id": item.ID, "pool_id": item.PoolID, "kind": item.Kind, "display_name": item.DisplayName,
		"backend_ref": item.BackendRef, "size_bytes": item.SizeBytes, "checksum_sha256": item.ChecksumSHA256,
		"status": item.Status, "created_at": item.CreatedAt.UTC().Format(time.RFC3339),
	}
}
