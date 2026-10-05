package httpapi

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/no-dal/ndl-ce/internal/appdb"
	"github.com/no-dal/ndl-ce/internal/lxc"
	"github.com/no-dal/ndl-ce/internal/rbac"
	"github.com/no-dal/ndl-ce/internal/storage"
)

// Deletion for configurable resources that had no way to be removed.
//
// Every delete checks what depends on the resource first and refuses with
// 409 and a plain reason instead of orphaning it. The database also refuses
// a delete that would leave a dependent row behind; that is reported the
// same way.

// errInUse is a dependency that blocks a delete.
type errInUse string

func (e errInUse) Error() string { return string(e) }

func plural(n int, one, many string) string {
	if n == 1 {
		return fmt.Sprintf("1 %s", one)
	}
	return fmt.Sprintf("%d %s", n, many)
}

func isForeignKeyError(err error) bool {
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "foreign key") || strings.Contains(msg, "23503")
}

// deleteConfig serves DELETE for a simple configuration record.
func (s *Server) deleteConfig(kind appdb.ConfigKind, perm, action string, check func(ctx context.Context, clusterID, id string) error) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p, err := s.require(w, r, perm)
		if err != nil {
			return
		}
		id := r.PathValue("id")
		if check != nil {
			if err := check(r.Context(), p.User.ClusterID, id); err != nil {
				var inUse errInUse
				if errors.As(err, &inUse) {
					s.audit(r, p.User.ClusterID, p.User.ID, action, "denied", inUse.Error())
					writeErr(w, http.StatusConflict, inUse.Error())
					return
				}
				writeErr(w, http.StatusNotFound, err.Error())
				return
			}
		}
		if err := s.Store.DeleteConfig(r.Context(), kind, p.User.ClusterID, id); err != nil {
			switch {
			case errors.Is(err, appdb.ErrConfigNotFound):
				writeErr(w, http.StatusNotFound, "not found")
			case isForeignKeyError(err):
				writeErr(w, http.StatusConflict, "still in use by other resources; remove those first")
			default:
				writeErr(w, http.StatusInternalServerError, err.Error())
			}
			return
		}
		s.audit(r, p.User.ClusterID, p.User.ID, action, "ok", id)
		w.WriteHeader(http.StatusNoContent)
	}
}

// checkPoolUnused refuses to forget a pool that still holds volumes or images.
func (s *Server) checkPoolUnused(ctx context.Context, clusterID, id string) error {
	pool, err := s.Store.GetStoragePool(ctx, clusterID, id)
	if err != nil || pool == nil {
		return errors.New("storage pool not found")
	}
	vols, err := s.Store.ListVolumes(ctx, clusterID, id)
	if err != nil {
		return errInUse("could not check the pool's volumes")
	}
	if len(vols) > 0 {
		return errInUse(fmt.Sprintf("%s still on this pool. Delete the workloads or volumes that use it first.", plural(len(vols), "volume is", "volumes are")))
	}
	items, err := s.Store.ListLibraryItems(ctx, clusterID, id)
	if err != nil {
		return errInUse("could not check the pool's images")
	}
	if len(items) > 0 {
		return errInUse(fmt.Sprintf("%s still stored on this pool.", plural(len(items), "image is", "images are")))
	}
	return nil
}

// checkBackupTargetUnused refuses to remove a target that a policy uses or
// that still holds restorable backups.
func (s *Server) checkBackupTargetUnused(ctx context.Context, clusterID, id string) error {
	t, err := s.Store.GetBackupTarget(ctx, clusterID, id)
	if err != nil || t == nil {
		return errors.New("backup target not found")
	}
	policies, err := s.Store.ListBackupPolicies(ctx, clusterID)
	if err != nil {
		return errInUse("could not check backup policies")
	}
	var names []string
	for _, pol := range policies {
		if pol.TargetID == id {
			names = append(names, pol.Name)
		}
	}
	if len(names) > 0 {
		return errInUse("used by backup policies: " + strings.Join(names, ", ") + ". Delete or retarget them first.")
	}
	runs, err := s.Store.ListBackupRuns(ctx, clusterID)
	if err != nil {
		return errInUse("could not check backups on this target")
	}
	onTarget := map[string]bool{}
	for _, run := range runs {
		if run.TargetID == id {
			onTarget[run.ID] = true
		}
	}
	artifacts, err := s.Store.ListBackupArtifacts(ctx, clusterID)
	if err != nil {
		return errInUse("could not check backups on this target")
	}
	n := 0
	for _, a := range artifacts {
		if onTarget[a.RunID] {
			n++
		}
	}
	if n > 0 {
		return errInUse(fmt.Sprintf("%s on this target would no longer be restorable. Let retention expire them first.", plural(n, "backup", "backups")))
	}
	return nil
}

// deleteVolume destroys a volume that no workload uses.
func (s *Server) deleteVolume(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.StorageVolumeCreate)
	if err != nil {
		return
	}
	ctx := r.Context()
	clusterID := p.User.ClusterID
	vol, err := s.Store.GetVolume(ctx, clusterID, r.PathValue("id"))
	if err != nil || vol == nil {
		writeErr(w, http.StatusNotFound, "volume not found")
		return
	}
	if reason := s.volumeInUse(ctx, clusterID, *vol); reason != "" {
		s.audit(r, clusterID, p.User.ID, "storage.volume.delete", "denied", reason)
		writeErr(w, http.StatusConflict, reason)
		return
	}
	if s.Storage != nil {
		pool, _ := s.Store.GetStoragePool(ctx, clusterID, vol.PoolID)
		root := ""
		if pool != nil {
			root = pool.RootPath
		}
		if err := s.Storage.DestroyDirectoryVolume(ctx, storage.CreateVolumeRequest{
			VolumeID: vol.ID, PoolID: vol.PoolID, RootPath: root, Class: vol.Class,
			Format: vol.Format, BackendRef: vol.BackendRef, Size: vol.SizeBytes,
			Owner: storage.VolumeOwnerName, OwnerKind: firstNonEmpty(vol.OwnerKind, storage.VolumeKindOperator),
			JobID: vol.OwnerJobID,
		}, storage.PoolHint{PoolID: vol.PoolID, BackendType: vol.BackendType, RootPath: root}); err != nil {
			writeErr(w, http.StatusBadGateway, "the agent could not remove the volume: "+err.Error())
			return
		}
	}
	if err := s.Store.DeleteVolume(ctx, clusterID, vol.ID); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.audit(r, clusterID, p.User.ID, "storage.volume.delete", "ok", vol.ID)
	s.emitEvent(ctx, clusterID, vol.NodeID, "storage.volume.deleted", map[string]string{"volume_id": vol.ID})
	w.WriteHeader(http.StatusNoContent)
}

// volumeInUse names what still uses a volume, or returns "".
func (s *Server) volumeInUse(ctx context.Context, clusterID string, vol appdb.Volume) string {
	if vol.OwnerKind == storage.VolumeKindMigration {
		return "this volume belongs to a migration job"
	}
	wls, err := s.Store.ListWorkloads(ctx, clusterID)
	if err != nil {
		return "could not check which workloads use this volume"
	}
	for _, wl := range wls {
		disks, err := s.Store.ListWorkloadDisks(ctx, clusterID, wl.ID)
		if err != nil {
			return "could not check which workloads use this volume"
		}
		for _, d := range disks {
			if d.VolumeID == vol.ID {
				return fmt.Sprintf("attached to %s. Detach it or delete the workload first.", wl.Name)
			}
		}
	}
	// Pool folders mounted into system containers are only recorded in the
	// container's mount list, so ask the agent.
	if !strings.HasPrefix(vol.BackendRef, "/") {
		return ""
	}
	for _, wl := range wls {
		if wl.Kind != lxc.KindSystemContainer {
			continue
		}
		if s.Workloads == nil {
			return "could not check whether system containers mount this volume"
		}
		res, err := s.Workloads.LifecycleCT(ctx, lxc.LifecycleRequest{WorkloadID: wl.ID, Action: lxc.ActionMountsGet})
		if err != nil {
			return fmt.Sprintf("could not check whether %s mounts this volume", wl.Name)
		}
		for _, m := range res.Mounts {
			if m.Source == vol.BackendRef || strings.HasPrefix(m.Source, strings.TrimSuffix(vol.BackendRef, "/")+"/") {
				return fmt.Sprintf("mounted in %s at %s. Unmount it on the container's Storage page first.", wl.Name, m.Target)
			}
		}
	}
	return ""
}
