package httpapi

import (
	"net/http"

	"github.com/no-dal/ndl-ce/internal/appdb"
	"github.com/no-dal/ndl-ce/internal/rbac"
	"github.com/no-dal/ndl-ce/internal/storage"
)

// deleteSnapshot removes one snapshot.
//
//   - ZFS: the snapshot is destroyed.
//   - Directory (qcow2 overlays): only the newest snapshot can go. Its
//     overlay is merged back into the image it froze, live or offline, so
//     the disk chain gets shorter and no data is lost; the workload keeps
//     every change made since.
//   - LVM: not supported yet, said plainly.
func (s *Server) deleteSnapshot(w http.ResponseWriter, r *http.Request) {
	p, err := s.requireAny(w, r, rbac.ComputeSnapshot, rbac.StorageSnapshot)
	if err != nil {
		return
	}
	ctx := r.Context()
	snap, err := s.Store.GetSnapshot(ctx, p.User.ClusterID, r.PathValue("id"))
	if err != nil || snap == nil {
		writeErr(w, http.StatusNotFound, "snapshot not found")
		return
	}
	row, err := s.Store.GetWorkload(ctx, p.User.ClusterID, snap.WorkloadID)
	if err != nil || row == nil {
		// The workload is gone; only the record is left.
		_ = s.Store.DeleteSnapshot(ctx, p.User.ClusterID, snap.ID)
		s.audit(r, p.User.ClusterID, p.User.ID, "snapshot.delete", "ok", snap.ID)
		writeJSON(w, http.StatusOK, map[string]any{"id": snap.ID, "deleted": true})
		return
	}
	vol, pool, _, err := s.bootVolumeLocator(ctx, p.User.ClusterID, *row)
	if err != nil {
		writeErr(w, statusFor(err), err.Error())
		return
	}
	switch {
	case pool.BackendType == storage.BackendZFS:
		tag := s.snapshotTag(snap.BackendRef)
		if tag == "" {
			tag = snap.PurposeTag
		}
		res, zerr := s.zfs().ZFSPool(ctx, storage.ZFSOp{
			Action: "destroy-snapshot", PoolID: pool.ID, Name: s.zfsPoolName(ctx, *pool),
			VolumeID: vol.ID, Snapshot: tag,
		})
		if zerr != nil {
			writeErr(w, http.StatusBadRequest, zerr.Error())
			return
		}
		if res.Status == storage.StatusFailed || res.Status == storage.StatusUnavailable {
			writeErr(w, http.StatusConflict, firstNonEmpty(res.Reason, "the ZFS snapshot could not be destroyed"))
			return
		}
		if err := s.Store.DeleteSnapshot(ctx, p.User.ClusterID, snap.ID); err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
	case snap.Mechanism == appdb.MechanismOverlay && pool.BackendType == storage.BackendDirectory:
		layers := s.backupLayers(ctx, p.User.ClusterID, *row, vol.ID)
		if len(layers) == 0 || layers[len(layers)-1].ID != snap.ID {
			writeErr(w, http.StatusConflict, "only the newest snapshot can be deleted; delete the newer ones first, or use Flatten to merge the whole chain")
			return
		}
		if err := s.commitBackupLayer(ctx, p.User.ClusterID, *row, *snap); err != nil {
			writeErr(w, statusFor(err), "the snapshot could not be merged back into the disk: "+err.Error())
			return
		}
	default:
		writeErr(w, http.StatusUnprocessableEntity, "deleting snapshots on this storage type is not supported yet")
		return
	}
	s.audit(r, p.User.ClusterID, p.User.ID, "snapshot.delete", "ok", snap.ID)
	writeJSON(w, http.StatusOK, map[string]any{"id": snap.ID, "deleted": true})
}
