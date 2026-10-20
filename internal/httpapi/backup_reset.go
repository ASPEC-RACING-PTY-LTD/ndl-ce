package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/no-dal/ndl-ce/internal/backup"
	"github.com/no-dal/ndl-ce/internal/backuphost"
	"github.com/no-dal/ndl-ce/internal/qemu"
	"github.com/no-dal/ndl-ce/internal/rbac"
)

// backupResetConfirm must be sent as X-Nodal-Confirm to reset backups.
const backupResetConfirm = "backup-reset"

// deleteBackupPolicyHistory clears a policy's run history. Runs a restore
// point still refers to stay, so no backup loses its record.
func (s *Server) deleteBackupPolicyHistory(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.BackupCreate)
	if err != nil {
		return
	}
	pol, err := s.Store.GetBackupPolicy(r.Context(), p.User.ClusterID, r.PathValue("id"))
	if err != nil || pol == nil {
		writeErr(w, http.StatusNotFound, "backup policy not found")
		return
	}
	removed, kept, err := s.Store.DeleteBackupRunsWithoutArtifacts(r.Context(), p.User.ClusterID, pol.ID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.audit(r, p.User.ClusterID, p.User.ID, "backup.policy.history_delete", "ok", pol.ID)
	writeJSON(w, http.StatusOK, map[string]any{"removed": removed, "kept": kept})
}

// resetBackups clean-slates the backup system: every running backup stops,
// the local repository is emptied (its key and target settings stay), older
// format backups are deleted, and every run, restore point and upload
// record goes. Policies, targets and their credentials stay. Objects already
// in a cloud bucket are not touched; Wipe on the target removes those.
func (s *Server) resetBackups(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.BackupCreate)
	if err != nil {
		return
	}
	if strings.TrimSpace(r.Header.Get(confirmHeader)) != backupResetConfirm {
		writeErr(w, http.StatusConflict, "a full backup reset requires X-Nodal-Confirm: "+backupResetConfirm)
		return
	}
	ctx := context.WithoutCancel(r.Context())
	clusterID := p.User.ClusterID
	stopped := s.suspendBackups("a full backup reset is running", backupDrainWait)
	defer s.resumeBackups()

	if s.Backup != nil {
		raw, _ := json.Marshal(backuphost.Request{Action: backuphost.ActionReset})
		if _, err := s.Backup.CopyBackup(ctx, qemu.BackupV2Reset, "", string(raw)); err != nil {
			writeErr(w, http.StatusConflict, "the backup repository was not reset: "+err.Error())
			return
		}
	}
	// Older format backups keep their data outside the repository.
	legacy := 0
	if arts, err := s.Store.ListBackupArtifacts(ctx, clusterID); err == nil {
		runs, _ := s.Store.ListBackupRuns(ctx, clusterID)
		targetOf := map[string]string{}
		for _, run := range runs {
			targetOf[run.ID] = run.TargetID
		}
		for _, a := range arts {
			if a.Format == backup.Format {
				continue
			}
			tgt, _ := s.Store.GetBackupTarget(ctx, clusterID, targetOf[a.RunID])
			if s.deleteArtifactData(ctx, clusterID, a, tgt) == nil {
				legacy++
			}
		}
	}
	if err := s.Store.ResetBackupRecords(ctx, clusterID); err != nil {
		writeErr(w, http.StatusInternalServerError, "the repository was emptied but its records were not: "+err.Error())
		return
	}
	s.storageMu.Lock()
	s.storageEvents = nil
	s.storageMu.Unlock()
	for _, pol := range s.listPoliciesQuiet(ctx, clusterID) {
		s.clearPolicyStopped(pol)
	}
	s.audit(r, clusterID, p.User.ID, "backup.reset", "ok", "")
	writeJSON(w, http.StatusOK, map[string]any{"stopped": stopped, "legacy_removed": legacy})
}

func (s *Server) listPoliciesQuiet(ctx context.Context, clusterID string) []string {
	pols, _ := s.Store.ListBackupPolicies(ctx, clusterID)
	ids := make([]string, 0, len(pols))
	for _, p := range pols {
		ids = append(ids, p.ID)
	}
	return ids
}
