package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/no-dal/ndl-ce/internal/appdb"
	"github.com/no-dal/ndl-ce/internal/automation/engine"
	"github.com/no-dal/ndl-ce/internal/automation/flowdef"
	"github.com/no-dal/ndl-ce/internal/rbac"
)

// Approval of flow versions. A version holding a protected step that a
// SuperAdmin did not build waits for approval; the last approved version
// keeps running meanwhile. An approval covers that exact definition (its
// hash). Deny rolls the draft back to the last approved version.

// allowOwnAPI refuses a definition that calls ndl-ce's own API unless p is
// a SuperAdmin.
func (s *Server) allowOwnAPI(w http.ResponseWriter, r *http.Request, p *principal, d *flowdef.Definition) bool {
	if isOwner(p) || p.User.ID == LocalRootUserID {
		return true
	}
	if names := ownAPISteps(s.protectedSteps(r.Context(), p.User.ClusterID, d)); len(names) > 0 {
		writeJSON(w, http.StatusForbidden, map[string]any{
			"error":     "steps " + strings.Join(names, ", ") + " call ndl-ce's own API; only a SuperAdmin may save that. Use the built-in steps instead.",
			"own_api":   names,
			"forbidden": true,
		})
		return false
	}
	return true
}

// addApprovalJSON adds the draft's approval state to a flow response.
func (s *Server) addApprovalJSON(ctx context.Context, p *principal, f appdb.AutomationFlow, out map[string]any) {
	protected := []protectedStep{}
	if d, err := flowdef.Parse(f.Draft); err == nil {
		protected = s.protectedSteps(ctx, f.ClusterID, d)
	}
	exempt := isOwner(p) && !f.DraftTainted
	out["protected_steps"] = protected
	out["publish_needs_approval"] = len(protected) > 0 && !exempt
	out["draft_tainted"] = f.DraftTainted
	out["pending_version"] = f.PendingVersion
	if f.PendingVersion > 0 {
		if v, _ := s.Store.GetAutomationFlowVersion(ctx, f.ClusterID, f.ID, f.PendingVersion); v != nil {
			out["pending"] = map[string]any{
				"version": v.Version, "requested_by": v.PublishedBy, "created_at": v.CreatedAt.UTC().Format(time.RFC3339),
				"draft_matches": engine.DefinitionHash(f.Draft) == v.Hash,
			}
		}
	}
	if vs, _ := s.Store.ListAutomationFlowVersions(ctx, f.ClusterID, f.ID); len(vs) > 0 {
		for _, v := range vs {
			if v.Status == appdb.FlowVersionDenied && v.Version > f.PublishedVersion && v.Version > f.PendingVersion {
				out["last_denied"] = map[string]any{"version": v.Version, "decided_by": v.DecidedBy, "note": v.DecisionNote}
				break
			}
		}
	}
}

func (s *Server) nextFlowVersion(ctx context.Context, f appdb.AutomationFlow) int {
	next := max(f.PublishedVersion, f.PendingVersion) + 1
	vs, _ := s.Store.ListAutomationFlowVersions(ctx, f.ClusterID, f.ID)
	for _, v := range vs {
		if v.Version >= next {
			next = v.Version + 1
		}
	}
	return next
}

// publishVersion records d as the next version of f: approved and running
// at once, or waiting for approval if it holds protected steps that a
// SuperAdmin did not build.
func (s *Server) publishVersion(w http.ResponseWriter, r *http.Request, p *principal, f *appdb.AutomationFlow, d *flowdef.Definition, note string) {
	ctx := r.Context()
	st := s.automation()
	def := d.Encode()
	protected := s.protectedSteps(ctx, f.ClusterID, d)
	exempt := isOwner(p) && !f.DraftTainted
	needs := len(protected) > 0 && !exempt
	prot, _ := json.Marshal(protected)
	version := s.nextFlowVersion(ctx, *f)
	v := appdb.AutomationFlowVersion{
		FlowID: f.ID, ClusterID: f.ClusterID, Version: version, Definition: def, PublishedBy: p.User.Username, Note: note,
		Status: appdb.FlowVersionApproved, Hash: engine.DefinitionHash(def), Protected: prot, Exempt: exempt && len(protected) > 0,
		RequestedByID: p.User.ID,
	}
	if needs {
		v.Status, v.NeedsApproval = appdb.FlowVersionPending, true
	}
	if err := s.Store.CreateAutomationFlowVersion(ctx, v); err != nil {
		writeErr(w, http.StatusConflict, "could not record the version: "+err.Error())
		return
	}
	// A newer version replaces a request still waiting.
	if f.PendingVersion > 0 {
		_ = s.Store.DecideAutomationFlowVersion(ctx, f.ClusterID, f.ID, f.PendingVersion, appdb.FlowVersionDecision{
			Status: appdb.FlowVersionSuperseded, DecidedBy: p.User.Username, DecidedAt: s.now(), Note: fmt.Sprintf("replaced by v%d", version),
		})
		f.PendingVersion = 0
	}
	if needs {
		f.PendingVersion = version
		if err := s.Store.UpdateAutomationFlow(ctx, *f); err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		s.audit(r, p.User.ClusterID, p.User.ID, "automation.flow.approval.request", "ok", fmt.Sprintf("%s v%d hash=%s", f.ID, version, v.Hash))
		names := make([]string, 0, len(protected))
		for _, ps := range protected {
			names = append(names, ps.Name)
		}
		s.notifyWithPermission(ctx, f.ClusterID, rbac.ApprovalsDecide, p.User.ID, noteApprovalWaiting,
			"Approval needed: "+f.Name,
			fmt.Sprintf("%s published version %d with protected steps: %s.", p.User.Username, version, strings.Join(names, ", ")),
			approvalLink(f.ID, version))
		out := s.flowJSON(ctx, *f, nil)
		s.addApprovalJSON(ctx, p, *f, out)
		out["waiting_for_approval"] = true
		writeJSON(w, http.StatusAccepted, out)
		return
	}
	f.PublishedVersion, f.TriggerKind, f.EventKind = version, d.TriggerKind(), d.EventKind()
	if err := s.Store.UpdateAutomationFlow(ctx, *f); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := st.svc.SyncTrigger(ctx, *f); err != nil {
		writeErr(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	s.audit(r, p.User.ClusterID, p.User.ID, "automation.flow.publish", "ok", fmt.Sprintf("%s v%d", f.ID, version))
	out := s.flowJSON(ctx, *f, nil)
	s.addApprovalJSON(ctx, p, *f, out)
	writeJSON(w, http.StatusOK, out)
}

func approvalLink(flowID string, version int) string {
	return fmt.Sprintf("/iam/approvals?flow=%s&version=%d", flowID, version)
}

func (s *Server) approvalJSON(ctx context.Context, v appdb.AutomationFlowVersion, f *appdb.AutomationFlow, detail bool) map[string]any {
	var protected []protectedStep
	_ = json.Unmarshal(v.Protected, &protected)
	if protected == nil {
		protected = []protectedStep{}
	}
	out := map[string]any{
		"flow_id": v.FlowID, "version": v.Version, "status": v.Status, "hash": v.Hash,
		"requested_by": v.PublishedBy, "requested_by_id": v.RequestedByID, "note": v.Note,
		"created_at": v.CreatedAt.UTC().Format(time.RFC3339), "protected_steps": protected,
		"decided_by": v.DecidedBy, "decision_note": v.DecisionNote, "decided_at": nil,
		"flow_name": "", "running_version": 0,
	}
	if v.DecidedAt != nil {
		out["decided_at"] = v.DecidedAt.UTC().Format(time.RFC3339)
	}
	if f != nil {
		out["flow_name"], out["running_version"] = f.Name, f.PublishedVersion
	}
	if detail {
		out["definition"] = autoDecode(v.Definition)
		out["base_version"], out["base_definition"] = 0, nil
		// The diff is against the newest approved version before this one.
		vs, _ := s.Store.ListAutomationFlowVersions(ctx, v.ClusterID, v.FlowID)
		best := 0
		for _, b := range vs {
			if b.Status == appdb.FlowVersionApproved && b.Version < v.Version && b.Version > best {
				best = b.Version
				out["base_version"], out["base_definition"] = b.Version, autoDecode(b.Definition)
			}
		}
	}
	return out
}

func (s *Server) listApprovals(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.ApprovalsView)
	if err != nil {
		return
	}
	ctx := r.Context()
	status := r.URL.Query().Get("status")
	switch status {
	case "", appdb.FlowVersionPending, appdb.FlowVersionApproved, appdb.FlowVersionDenied, appdb.FlowVersionSuperseded:
	default:
		writeErr(w, http.StatusBadRequest, "status is pending, approved, denied or superseded")
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	vs, err := s.Store.ListAutomationApprovals(ctx, p.User.ClusterID, status, limit)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	flows := map[string]*appdb.AutomationFlow{}
	out := make([]map[string]any, 0, len(vs))
	pending := 0
	for _, v := range vs {
		if _, ok := flows[v.FlowID]; !ok {
			flows[v.FlowID], _ = s.Store.GetAutomationFlow(ctx, p.User.ClusterID, v.FlowID)
		}
		if v.Status == appdb.FlowVersionPending {
			pending++
		}
		out = append(out, s.approvalJSON(ctx, v, flows[v.FlowID], false))
	}
	if status != appdb.FlowVersionPending {
		if all, _ := s.Store.ListAutomationApprovals(ctx, p.User.ClusterID, appdb.FlowVersionPending, 500); all != nil {
			pending = len(all)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"items": out, "pending": pending, "can_decide": rbac.Authorize(p.Grants, rbac.ApprovalsDecide),
		"can_manage_access": isOwner(p),
	})
}

func (s *Server) loadApproval(w http.ResponseWriter, r *http.Request, p *principal) (*appdb.AutomationFlowVersion, *appdb.AutomationFlow, bool) {
	ctx := r.Context()
	version, err := strconv.Atoi(r.PathValue("version"))
	if err != nil || version <= 0 {
		writeErr(w, http.StatusBadRequest, "invalid version")
		return nil, nil, false
	}
	f, _ := s.Store.GetAutomationFlow(ctx, p.User.ClusterID, r.PathValue("flow"))
	if f == nil {
		writeErr(w, http.StatusNotFound, "flow not found")
		return nil, nil, false
	}
	v, _ := s.Store.GetAutomationFlowVersion(ctx, p.User.ClusterID, f.ID, version)
	if v == nil || !v.NeedsApproval {
		writeErr(w, http.StatusNotFound, "approval request not found")
		return nil, nil, false
	}
	return v, f, true
}

func (s *Server) getApproval(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.ApprovalsView)
	if err != nil {
		return
	}
	v, f, ok := s.loadApproval(w, r, p)
	if !ok {
		return
	}
	out := s.approvalJSON(r.Context(), *v, f, true)
	out["can_decide"] = rbac.Authorize(p.Grants, rbac.ApprovalsDecide) && (isOwner(p) || v.RequestedByID != p.User.ID)
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) decideApproval(approve bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p, err := s.require(w, r, rbac.ApprovalsDecide)
		if err != nil {
			return
		}
		var req struct {
			Note string `json:"note"`
		}
		_ = readJSON(r, &req)
		v, f, ok := s.loadApproval(w, r, p)
		if !ok {
			return
		}
		ctx := r.Context()
		if v.RequestedByID == p.User.ID && !isOwner(p) {
			writeErr(w, http.StatusForbidden, "you cannot decide your own request")
			return
		}
		if v.Status != appdb.FlowVersionPending {
			writeErr(w, http.StatusConflict, appdb.ErrNotPending.Error())
			return
		}
		if engine.DefinitionHash(v.Definition) != v.Hash {
			writeErr(w, http.StatusConflict, "the version no longer matches what was requested")
			return
		}
		status, verb := appdb.FlowVersionDenied, "deny"
		if approve {
			status, verb = appdb.FlowVersionApproved, "approve"
		}
		d, err := flowdef.Parse(v.Definition)
		if approve && err != nil {
			writeErr(w, http.StatusUnprocessableEntity, err.Error())
			return
		}
		note := strings.TrimSpace(req.Note)
		if err := s.Store.DecideAutomationFlowVersion(ctx, f.ClusterID, f.ID, v.Version, appdb.FlowVersionDecision{
			Status: status, DecidedBy: p.User.Username, DecidedAt: s.now(), Note: note,
		}); err != nil {
			code := http.StatusInternalServerError
			if errors.Is(err, appdb.ErrNotPending) {
				code = http.StatusConflict
			}
			writeErr(w, code, err.Error())
			return
		}
		if f.PendingVersion == v.Version {
			f.PendingVersion = 0
		}
		draftIsVersion := engine.DefinitionHash(f.Draft) == v.Hash
		if approve {
			f.PublishedVersion, f.TriggerKind, f.EventKind = v.Version, d.TriggerKind(), d.EventKind()
			if draftIsVersion {
				// The draft is what was just vetted.
				f.DraftTainted = false
			}
		} else if draftIsVersion && f.PublishedVersion > 0 {
			// Roll the draft back to the version that keeps running.
			if base, _ := s.Store.GetAutomationFlowVersion(ctx, f.ClusterID, f.ID, f.PublishedVersion); base != nil {
				f.Draft, f.DraftTainted = base.Definition, false
			}
		}
		if err := s.Store.UpdateAutomationFlow(ctx, *f); err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		if approve {
			if err := s.automation().svc.SyncTrigger(ctx, *f); err != nil {
				writeErr(w, http.StatusUnprocessableEntity, err.Error())
				return
			}
		}
		detail := fmt.Sprintf("%s v%d hash=%s", f.ID, v.Version, v.Hash)
		if note != "" {
			detail += ": " + note
		}
		s.audit(r, p.User.ClusterID, p.User.ID, "automation.flow.approval."+verb, "ok", detail)
		title := "Approved: " + f.Name
		body := fmt.Sprintf("%s approved version %d; it runs now.", p.User.Username, v.Version)
		if !approve {
			title = "Denied: " + f.Name
			body = fmt.Sprintf("%s denied version %d.", p.User.Username, v.Version)
			if f.PublishedVersion > 0 {
				body += fmt.Sprintf(" Version %d keeps running.", f.PublishedVersion)
			} else {
				body += " The flow stays a draft."
			}
		}
		if note != "" {
			body += " Note: " + note
		}
		s.notifyUser(ctx, f.ClusterID, v.RequestedByID, noteApprovalDecided, title, body, "/automation/flows/"+f.ID)
		updated, _ := s.Store.GetAutomationFlowVersion(ctx, f.ClusterID, f.ID, v.Version)
		if updated == nil {
			updated = v
		}
		writeJSON(w, http.StatusOK, s.approvalJSON(ctx, *updated, f, false))
	}
}
