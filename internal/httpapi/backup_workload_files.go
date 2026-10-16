package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/no-dal/ndl-ce/internal/appdb"
	"github.com/no-dal/ndl-ce/internal/backuphost"
	"github.com/no-dal/ndl-ce/internal/lxc"
	"github.com/no-dal/ndl-ce/internal/qemu"
	"github.com/no-dal/ndl-ce/internal/rbac"
)

// workloadRootfs is the host folder holding a system container's files.
func (s *Server) workloadRootfs(ctx context.Context, clusterID, id string) (*appdb.Workload, string, error) {
	wl, err := s.Store.GetWorkload(ctx, clusterID, id)
	if err != nil || wl == nil {
		return nil, "", errNotFound("workload not found")
	}
	if wl.Kind != lxc.KindSystemContainer {
		return wl, "", errUnprocessable("only system containers can be browsed and given backup rules")
	}
	if s.Backup == nil {
		return wl, "", errUnavailable("backup agent is unavailable")
	}
	_, _, rootfs, err := s.bootVolumeLocator(ctx, clusterID, *wl)
	if err != nil {
		return wl, "", err
	}
	return wl, rootfs, nil
}

func (s *Server) backupHostCall(ctx context.Context, action, rootfs string, req backuphost.Request) (backuphost.Result, error) {
	req.Action = action
	raw, _ := json.Marshal(req)
	res, err := s.Backup.CopyBackup(ctx, action, rootfs, string(raw))
	var out backuphost.Result
	if err != nil {
		return out, err
	}
	if res.Extra != "" {
		_ = json.Unmarshal([]byte(res.Extra), &out)
	}
	return out, nil
}

// getWorkloadBackupFiles lists one folder of a workload with sizes, short
// labels and the backup rule that applies to each item.
func (s *Server) getWorkloadBackupFiles(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.BackupRead)
	if err != nil {
		return
	}
	wl, rootfs, err := s.workloadRootfs(r.Context(), p.User.ClusterID, r.PathValue("id"))
	if err != nil {
		writeErr(w, statusFor(err), err.Error())
		return
	}
	dir := strings.TrimSpace(r.URL.Query().Get("path"))
	if dir == "" {
		dir = "/"
	}
	res, err := s.backupHostCall(r.Context(), qemu.BackupV2Browse, rootfs, backuphost.Request{WorkloadID: wl.ID, WorkloadName: wl.Name, Path: dir})
	if err != nil {
		writeErr(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	if res.Listing == nil {
		res.Listing = &backuphost.Listing{Path: dir}
	}
	if res.Listing.Entries == nil {
		res.Listing.Entries = []backuphost.BrowseEntry{}
	}
	writeJSON(w, http.StatusOK, res.Listing)
}

func (s *Server) getWorkloadBackupRules(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.BackupRead)
	if err != nil {
		return
	}
	wl, rootfs, err := s.workloadRootfs(r.Context(), p.User.ClusterID, r.PathValue("id"))
	if err != nil {
		writeErr(w, statusFor(err), err.Error())
		return
	}
	res, err := s.backupHostCall(r.Context(), qemu.BackupV2RulesGet, rootfs, backuphost.Request{WorkloadID: wl.ID, WorkloadName: wl.Name})
	if err != nil {
		writeErr(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, rulesJSON(res.Rules))
}

// putWorkloadBackupRules stores the workload's skip and keep lists in the
// workload itself (/.ndl-backup/rules.json); every backup follows them.
func (s *Server) putWorkloadBackupRules(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.BackupCreate)
	if err != nil {
		return
	}
	var body struct {
		Skip []string `json:"skip"`
		Keep []string `json:"keep"`
	}
	if err := readJSON(r, &body); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid rules")
		return
	}
	wl, rootfs, err := s.workloadRootfs(r.Context(), p.User.ClusterID, r.PathValue("id"))
	if err != nil {
		writeErr(w, statusFor(err), err.Error())
		return
	}
	rules := backuphost.WorkloadRules{Skip: body.Skip, Keep: body.Keep, UpdatedBy: p.User.Username}
	if _, err := rules.Normalize(); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	res, err := s.backupHostCall(r.Context(), qemu.BackupV2RulesSet, rootfs, backuphost.Request{WorkloadID: wl.ID, WorkloadName: wl.Name, Rules: &rules})
	if err != nil {
		writeErr(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	s.audit(r, p.User.ClusterID, p.User.ID, "backup.rules.update", "ok", fmt.Sprintf("%s: %d skipped, %d kept", wl.Name, len(rules.Skip), len(rules.Keep)))
	writeJSON(w, http.StatusOK, rulesJSON(res.Rules))
}

func rulesJSON(r *backuphost.WorkloadRules) map[string]any {
	if r == nil {
		r = &backuphost.WorkloadRules{}
	}
	skip, keep := r.Skip, r.Keep
	if skip == nil {
		skip = []string{}
	}
	if keep == nil {
		keep = []string{}
	}
	out := map[string]any{"skip": skip, "keep": keep, "path": backuphost.RulesPath}
	if !r.UpdatedAt.IsZero() {
		out["updated_at"] = r.UpdatedAt
		out["updated_by"] = r.UpdatedBy
	}
	return out
}

// deleteWorkloadBackups deletes every backup of one workload, here and in
// cloud storage. Protected backups stay unless include_protected is set.
func (s *Server) deleteWorkloadBackups(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.BackupCreate)
	if err != nil {
		return
	}
	if strings.TrimSpace(r.Header.Get(confirmHeader)) != deleteBackupConfirm {
		writeErr(w, http.StatusConflict, "deleting backups requires X-Nodal-Confirm: "+deleteBackupConfirm)
		return
	}
	ctx := r.Context()
	clusterID, id := p.User.ClusterID, r.PathValue("id")
	withProtected := r.URL.Query().Get("include_protected") == "true"
	arts, err := s.Store.ListBackupArtifacts(ctx, clusterID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	protected, _ := s.Store.ListProtectedBackupArtifacts(ctx, clusterID)
	deleted, unreachable, kept, failed := 0, 0, 0, 0
	for _, a := range arts {
		wid := a.WorkloadID
		if wid == "" {
			if run, _ := s.Store.GetBackupRun(ctx, clusterID, a.RunID); run != nil {
				wid = run.WorkloadID
			}
		}
		if wid != id {
			continue
		}
		if protected[a.ID] && !withProtected {
			kept++
			continue
		}
		gone, err := s.removeArtifact(ctx, clusterID, a, true)
		if err != nil {
			failed++
			s.recordStorageEvent("delete", false, wid, "backup "+a.ID+": "+err.Error())
			continue
		}
		deleted++
		if !gone {
			unreachable++
		}
	}
	s.audit(r, clusterID, p.User.ID, "backup.delete_workload", "ok", fmt.Sprintf("%s: %d deleted, %d protected kept, %d failed", id, deleted, kept, failed))
	if deleted > 0 {
		s.reclaimLater("deleted every backup of a workload")
	}
	writeJSON(w, http.StatusOK, map[string]any{"deleted": deleted, "data_unreachable": unreachable, "protected_kept": kept, "failed": failed})
}
