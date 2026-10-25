package httpapi

import (
	"context"
	"net/http"
	"strings"

	"github.com/no-dal/ndl-ce/internal/appdb"
	"github.com/no-dal/ndl-ce/internal/rbac"
)

// A workload created after an "All workloads" backup policy is not backed
// up by such policies until someone approves it from the bell. Denying it
// adds it to each such policy's Leave out list, where it can be undone.

const (
	noteBackupApproval  = "backup.approval"
	backupApprovalLink  = "/backups?approve="
	backupApprovalTitle = "Back up new workload?"
)

// approvalSubject is the workload a backup approval notification asks about.
func approvalSubject(n appdb.Notification) string {
	if n.Kind != noteBackupApproval {
		return ""
	}
	id, ok := strings.CutPrefix(n.Link, backupApprovalLink)
	if !ok {
		return ""
	}
	return id
}

// syncBackupApprovals records every backup-capable workload not yet asked
// about: one created after the oldest "All workloads" policy waits for
// approval and its approvers are notified; any other is approved. Approvals
// of deleted workloads are dropped. It returns the approvals by workload.
func (s *Server) syncBackupApprovals(ctx context.Context, clusterID string) map[string]appdb.BackupApproval {
	s.approvalMu.Lock()
	defer s.approvalMu.Unlock()
	approvals, err := s.Store.BackupApprovals(ctx, clusterID)
	if err != nil {
		return nil
	}
	workloads, err := s.Store.ListWorkloads(ctx, clusterID)
	if err != nil {
		return approvals
	}
	pols, err := s.Store.ListBackupPolicies(ctx, clusterID)
	if err != nil {
		return approvals
	}
	var oldest *appdb.BackupPolicy
	for i := range pols {
		pol := pols[i]
		appdb.NormalizeBackupPolicy(&pol)
		if pol.Scope == appdb.BackupScopeAll && (oldest == nil || pol.CreatedAt.Before(oldest.CreatedAt)) {
			oldest = &pol
		}
	}
	live := map[string]bool{}
	now := s.now()
	for _, wl := range workloads {
		live[wl.ID] = true
		if _, ok := approvals[wl.ID]; ok || !backupKind(wl.Kind) {
			continue
		}
		a := appdb.BackupApproval{ClusterID: clusterID, WorkloadID: wl.ID, State: appdb.BackupApprovalApproved, CreatedAt: now, DecidedAt: &now}
		pending := oldest != nil && wl.CreatedAt.After(oldest.CreatedAt)
		if pending {
			a.State, a.DecidedAt = appdb.BackupApprovalPending, nil
		}
		if err := s.Store.PutBackupApproval(ctx, a); err != nil {
			continue
		}
		approvals[wl.ID] = a
		if pending {
			s.notifyWithPermission(ctx, clusterID, rbac.BackupCreate, "", noteBackupApproval, backupApprovalTitle,
				wl.Name+" was created after your All workloads backup policies. Approve to back it up with them, or deny to leave it out.",
				backupApprovalLink+wl.ID)
		}
	}
	for id := range approvals {
		if !live[id] {
			_ = s.Store.DeleteBackupApproval(ctx, clusterID, id)
			delete(approvals, id)
		}
	}
	return approvals
}

// decideBackupApproval approves or denies one waiting workload.
func (s *Server) decideBackupApproval(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.BackupCreate)
	if err != nil {
		return
	}
	var req struct {
		Decision string `json:"decision"`
	}
	if err := readJSON(r, &req); err != nil || (req.Decision != "approve" && req.Decision != "deny") {
		writeErr(w, http.StatusBadRequest, `decision must be "approve" or "deny"`)
		return
	}
	ctx := r.Context()
	clusterID, id := p.User.ClusterID, r.PathValue("id")
	s.approvalMu.Lock()
	defer s.approvalMu.Unlock()
	approvals, err := s.Store.BackupApprovals(ctx, clusterID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	a, ok := approvals[id]
	if !ok {
		writeErr(w, http.StatusNotFound, "no backup approval for this workload")
		return
	}
	if a.State != appdb.BackupApprovalPending {
		writeJSON(w, http.StatusConflict, map[string]any{"error": "this workload was already " + a.State, "state": a.State})
		return
	}
	if req.Decision == "deny" {
		pols, err := s.Store.ListBackupPolicies(ctx, clusterID)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		for _, pol := range pols {
			appdb.NormalizeBackupPolicy(&pol)
			if pol.Scope != appdb.BackupScopeAll || containsString(pol.ExcludedWorkloadIDs, id) {
				continue
			}
			pol.ExcludedWorkloadIDs = append(pol.ExcludedWorkloadIDs, id)
			if err := s.Store.UpdateBackupPolicy(ctx, pol); err != nil {
				writeErr(w, http.StatusInternalServerError, err.Error())
				return
			}
		}
	}
	now := s.now()
	a.State, a.DecidedBy, a.DecidedAt = appdb.BackupApprovalApproved, p.User.ID, &now
	if req.Decision == "deny" {
		a.State = appdb.BackupApprovalDenied
	}
	if err := s.Store.PutBackupApproval(ctx, a); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.markApprovalRead(ctx, clusterID, p.User.ID, id)
	s.audit(r, clusterID, p.User.ID, "backup.approval."+req.Decision, "ok", id)
	writeJSON(w, http.StatusOK, map[string]any{"workload_id": id, "state": a.State})
}

// markApprovalRead marks the decider's notifications about the workload read.
func (s *Server) markApprovalRead(ctx context.Context, clusterID, userID, workloadID string) {
	items, err := s.Store.ListNotifications(ctx, clusterID, userID, true, appdb.NotificationsKept)
	if err != nil {
		return
	}
	var ids []string
	for _, n := range items {
		if approvalSubject(n) == workloadID {
			ids = append(ids, n.ID)
		}
	}
	if len(ids) > 0 {
		_ = s.Store.MarkNotificationsRead(ctx, clusterID, userID, ids, s.now())
	}
}
