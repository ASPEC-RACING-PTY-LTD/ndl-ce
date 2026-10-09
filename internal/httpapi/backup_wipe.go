package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/no-dal/ndl-ce/internal/appdb"
	"github.com/no-dal/ndl-ce/internal/backup"
	"github.com/no-dal/ndl-ce/internal/backuphost"
	"github.com/no-dal/ndl-ce/internal/qemu"
	"github.com/no-dal/ndl-ce/internal/rbac"
)

const wipeTargetConfirm = "wipe-target"

// wipeBackupTarget deletes every No-dal backup object in an object-storage
// target so backups can start again from nothing. It needs the confirmation
// header and the target's exact name. Backups still held locally stay and
// become local-only; backups that only existed in the target are removed from
// the list because their data is gone.
func (s *Server) wipeBackupTarget(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.BackupCreate)
	if err != nil {
		return
	}
	if strings.TrimSpace(r.Header.Get(confirmHeader)) != wipeTargetConfirm {
		writeErr(w, http.StatusConflict, "wiping a target requires X-Nodal-Confirm: "+wipeTargetConfirm)
		return
	}
	var req struct {
		ConfirmName string `json:"confirm_name"`
	}
	if err := readJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "confirm_name is required")
		return
	}
	ctx := r.Context()
	clusterID := p.User.ClusterID
	tgt, err := s.Store.GetBackupTarget(ctx, clusterID, r.PathValue("id"))
	if err != nil || tgt == nil {
		writeErr(w, http.StatusNotFound, "backup target not found")
		return
	}
	if !isObjectBackupKind(tgt.Kind) {
		writeErr(w, http.StatusUnprocessableEntity, "only cloud object storage targets can be wiped here")
		return
	}
	if strings.TrimSpace(req.ConfirmName) != tgt.Name {
		writeErr(w, http.StatusConflict, "type the target name exactly to confirm")
		return
	}
	if s.Backup == nil {
		writeErr(w, http.StatusBadGateway, "backup agent is unavailable")
		return
	}
	raw, _ := json.Marshal(backuphost.Request{Action: backuphost.ActionWipeRemote, Target: s.v2TargetSpec(ctx, *tgt)})
	res, err := s.Backup.CopyBackup(ctx, qemu.BackupV2WipeRemote, "", string(raw))
	if err != nil {
		s.recordStorageEvent("wipe", false, "", tgt.Name+": "+err.Error())
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	var out backuphost.Result
	_ = json.Unmarshal([]byte(res.Extra), &out)
	local := map[string]bool{}
	for _, pt := range out.Points {
		local[pt.BackupID] = true
	}

	runs, _ := s.Store.ListBackupRuns(ctx, clusterID)
	onTarget := map[string]bool{}
	for _, run := range runs {
		if run.TargetID == tgt.ID {
			onTarget[run.ID] = true
		}
	}
	arts, _ := s.Store.ListBackupArtifacts(ctx, clusterID)
	removed, localOnly := 0, 0
	for _, a := range arts {
		if !onTarget[a.RunID] {
			continue
		}
		if a.Format == backup.Format && a.BackupID != "" && local[a.BackupID] {
			if a.RemoteState != string(backup.RemoteNone) {
				a.RemoteState = string(backup.RemoteNone)
				_ = s.Store.UpdateBackupArtifact(ctx, a)
			}
			localOnly++
			continue
		}
		if a.Format == backup.Format || a.ObjectKey != "" || strings.HasPrefix(a.Locator, "s3://") {
			if s.Store.DeleteBackupArtifact(ctx, clusterID, a.ID) == nil {
				removed++
			}
		}
	}
	wipe := out.Wipe
	if wipe == nil {
		wipe = &backuphost.WipeResult{}
	}
	msg := fmt.Sprintf("%s wiped: %d object(s), %s deleted; %d backup record(s) removed, %d kept as local-only",
		tgt.Name, wipe.ObjectsDeleted, humanBytes(wipe.BytesDeleted), removed, localOnly)
	s.recordStorageEvent("wipe", true, "", msg)
	s.audit(r, clusterID, p.User.ID, "backup.target.wipe", "ok", tgt.ID)
	_ = s.Store.UpdateBackupTargetStatus(ctx, clusterID, tgt.ID, appdb.BackupUntested)
	writeJSON(w, http.StatusOK, map[string]any{
		"target_id": tgt.ID, "objects_deleted": wipe.ObjectsDeleted, "bytes_deleted": wipe.BytesDeleted,
		"records_removed": removed, "records_local_only": localOnly,
	})
}
