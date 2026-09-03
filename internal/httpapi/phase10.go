package httpapi

import (
	"context"
	"net/http"
	"path"
	"strings"
	"unicode"

	"github.com/google/uuid"
	"github.com/no-dal/ndl-ce/internal/appdb"
	"github.com/no-dal/ndl-ce/internal/lxc"
	"github.com/no-dal/ndl-ce/internal/qemu"
	"github.com/no-dal/ndl-ce/internal/rbac"
	"github.com/no-dal/ndl-ce/internal/storage"
	"github.com/no-dal/ndl-ce/internal/vmspec"
)

const (
	ctSnapshotReason   = "Directory system container snapshots are not available. They are not ZFS. Use a ZFS dataset for system container snapshots."
	zfsFlattenReason   = "ZFS snapshots do not use qcow2 overlay chains. Flatten is not applicable."
	zfsRollbackRun     = "stop the workload before ZFS rollback"
	overlayRollbackRun = "stop the workload before overlay rollback"
	overlayFlattenRun  = "stop the workload before overlay flatten"
	rollbackConfirm    = "rollback"
	flattenConfirm     = "flatten"
)

func snapshotJSON(s appdb.Snapshot) map[string]any {
	return map[string]any{
		"id": s.ID, "workload_id": s.WorkloadID, "volume_id": s.VolumeID,
		"name": s.Name, "purpose_tag": s.PurposeTag, "mechanism": s.Mechanism,
		"backend_ref": s.BackendRef, "parent_id": s.ParentID, "chain_depth": s.ChainDepth,
		"status": s.Status, "created_at": s.CreatedAt.UTC().Format("2006-01-02T15:04:05Z"),
	}
}

func (s *Server) snapshotCapability(kind string, depth int, backend string) map[string]any {
	if backend == storage.BackendZFS {
		return map[string]any{
			"supported": true, "mechanism": appdb.MechanismZFS, "chain_max": 0,
			"chain_depth": depth, "reason": "",
		}
	}
	if backend == storage.BackendLVM {
		return map[string]any{
			"supported": true, "mechanism": appdb.MechanismLVM, "chain_max": 0,
			"chain_depth": depth, "reason": "",
		}
	}
	if backend == storage.BackendISCSI {
		return map[string]any{
			"supported": false, "mechanism": "", "chain_max": 0,
			"chain_depth": depth, "reason": iscsiSnapReason,
		}
	}
	if backend == storage.BackendDistributed {
		return map[string]any{
			"supported": false, "mechanism": "", "chain_max": 0,
			"chain_depth": depth, "reason": distSnapReason,
		}
	}
	if kind != vmspec.KindVM {
		return map[string]any{
			"supported": false, "mechanism": "", "chain_max": qemu.ChainMax,
			"chain_depth": depth, "reason": ctSnapshotReason,
		}
	}
	return map[string]any{
		"supported": true, "mechanism": appdb.MechanismOverlay, "chain_max": qemu.ChainMax,
		"chain_depth": depth, "reason": "",
	}
}

func (s *Server) listSnapshots(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.ComputeRead)
	if err != nil {
		return
	}
	id := r.PathValue("id")
	row, err := s.Store.GetWorkload(r.Context(), p.User.ClusterID, id)
	if err != nil || row == nil {
		writeErr(w, http.StatusNotFound, "workload not found")
		return
	}
	items, err := s.Store.ListSnapshots(r.Context(), p.User.ClusterID, id)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := make([]map[string]any, 0, len(items))
	for _, item := range items {
		out = append(out, snapshotJSON(item))
	}
	depth := len(items)
	backend := ""
	if vol, pool, _, locErr := s.bootVolumeLocator(r.Context(), p.User.ClusterID, *row); locErr == nil {
		backend = pool.BackendType
		if backend != storage.BackendZFS && backend != storage.BackendLVM && backend != storage.BackendISCSI && backend != storage.BackendDistributed {
			depth = overlayChainDepth(vol.BackendRef, items)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"items":      out,
		"capability": s.snapshotCapability(row.Kind, depth, backend),
	})
}

func (s *Server) createSnapshot(w http.ResponseWriter, r *http.Request) {
	p, err := s.requireAny(w, r, rbac.ComputeSnapshot, rbac.StorageSnapshot)
	if err != nil {
		return
	}
	id := r.PathValue("id")
	row, err := s.Store.GetWorkload(r.Context(), p.User.ClusterID, id)
	if err != nil || row == nil {
		writeErr(w, http.StatusNotFound, "workload not found")
		return
	}
	var req struct {
		Name string `json:"name"`
	}
	if err := readJSON(r, &req); err != nil || strings.TrimSpace(req.Name) == "" {
		writeErr(w, http.StatusBadRequest, "name is required")
		return
	}
	existing, err := s.Store.ListSnapshots(r.Context(), p.User.ClusterID, id)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	vol, pool, tip, err := s.bootVolumeLocator(r.Context(), p.User.ClusterID, *row)
	if err != nil {
		writeErr(w, statusFor(err), err.Error())
		return
	}
	if pool.BackendType == storage.BackendZFS {
		s.createZFSSnapshot(w, r, p, *row, *vol, *pool, strings.TrimSpace(req.Name), existing)
		return
	}
	if pool.BackendType == storage.BackendLVM {
		s.createLVMSnapshot(w, r, p, *row, *vol, *pool, strings.TrimSpace(req.Name), existing)
		return
	}
	if pool.BackendType == storage.BackendISCSI {
		writeErr(w, http.StatusUnprocessableEntity, iscsiSnapReason)
		return
	}
	if pool.BackendType == storage.BackendDistributed {
		writeErr(w, http.StatusUnprocessableEntity, distSnapReason)
		return
	}
	if row.Kind == lxc.KindSystemContainer || row.Kind != vmspec.KindVM {
		writeErr(w, http.StatusUnprocessableEntity, ctSnapshotReason)
		return
	}
	depth := overlayChainDepth(vol.BackendRef, existing)
	if depth >= qemu.ChainMax {
		writeErr(w, http.StatusConflict, "qcow2 overlay chain cap is 16")
		return
	}
	snapID := uuid.NewString()
	overlayRel := path.Join("volumes", storage.ClassVMDisk, vol.ID+"--"+snapID+".qcow2")
	overlay, jerr := storage.JoinUnder(pool.RootPath, overlayRel)
	if jerr != nil {
		writeErr(w, http.StatusBadRequest, "overlay locator is invalid")
		return
	}
	if s.VM == nil {
		writeErr(w, http.StatusBadGateway, "vm agent is unavailable")
		return
	}
	parentID := ""
	if depth > 0 && len(existing) > 0 {
		parentID = existing[len(existing)-1].ID
	}
	_, err = s.VM.SnapshotVM(r.Context(), qemu.OverlayRequest{
		Action: qemu.OverlayCreate, WorkloadID: id, OverlayPath: overlay, BackingPath: tip,
		ChainDepth: depth, ChainMax: qemu.ChainMax,
	})
	if err != nil {
		writeErr(w, http.StatusConflict, err.Error())
		return
	}
	if err := s.Store.UpdateVolumeLocator(r.Context(), p.User.ClusterID, vol.ID, overlayRel); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	snap := appdb.Snapshot{
		ID: snapID, ClusterID: p.User.ClusterID, WorkloadID: id, VolumeID: vol.ID,
		Name: strings.TrimSpace(req.Name), PurposeTag: purposeTag(req.Name),
		Mechanism: appdb.MechanismOverlay, BackendRef: vol.BackendRef, ParentID: parentID,
		ChainDepth: depth + 1, Status: appdb.SnapshotAvailable,
	}
	if err := s.Store.CreateSnapshot(r.Context(), snap); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.audit(r, p.User.ClusterID, p.User.ID, "snapshot.create", "ok", snap.ID)
	writeJSON(w, http.StatusCreated, snapshotJSON(snap))
}

func (s *Server) rollbackSnapshot(w http.ResponseWriter, r *http.Request) {
	p, err := s.requireAny(w, r, rbac.ComputeSnapshot, rbac.StorageSnapshot)
	if err != nil {
		return
	}
	if strings.TrimSpace(r.Header.Get("X-Nodal-Confirm")) != rollbackConfirm {
		writeErr(w, http.StatusConflict, "rollback requires X-Nodal-Confirm: rollback")
		return
	}
	snap, err := s.Store.GetSnapshot(r.Context(), p.User.ClusterID, r.PathValue("id"))
	if err != nil || snap == nil {
		writeErr(w, http.StatusNotFound, "snapshot not found")
		return
	}
	row, err := s.Store.GetWorkload(r.Context(), p.User.ClusterID, snap.WorkloadID)
	if err != nil || row == nil {
		writeErr(w, http.StatusNotFound, "workload not found")
		return
	}
	vol, pool, _, err := s.bootVolumeLocator(r.Context(), p.User.ClusterID, *row)
	if err != nil {
		writeErr(w, statusFor(err), err.Error())
		return
	}
	if pool.BackendType == storage.BackendZFS {
		if row.UnitActive || row.Status == qemu.StatusRunning || row.Status == qemu.StatusStarting {
			writeErr(w, http.StatusUnprocessableEntity, zfsRollbackRun)
			return
		}
		tag := s.snapshotTag(snap.BackendRef)
		if tag == "" {
			tag = snap.PurposeTag
		}
		res, zerr := s.zfs().ZFSPool(r.Context(), storage.ZFSOp{
			Action: "rollback", PoolID: pool.ID, Name: s.zfsPoolName(r.Context(), *pool),
			VolumeID: vol.ID, Snapshot: tag,
		})
		if zerr != nil {
			writeErr(w, http.StatusBadRequest, zerr.Error())
			return
		}
		if res.Status != storage.StatusAvailable {
			writeErr(w, http.StatusConflict, firstNonEmpty(res.Reason, "zfs rollback failed"))
			return
		}
		s.audit(r, p.User.ClusterID, p.User.ID, "snapshot.rollback", "ok", snap.ID)
		writeJSON(w, http.StatusOK, snapshotJSON(*snap))
		return
	}
	if pool.BackendType == storage.BackendLVM {
		if row.UnitActive || row.Status == qemu.StatusRunning || row.Status == qemu.StatusStarting {
			writeErr(w, http.StatusUnprocessableEntity, lvmRollbackRun)
			return
		}
		tag := snap.PurposeTag
		if tag == "" {
			tag = s.snapshotTag(snap.BackendRef)
		}
		res, lerr := s.lvm().LVMPool(r.Context(), storage.LVMOp{
			Action: "rollback", PoolID: pool.ID, Name: s.lvmVGName(r.Context(), *pool),
			VolumeID: vol.ID, Snapshot: tag,
		})
		if lerr != nil {
			writeErr(w, http.StatusBadRequest, lerr.Error())
			return
		}
		if res.Status != storage.StatusAvailable {
			writeErr(w, http.StatusConflict, firstNonEmpty(res.Reason, "lvm rollback failed"))
			return
		}
		s.audit(r, p.User.ClusterID, p.User.ID, "snapshot.rollback", "ok", snap.ID)
		writeJSON(w, http.StatusOK, snapshotJSON(*snap))
		return
	}
	if row.UnitActive || row.Status == qemu.StatusRunning || row.Status == qemu.StatusStarting {
		writeErr(w, http.StatusUnprocessableEntity, overlayRollbackRun)
		return
	}
	newID := uuid.NewString()
	overlayRel := path.Join("volumes", storage.ClassVMDisk, vol.ID+"--rb-"+newID+".qcow2")
	overlay, jerr := storage.JoinUnder(pool.RootPath, overlayRel)
	if jerr != nil {
		writeErr(w, http.StatusBadRequest, "overlay locator is invalid")
		return
	}
	backing, jerr := storage.JoinUnder(pool.RootPath, snap.BackendRef)
	if jerr != nil {
		writeErr(w, http.StatusBadRequest, "snapshot locator is invalid")
		return
	}
	if s.VM == nil {
		writeErr(w, http.StatusBadGateway, "vm agent is unavailable")
		return
	}
	_, err = s.VM.SnapshotVM(r.Context(), qemu.OverlayRequest{
		Action: qemu.OverlayRollback, WorkloadID: row.ID, OverlayPath: overlay, BackingPath: backing,
	})
	if err != nil {
		writeErr(w, http.StatusConflict, err.Error())
		return
	}
	if err := s.Store.UpdateVolumeLocator(r.Context(), p.User.ClusterID, vol.ID, overlayRel); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.audit(r, p.User.ClusterID, p.User.ID, "snapshot.rollback", "ok", snap.ID)
	writeJSON(w, http.StatusOK, snapshotJSON(*snap))
}

func (s *Server) flattenSnapshots(w http.ResponseWriter, r *http.Request) {
	p, err := s.requireAny(w, r, rbac.ComputeSnapshot, rbac.StorageSnapshot)
	if err != nil {
		return
	}
	if strings.TrimSpace(r.Header.Get("X-Nodal-Confirm")) != flattenConfirm {
		writeErr(w, http.StatusConflict, "flatten requires X-Nodal-Confirm: flatten")
		return
	}
	id := r.PathValue("id")
	row, err := s.Store.GetWorkload(r.Context(), p.User.ClusterID, id)
	if err != nil || row == nil {
		writeErr(w, http.StatusNotFound, "workload not found")
		return
	}
	vol, pool, tip, err := s.bootVolumeLocator(r.Context(), p.User.ClusterID, *row)
	if err != nil {
		writeErr(w, statusFor(err), err.Error())
		return
	}
	if pool.BackendType == storage.BackendZFS {
		writeErr(w, http.StatusUnprocessableEntity, zfsFlattenReason)
		return
	}
	if pool.BackendType == storage.BackendLVM {
		writeErr(w, http.StatusUnprocessableEntity, lvmFlattenReason)
		return
	}
	if pool.BackendType == storage.BackendISCSI {
		writeErr(w, http.StatusUnprocessableEntity, iscsiSnapReason)
		return
	}
	if pool.BackendType == storage.BackendDistributed {
		writeErr(w, http.StatusUnprocessableEntity, distSnapReason)
		return
	}
	if row.Kind != vmspec.KindVM {
		writeErr(w, http.StatusUnprocessableEntity, ctSnapshotReason)
		return
	}
	if row.UnitActive || row.Status == qemu.StatusRunning || row.Status == qemu.StatusStarting {
		writeErr(w, http.StatusUnprocessableEntity, overlayFlattenRun)
		return
	}
	flatID := uuid.NewString()
	flatRel := path.Join("volumes", storage.ClassVMDisk, vol.ID+"--flat-"+flatID+".qcow2")
	flat, jerr := storage.JoinUnder(pool.RootPath, flatRel)
	if jerr != nil {
		writeErr(w, http.StatusBadRequest, "flatten locator is invalid")
		return
	}
	if s.VM == nil {
		writeErr(w, http.StatusBadGateway, "vm agent is unavailable")
		return
	}
	_, err = s.VM.SnapshotVM(r.Context(), qemu.OverlayRequest{
		Action: qemu.OverlayFlatten, WorkloadID: id, OverlayPath: flat, BackingPath: tip,
	})
	if err != nil {
		writeErr(w, http.StatusConflict, err.Error())
		return
	}
	if err := s.Store.UpdateVolumeLocator(r.Context(), p.User.ClusterID, vol.ID, flatRel); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.audit(r, p.User.ClusterID, p.User.ID, "snapshot.flatten", "ok", id)
	s.listSnapshots(w, r)
}

func (s *Server) bootVolumeLocator(ctx context.Context, clusterID string, row appdb.Workload) (*appdb.Volume, *appdb.StoragePool, string, error) {
	disks, err := s.Store.ListWorkloadDisks(ctx, clusterID, row.ID)
	if err != nil {
		return nil, nil, "", err
	}
	var volID string
	for _, d := range disks {
		if d.Role == vmspec.DiskRoleBoot || volID == "" {
			volID = d.VolumeID
			if d.Role == vmspec.DiskRoleBoot {
				break
			}
		}
	}
	if volID == "" {
		return nil, nil, "", errConflict("workload has no boot volume")
	}
	vol, err := s.Store.GetVolume(ctx, clusterID, volID)
	if err != nil || vol == nil {
		return nil, nil, "", errNotFound("volume is not found")
	}
	if vol.Status != storage.StatusAvailable && vol.Status != storage.StatusWarning {
		return nil, nil, "", errConflict("storage is unavailable")
	}
	pool, err := s.Store.GetStoragePool(ctx, clusterID, vol.PoolID)
	if err != nil || pool == nil {
		return nil, nil, "", errNotFound("storage pool is not found")
	}
	if pool.Status != storage.StatusAvailable && pool.Status != storage.StatusWarning {
		return nil, nil, "", errConflict("storage pool is unavailable")
	}
	tip, err := storage.HostVolumePath(pool.BackendType, pool.RootPath, vol.BackendRef)
	if err != nil {
		return nil, nil, "", errConflict("volume locator is invalid")
	}
	return vol, pool, tip, nil
}

func purposeTag(name string) string {
	var b strings.Builder
	b.WriteString("ndl-user-")
	n := 0
	for _, r := range strings.ToLower(strings.TrimSpace(name)) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '-' {
			b.WriteRune(r)
			n++
		} else if r == ' ' || r == '_' {
			b.WriteByte('-')
			n++
		}
		if n >= 40 {
			break
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "ndl-user" {
		return "ndl-user-snapshot"
	}
	return out
}

func overlayChainReset(backendRef string) bool {
	base := path.Base(backendRef)
	return strings.Contains(base, "--flat-") || strings.Contains(base, "--rb-")
}

// overlayChainLink is a qcow2 overlay file in the live chain. Flatten and
// rollback tips start a new chain. A catalog BackendRef of the original boot
// image is not an overlay.
func overlayChainLink(backendRef string) bool {
	base := path.Base(backendRef)
	if overlayChainReset(base) {
		return false
	}
	return strings.Contains(base, "--") || strings.HasSuffix(base, "-tmpl.qcow2")
}

func overlayChainDepth(backendRef string, items []appdb.Snapshot) int {
	if overlayChainReset(backendRef) {
		return 0
	}
	seen := map[string]struct{}{}
	add := func(ref string) {
		if !overlayChainLink(ref) {
			return
		}
		seen[path.Base(ref)] = struct{}{}
	}
	add(backendRef)
	for i := len(items) - 1; i >= 0; i-- {
		if overlayChainReset(items[i].BackendRef) {
			break
		}
		add(items[i].BackendRef)
	}
	return len(seen)
}
