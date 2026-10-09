package httpapi

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/no-dal/ndl-ce/internal/appdb"
	"github.com/no-dal/ndl-ce/internal/rbac"
)

const deleteBackupConfirm = "delete-backup"

// removeArtifact deletes one backup's data and then its record. With
// forget, the record also goes when the data cannot be reached, for example
// on a test target whose bucket no longer exists; the caller reports that.
func (s *Server) removeArtifact(ctx context.Context, clusterID string, a appdb.BackupArtifact, forget bool) (dataGone bool, err error) {
	var tgt *appdb.BackupTarget
	if run, _ := s.Store.GetBackupRun(ctx, clusterID, a.RunID); run != nil && run.TargetID != "" {
		tgt, _ = s.Store.GetBackupTarget(ctx, clusterID, run.TargetID)
	}
	if derr := s.deleteArtifactData(ctx, clusterID, a, tgt); derr != nil {
		if !forget {
			return false, derr
		}
		s.recordStorageEvent("delete", false, a.WorkloadID, fmt.Sprintf("backup %s: its data could not be removed (%v); the record was removed as asked", a.ID, derr))
	} else {
		dataGone = true
	}
	if err := s.Store.DeleteBackupArtifact(ctx, clusterID, a.ID); err != nil {
		return dataGone, err
	}
	return dataGone, nil
}

// deleteBackupArtifact deletes one backup. Protected backups are refused
// until unprotected. ?forget=true removes the record even when its data
// cannot be reached.
func (s *Server) deleteBackupArtifact(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.BackupCreate)
	if err != nil {
		return
	}
	if strings.TrimSpace(r.Header.Get(confirmHeader)) != deleteBackupConfirm {
		writeErr(w, http.StatusConflict, "deleting a backup requires X-Nodal-Confirm: "+deleteBackupConfirm)
		return
	}
	ctx := r.Context()
	a, err := s.Store.GetBackupArtifact(ctx, p.User.ClusterID, r.PathValue("id"))
	if err != nil || a == nil {
		writeErr(w, http.StatusNotFound, "backup not found")
		return
	}
	if protected, _ := s.Store.ListProtectedBackupArtifacts(ctx, p.User.ClusterID); protected[a.ID] {
		writeErr(w, http.StatusConflict, "this backup is protected; unprotect it first")
		return
	}
	gone, err := s.removeArtifact(ctx, p.User.ClusterID, *a, r.URL.Query().Get("forget") == "true")
	if err != nil {
		writeErr(w, http.StatusBadGateway, "the backup could not be removed: "+err.Error())
		return
	}
	s.audit(r, p.User.ClusterID, p.User.ID, "backup.delete", "ok", a.ID)
	s.reclaimLater("deleted a backup")
	writeJSON(w, http.StatusOK, map[string]any{"id": a.ID, "deleted": true, "data_removed": gone})
}

// purgeDeletedWorkloadBackups deletes every backup whose workload no longer
// exists, or only those of the listed workload ids. Protected backups stay.
// Records whose data cannot be reached (for example on a test target that
// is gone) are removed too, and counted.
func (s *Server) purgeDeletedWorkloadBackups(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.BackupCreate)
	if err != nil {
		return
	}
	if strings.TrimSpace(r.Header.Get(confirmHeader)) != deleteBackupConfirm {
		writeErr(w, http.StatusConflict, "deleting backups requires X-Nodal-Confirm: "+deleteBackupConfirm)
		return
	}
	var req struct {
		WorkloadIDs []string `json:"workload_ids"`
	}
	_ = readJSON(r, &req)
	only := map[string]bool{}
	for _, id := range req.WorkloadIDs {
		only[strings.TrimSpace(id)] = true
	}
	ctx := r.Context()
	clusterID := p.User.ClusterID
	workloads, err := s.Store.ListWorkloads(ctx, clusterID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	live := map[string]bool{}
	for _, wl := range workloads {
		live[wl.ID] = true
	}
	arts, err := s.Store.ListBackupArtifacts(ctx, clusterID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	protected, _ := s.Store.ListProtectedBackupArtifacts(ctx, clusterID)
	deleted, unreachable, kept := 0, 0, 0
	for _, a := range arts {
		wid := a.WorkloadID
		if wid == "" {
			if run, _ := s.Store.GetBackupRun(ctx, clusterID, a.RunID); run != nil {
				wid = run.WorkloadID
			}
		}
		if wid == "" || live[wid] || (len(only) > 0 && !only[wid]) {
			continue
		}
		if protected[a.ID] {
			kept++
			continue
		}
		gone, err := s.removeArtifact(ctx, clusterID, a, true)
		if err != nil {
			s.recordStorageEvent("delete", false, wid, "backup "+a.ID+": "+err.Error())
			continue
		}
		deleted++
		if !gone {
			unreachable++
		}
	}
	s.audit(r, clusterID, p.User.ID, "backup.purge_deleted_workloads", "ok", fmt.Sprintf("%d deleted, %d without reachable data, %d protected kept", deleted, unreachable, kept))
	s.reclaimLater("deleted backups of deleted workloads")
	writeJSON(w, http.StatusOK, map[string]any{"deleted": deleted, "data_unreachable": unreachable, "protected_kept": kept})
}

// deleteBackupTarget deletes a target. With ?delete_backups=true and the
// confirmation header, its backups are deleted first (records whose data
// cannot be reached are removed too), so a test target can be cleaned up in
// one step. Without it, a target that still has backups is refused as before.
func (s *Server) deleteBackupTarget(plain http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("delete_backups") != "true" {
			plain(w, r)
			return
		}
		p, err := s.require(w, r, rbac.BackupCreate)
		if err != nil {
			return
		}
		if strings.TrimSpace(r.Header.Get(confirmHeader)) != deleteBackupConfirm {
			writeErr(w, http.StatusConflict, "deleting a target with its backups requires X-Nodal-Confirm: "+deleteBackupConfirm)
			return
		}
		ctx := r.Context()
		clusterID := p.User.ClusterID
		id := r.PathValue("id")
		runs, _ := s.Store.ListBackupRuns(ctx, clusterID)
		onTarget := map[string]bool{}
		for _, run := range runs {
			if run.TargetID == id {
				onTarget[run.ID] = true
			}
		}
		arts, _ := s.Store.ListBackupArtifacts(ctx, clusterID)
		protected, _ := s.Store.ListProtectedBackupArtifacts(ctx, clusterID)
		for _, a := range arts {
			if !onTarget[a.RunID] {
				continue
			}
			if protected[a.ID] {
				writeErr(w, http.StatusConflict, "a backup on this target is protected; unprotect it first")
				return
			}
			if _, err := s.removeArtifact(ctx, clusterID, a, true); err != nil {
				writeErr(w, http.StatusInternalServerError, "backup "+a.ID+" could not be removed: "+err.Error())
				return
			}
		}
		s.reclaimLater("deleted a backup target")
		plain(w, r)
	}
}

// reclaimLater runs a cleanup pass in the background so deleted backups free
// their space without holding the request open.
func (s *Server) reclaimLater(reason string) {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Hour)
		defer cancel()
		_, _ = s.runBackupMaintenance(ctx, reason)
	}()
}
