package httpapi

import (
	"fmt"
	"net/http"
	"strings"
	"time"
)

const clearLogConfirm = "clear-log"

// clearActivityLog deletes audit events and finished tasks, older than a
// date or all of them. Admin only, with a confirmation header. The clear is
// itself recorded as a new audit event, so the log always shows that it was
// cleared, by whom and how far back.
func (s *Server) clearActivityLog(w http.ResponseWriter, r *http.Request) {
	p, err := s.requireAdmin(w, r, "audit.clear")
	if err != nil {
		return
	}
	if strings.TrimSpace(r.Header.Get(confirmHeader)) != clearLogConfirm {
		writeErr(w, http.StatusConflict, "clearing the log requires X-Nodal-Confirm: "+clearLogConfirm)
		return
	}
	var req struct {
		Before string `json:"before"`
		All    bool   `json:"all"`
		Audit  bool   `json:"audit"`
		Tasks  bool   `json:"tasks"`
	}
	if err := readJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request")
		return
	}
	if !req.Audit && !req.Tasks {
		writeErr(w, http.StatusBadRequest, "choose audit events, tasks, or both")
		return
	}
	var before time.Time
	if !req.All {
		before, err = time.Parse(time.RFC3339, strings.TrimSpace(req.Before))
		if err != nil {
			writeErr(w, http.StatusBadRequest, "before must be an RFC 3339 time, or set all")
			return
		}
	}
	ctx := r.Context()
	out := map[string]any{}
	if req.Audit {
		n, err := s.Store.ClearAuditEvents(ctx, p.User.ClusterID, before)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "audit events could not be cleared: "+err.Error())
			return
		}
		out["audit_deleted"] = n
	}
	if req.Tasks {
		n, err := s.Store.ClearOperations(ctx, p.User.ClusterID, before)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "tasks could not be cleared: "+err.Error())
			return
		}
		out["tasks_deleted"] = n
	}
	scope := "everything"
	if !before.IsZero() {
		scope = "entries before " + before.UTC().Format(time.RFC3339)
	}
	s.audit(r, p.User.ClusterID, p.User.ID, "audit.clear", "ok", fmt.Sprintf("%s (audit %v, tasks %v): %v", scope, req.Audit, req.Tasks, out))
	writeJSON(w, http.StatusOK, out)
}
