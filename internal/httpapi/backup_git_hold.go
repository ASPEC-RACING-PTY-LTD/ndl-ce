package httpapi

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"

	"github.com/no-dal/ndl-ce/internal/appdb"
	"github.com/no-dal/ndl-ce/internal/backup"
	"github.com/no-dal/ndl-ce/internal/backuphost"
	"github.com/no-dal/ndl-ce/internal/qemu"
	"github.com/no-dal/ndl-ce/internal/rbac"
)

// gitHoldError is returned for a capture held for uncommitted Git work.
type gitHoldError struct{ repos []backup.GitRepo }

func (e gitHoldError) Error() string {
	paths := make([]string, 0, len(e.repos))
	for _, r := range e.repos {
		paths = append(paths, r.Path)
	}
	return fmt.Sprintf("backup held: Git work in %s is not committed and pushed. Commit and push it, let NDL-CE do it, or turn on ignoring uncommitted Git repositories in Backups settings",
		strings.Join(paths, ", "))
}

// gitHoldFingerprint changes when the held work changes.
func gitHoldFingerprint(repos []backup.GitRepo) string {
	h := sha256.New()
	for _, r := range repos {
		fmt.Fprintf(h, "%s\x00%s\x00%t\x00%s\n", r.Path, r.Commit, r.Unpushed, strings.Join(r.Unsaved(), "\x00"))
	}
	return hex.EncodeToString(h.Sum(nil))
}

// recordGitHold stores a held workload and tells the Owner once per change.
func (s *Server) recordGitHold(ctx context.Context, clusterID string, wl appdb.Workload, run *appdb.BackupRun, repos []backup.GitRepo) error {
	raw, _ := json.Marshal(repos)
	fp := gitHoldFingerprint(repos)
	prev := s.gitHold(ctx, clusterID, wl.ID)
	h := appdb.BackupGitHold{
		ClusterID: clusterID, WorkloadID: wl.ID, PolicyID: run.PolicyID, TargetID: run.TargetID,
		ReposJSON: string(raw), Fingerprint: fp,
	}
	if prev != nil {
		h.LastAction, h.LastError = prev.LastAction, prev.LastError
	}
	if err := s.Store.UpsertBackupGitHold(ctx, h); err != nil {
		return err
	}
	if prev == nil || prev.Fingerprint != fp {
		payload, _ := json.Marshal(map[string]any{
			"kind": "backup.git_hold", "workload_id": wl.ID, "workload_name": wl.Name,
			"message": fmt.Sprintf("The backup of %s is held: %d Git repositories have work that is not committed and pushed.", wl.Name, len(repos)),
			"repos":   gitHoldReposJSON(repos),
		})
		s.emitUpdatePayload(ctx, clusterID, "backup.git_hold", payload)
		s.notify(ctx, clusterID, payload)
	}
	return gitHoldError{repos: repos}
}

func (s *Server) gitHold(ctx context.Context, clusterID, workloadID string) *appdb.BackupGitHold {
	holds, _ := s.Store.ListBackupGitHolds(ctx, clusterID)
	for i := range holds {
		if holds[i].WorkloadID == workloadID {
			return &holds[i]
		}
	}
	return nil
}

func gitHoldReposJSON(repos []backup.GitRepo) []map[string]any {
	out := make([]map[string]any, 0, len(repos))
	for _, r := range repos {
		remotes := make([]string, 0, len(r.Remotes))
		for _, rm := range r.Remotes {
			remotes = append(remotes, rm.Name+" "+rm.URL)
		}
		unsaved := r.Unsaved()
		if unsaved == nil {
			unsaved = []string{}
		}
		out = append(out, map[string]any{
			"path": r.Path, "branch": r.Branch, "commit": r.Commit, "remotes": remotes,
			"unpushed": r.Unpushed, "unsaved": unsaved, "more_changes": len(r.Changed) >= backup.GitChangedLimit,
		})
	}
	return out
}

func (s *Server) gitHoldJSON(ctx context.Context, h appdb.BackupGitHold) map[string]any {
	var repos []backup.GitRepo
	_ = json.Unmarshal([]byte(h.ReposJSON), &repos)
	name := h.WorkloadID
	if wl, _ := s.Store.GetWorkload(ctx, h.ClusterID, h.WorkloadID); wl != nil {
		name = wl.Name
	}
	return map[string]any{
		"workload_id": h.WorkloadID, "workload_name": name, "policy_id": h.PolicyID, "target_id": h.TargetID,
		"repos": gitHoldReposJSON(repos), "last_action": h.LastAction, "last_error": h.LastError,
		"held_since": h.CreatedAt, "updated_at": h.UpdatedAt,
	}
}

func (s *Server) listBackupGitHolds(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.BackupRead)
	if err != nil {
		return
	}
	holds, err := s.Store.ListBackupGitHolds(r.Context(), p.User.ClusterID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := make([]map[string]any, 0, len(holds))
	for _, h := range holds {
		out = append(out, s.gitHoldJSON(r.Context(), h))
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": out})
}

// resolveBackupGitHold acts on a held workload. "retry" backs it up again
// (after the Owner committed and pushed by hand); "save" has the agent
// commit and push the work from inside the guest, leaving secret-like files
// out, then retries. Only an Owner may do either.
func (s *Server) resolveBackupGitHold(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.BackupCreate)
	if err != nil {
		return
	}
	if !hasRole(p, rbac.Admin) {
		writeErr(w, http.StatusForbidden, "only an Owner can release a held backup")
		return
	}
	var req struct {
		Action string `json:"action"`
	}
	if err := readJSON(r, &req); err != nil || (req.Action != "retry" && req.Action != "save") {
		writeErr(w, http.StatusBadRequest, `action must be "retry" or "save"`)
		return
	}
	ctx := r.Context()
	clusterID := p.User.ClusterID
	h := s.gitHold(ctx, clusterID, r.PathValue("workload"))
	if h == nil {
		writeErr(w, http.StatusNotFound, "this workload's backup is not held")
		return
	}
	wl, err := s.Store.GetWorkload(ctx, clusterID, h.WorkloadID)
	if err != nil || wl == nil {
		_ = s.Store.DeleteBackupGitHold(ctx, clusterID, h.WorkloadID)
		writeErr(w, http.StatusNotFound, "workload not found")
		return
	}
	resp := map[string]any{"workload_id": wl.ID}
	if req.Action == "save" {
		var repos []backup.GitRepo
		_ = json.Unmarshal([]byte(h.ReposJSON), &repos)
		saved, err := s.saveGitWork(ctx, clusterID, *wl, repos)
		h.LastAction = "save"
		h.LastError = ""
		if err != nil {
			h.LastError = err.Error()
		}
		for _, sv := range saved {
			if sv.Error != "" {
				h.LastError = strings.TrimSpace(h.LastError + "\n" + sv.Path + ": " + sv.Error)
			}
		}
		_ = s.Store.UpsertBackupGitHold(ctx, *h)
		s.audit(r, clusterID, p.User.ID, "backup.git_hold.save", map[bool]string{true: "ok", false: "failed"}[h.LastError == ""], wl.ID)
		resp["saved"] = saved
		if h.LastError != "" {
			resp["error"] = h.LastError
			writeJSON(w, http.StatusUnprocessableEntity, resp)
			return
		}
	}
	if h.TargetID == "" {
		writeJSON(w, http.StatusOK, resp)
		return
	}
	run, err := s.executeBackup(ctx, clusterID, wl.ID, h.TargetID, h.PolicyID, "", nil)
	if h.PolicyID != "" {
		if pr := s.afterBackupAttempt(ctx, clusterID, wl.ID, h.TargetID, h.PolicyID); pr.V2 > 0 {
			_, _ = s.runBackupMaintenance(ctx, "held backup retention")
		}
	}
	if err != nil {
		writeErr(w, statusFor(err), err.Error())
		return
	}
	s.audit(r, clusterID, p.User.ID, "backup.git_hold."+req.Action, run.Status, wl.ID)
	resp["run"] = backupRunJSON(run)
	resp["held"] = s.gitHold(ctx, clusterID, wl.ID) != nil
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) saveGitWork(ctx context.Context, clusterID string, wl appdb.Workload, repos []backup.GitRepo) ([]backuphost.GitSaveResult, error) {
	if s.Backup == nil {
		return nil, errUnavailable("backup agent is unavailable")
	}
	_, _, rootfs, err := s.bootVolumeLocator(ctx, clusterID, wl)
	if err != nil {
		return nil, err
	}
	paths := make([]string, 0, len(repos))
	for _, r := range repos {
		paths = append(paths, r.Path)
	}
	sort.Strings(paths)
	raw, _ := json.Marshal(backuphost.Request{Action: backuphost.ActionGitSave, WorkloadID: wl.ID, WorkloadName: wl.Name, Repos: paths})
	res, err := s.Backup.CopyBackup(ctx, qemu.BackupV2GitSave, rootfs, string(raw))
	if err != nil {
		return nil, err
	}
	var out backuphost.Result
	_ = json.Unmarshal([]byte(res.Extra), &out)
	return out.GitSave, nil
}
