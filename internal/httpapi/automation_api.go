package httpapi

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/no-dal/ndl-ce/internal/appdb"
	"github.com/no-dal/ndl-ce/internal/automation/engine"
	"github.com/no-dal/ndl-ce/internal/automation/flowdef"
	"github.com/no-dal/ndl-ce/internal/rbac"
	"github.com/no-dal/ndl-ce/internal/secutil"
)

// Automations API. Reads need policy.read, editing needs policy.apply and
// running, testing, cancelling and approving need policy.run. Every route
// except the inbound webhook also needs the Automation feature turned on;
// the webhook refuses on its own while it is off.

func autoDecode(raw json.RawMessage) any {
	if len(raw) == 0 {
		return map[string]any{}
	}
	var v any
	if json.Unmarshal(raw, &v) != nil {
		return map[string]any{}
	}
	return v
}

func autoTimeJSON(t *time.Time) any {
	if t == nil {
		return nil
	}
	return t.UTC().Format(time.RFC3339)
}

func emptyDefinition() *flowdef.Definition {
	return &flowdef.Definition{
		SchemaVersion: flowdef.SchemaVersion,
		Trigger:       flowdef.Trigger{ID: flowdef.TriggerID, Type: flowdef.TriggerManual, Config: map[string]any{}},
		Nodes:         []flowdef.Node{},
		Edges:         []flowdef.Edge{},
	}
}

func (s *Server) flowJSON(ctx context.Context, f appdb.AutomationFlow, last *appdb.AutomationRun) map[string]any {
	st := s.automation()
	draftHash := engine.DefinitionHash(f.Draft)
	publishedHash := ""
	if def, err := st.svc.Definition(ctx, f); err == nil {
		publishedHash = engine.DefinitionHash(def.Encode())
	}
	var destructive []string
	if d, err := flowdef.Parse(f.Draft); err == nil {
		destructive = st.reg.HasDestructive(d)
	}
	if destructive == nil {
		destructive = []string{}
	}
	out := map[string]any{
		"id": f.ID, "name": f.Name, "description": f.Description, "enabled": f.Enabled,
		"published_version": f.PublishedVersion, "trigger_kind": f.TriggerKind, "event_kind": f.EventKind,
		"draft": autoDecode(f.Draft), "limits": engine.FlowLimits(f),
		"draft_changed":  f.PublishedVersion == 0 || draftHash != publishedHash,
		"dry_run_passed": f.DryRunHash != "" && f.DryRunHash == draftHash,
		"destructive":    destructive, "consecutive_failures": f.ConsecutiveFailures, "disabled_reason": f.DisabledReason,
		"legacy_policy_id": f.LegacyPolicyID, "created_by": f.CreatedBy,
		"created_at": f.CreatedAt.UTC().Format(time.RFC3339), "updated_at": f.UpdatedAt.UTC().Format(time.RFC3339),
	}
	if last != nil {
		out["last_run"] = map[string]any{"id": last.ID, "status": last.Status, "mode": last.Mode, "created_at": last.CreatedAt.UTC().Format(time.RFC3339), "error": last.Error}
	}
	return out
}

func runJSON(r appdb.AutomationRun, flowName string) map[string]any {
	out := map[string]any{
		"id": r.ID, "flow_id": r.FlowID, "flow_name": flowName, "version": r.Version, "mode": r.Mode, "status": r.Status,
		"trigger_kind": r.TriggerKind, "trigger_payload": autoDecode(r.TriggerPayload), "inputs": autoDecode(r.Inputs),
		"idempotency_key": r.IdempotencyKey, "correlation_id": r.CorrelationID, "caused_by_run": r.CausedByRun,
		"chain_depth": r.ChainDepth, "retry_of": r.RetryOf, "started_by": r.StartedBy, "error": r.Error,
		"wake_at": autoTimeJSON(r.WakeAt), "started_at": autoTimeJSON(r.StartedAt), "finished_at": autoTimeJSON(r.FinishedAt),
		"created_at": r.CreatedAt.UTC().Format(time.RFC3339),
	}
	if len(r.Report) > 0 && string(r.Report) != "{}" {
		out["report"] = autoDecode(r.Report)
	}
	approvals := []any{}
	if st, err := engine.DecodeState(r.State); err == nil {
		top := st.Frames[len(st.Frames)-1]
		for _, id := range autoSortedNodes(top.Nodes) {
			if top.Nodes[id].Status == engine.NodeApproval {
				msg := ""
				if m, ok := st.Outputs[id].(map[string]any); ok {
					msg, _ = m["message"].(string)
				}
				approvals = append(approvals, map[string]any{"node_id": id, "scope": top.Scope, "message": msg})
			}
		}
		out["steps"] = st.Steps
	}
	out["pending_approvals"] = approvals
	return out
}

func autoSortedNodes(m map[string]*engine.NodeState) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j] < out[j-1]; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

func (s *Server) loadFlow(w http.ResponseWriter, r *http.Request, p *principal) (*appdb.AutomationFlow, bool) {
	f, err := s.Store.GetAutomationFlow(r.Context(), p.User.ClusterID, r.PathValue("id"))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return nil, false
	}
	if f == nil {
		writeErr(w, http.StatusNotFound, "flow not found")
		return nil, false
	}
	return f, true
}

func writeProblems(w http.ResponseWriter, problems []flowdef.Problem) {
	writeJSON(w, http.StatusUnprocessableEntity, map[string]any{
		"error": fmt.Sprintf("the flow has %d problem(s)", len(problems)), "problems": problems,
	})
}

func (s *Server) listAutomationFlows(w http.ResponseWriter, r *http.Request) {
	p, ok := s.requireAutomation(w, r, rbac.PolicyRead)
	if !ok {
		return
	}
	flows, err := s.Store.ListAutomationFlows(r.Context(), p.User.ClusterID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := make([]map[string]any, 0, len(flows))
	for _, f := range flows {
		var last *appdb.AutomationRun
		if runs, _ := s.Store.ListAutomationRuns(r.Context(), p.User.ClusterID, appdb.AutomationRunFilter{FlowID: f.ID, Limit: 1}); len(runs) > 0 {
			last = &runs[0]
		}
		out = append(out, s.flowJSON(r.Context(), f, last))
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": out})
}

type flowBody struct {
	Name        *string         `json:"name"`
	Description *string         `json:"description"`
	Definition  json.RawMessage `json:"definition"`
	Limits      *flowdef.Limits `json:"limits"`
	Template    string          `json:"template"`
	Inputs      map[string]any  `json:"inputs"`
	Payload     map[string]any  `json:"payload"`
}

func (s *Server) createAutomationFlow(w http.ResponseWriter, r *http.Request) {
	p, ok := s.requireAutomationEdit(w, r)
	if !ok {
		return
	}
	var req flowBody
	if err := readJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request")
		return
	}
	def := emptyDefinition()
	var limits flowdef.Limits
	desc := ""
	if req.Template != "" {
		t, found := automationTemplate(req.Template)
		if !found {
			writeErr(w, http.StatusNotFound, "template not found")
			return
		}
		def, limits, desc = t.definition(), t.Limits, t.Description
		if req.Name == nil {
			name := t.Name
			req.Name = &name
		}
	}
	if len(req.Definition) > 0 {
		d, err := flowdef.Parse(req.Definition)
		if err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		def = d
	}
	if req.Limits != nil {
		limits = *req.Limits
	}
	if req.Description != nil {
		desc = *req.Description
	}
	name := ""
	if req.Name != nil {
		name = strings.TrimSpace(*req.Name)
	}
	if name == "" || len(name) > 120 {
		writeErr(w, http.StatusBadRequest, "name is required (at most 120 characters)")
		return
	}
	if !s.allowOwnAPI(w, r, p, def) {
		return
	}
	lim, _ := json.Marshal(limits.Normalized())
	f := appdb.AutomationFlow{
		ID: uuid.NewString(), ClusterID: p.User.ClusterID, Name: name, Description: strings.TrimSpace(desc),
		Draft: def.Encode(), Limits: lim, CreatedBy: p.User.Username, DraftTainted: !isOwner(p),
	}
	if err := s.Store.CreateAutomationFlow(r.Context(), f); err != nil {
		writeErr(w, http.StatusConflict, err.Error())
		return
	}
	s.audit(r, p.User.ClusterID, p.User.ID, "automation.flow.create", "ok", f.ID+" "+f.Name)
	created, _ := s.Store.GetAutomationFlow(r.Context(), p.User.ClusterID, f.ID)
	if created == nil {
		created = &f
	}
	writeJSON(w, http.StatusCreated, s.flowJSON(r.Context(), *created, nil))
}

func (s *Server) getAutomationFlow(w http.ResponseWriter, r *http.Request) {
	p, ok := s.requireAutomation(w, r, rbac.PolicyRead)
	if !ok {
		return
	}
	f, ok := s.loadFlow(w, r, p)
	if !ok {
		return
	}
	var last *appdb.AutomationRun
	if runs, _ := s.Store.ListAutomationRuns(r.Context(), p.User.ClusterID, appdb.AutomationRunFilter{FlowID: f.ID, Limit: 1}); len(runs) > 0 {
		last = &runs[0]
	}
	out := s.flowJSON(r.Context(), *f, last)
	if d, err := flowdef.Parse(f.Draft); err == nil {
		out["problems"] = flowdef.Validate(d, s.automation().reg)
	}
	s.addApprovalJSON(r.Context(), p, *f, out)
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) patchAutomationFlow(w http.ResponseWriter, r *http.Request) {
	p, ok := s.requireAutomationEdit(w, r)
	if !ok {
		return
	}
	f, ok := s.loadFlow(w, r, p)
	if !ok {
		return
	}
	var req flowBody
	if err := readJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request")
		return
	}
	if req.Name != nil {
		name := strings.TrimSpace(*req.Name)
		if name == "" || len(name) > 120 {
			writeErr(w, http.StatusBadRequest, "name is required (at most 120 characters)")
			return
		}
		f.Name = name
	}
	if req.Description != nil {
		f.Description = strings.TrimSpace(*req.Description)
	}
	if len(req.Definition) > 0 {
		d, err := flowdef.Parse(req.Definition)
		if err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		if len(d.Nodes) > flowdef.MaxNodes {
			writeErr(w, http.StatusUnprocessableEntity, fmt.Sprintf("a flow has at most %d steps", flowdef.MaxNodes))
			return
		}
		if !s.allowOwnAPI(w, r, p, d) {
			return
		}
		if engine.DefinitionHash(d.Encode()) != engine.DefinitionHash(f.Draft) {
			taintDraft(p, f)
		}
		f.Draft = d.Encode()
	}
	if req.Limits != nil {
		lim, _ := json.Marshal(req.Limits.Normalized())
		f.Limits = lim
	}
	if err := s.Store.UpdateAutomationFlow(r.Context(), *f); err != nil {
		writeErr(w, http.StatusConflict, err.Error())
		return
	}
	s.audit(r, p.User.ClusterID, p.User.ID, "automation.flow.update", "ok", f.ID)
	out := s.flowJSON(r.Context(), *f, nil)
	if d, err := flowdef.Parse(f.Draft); err == nil {
		out["problems"] = flowdef.Validate(d, s.automation().reg)
	}
	s.addApprovalJSON(r.Context(), p, *f, out)
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) deleteAutomationFlow(w http.ResponseWriter, r *http.Request) {
	p, ok := s.requireAutomationEdit(w, r)
	if !ok {
		return
	}
	f, ok := s.loadFlow(w, r, p)
	if !ok {
		return
	}
	runs, _ := s.Store.ListAutomationRuns(r.Context(), p.User.ClusterID, appdb.AutomationRunFilter{FlowID: f.ID, Limit: 500})
	for _, run := range runs {
		if !appdb.AutoRunFinished(run.Status) {
			_ = s.automation().svc.Cancel(r.Context(), p.User.ClusterID, run.ID, p.User.Username)
		}
	}
	_ = s.Store.DeleteAutomationSchedule(r.Context(), p.User.ClusterID, f.ID)
	if err := s.Store.DeleteAutomationFlow(r.Context(), p.User.ClusterID, f.ID); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.audit(r, p.User.ClusterID, p.User.ID, "automation.flow.delete", "ok", f.ID+" "+f.Name)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) validateAutomationFlow(w http.ResponseWriter, r *http.Request) {
	p, ok := s.requireAutomation(w, r, rbac.PolicyRead)
	if !ok {
		return
	}
	f, ok := s.loadFlow(w, r, p)
	if !ok {
		return
	}
	var req flowBody
	_ = readJSON(r, &req)
	raw := f.Draft
	if len(req.Definition) > 0 {
		raw = req.Definition
	}
	d, err := flowdef.Parse(raw)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "problems": []flowdef.Problem{{Path: "", Message: err.Error()}}})
		return
	}
	problems := flowdef.Validate(d, s.automation().reg)
	if problems == nil {
		problems = []flowdef.Problem{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": len(problems) == 0, "problems": problems, "destructive": s.automation().reg.HasDestructive(d)})
}

func (s *Server) publishAutomationFlow(w http.ResponseWriter, r *http.Request) {
	p, ok := s.requireAutomationEdit(w, r)
	if !ok {
		return
	}
	f, ok := s.loadFlow(w, r, p)
	if !ok {
		return
	}
	var req struct {
		Note             string `json:"note"`
		SkipDryRunReason string `json:"skip_dry_run_reason"`
	}
	_ = readJSON(r, &req)
	st := s.automation()
	d, err := flowdef.Parse(f.Draft)
	if err != nil {
		writeProblems(w, []flowdef.Problem{{Path: "", Message: err.Error()}})
		return
	}
	if problems := flowdef.Validate(d, st.reg); len(problems) > 0 {
		writeProblems(w, problems)
		return
	}
	if !s.allowOwnAPI(w, r, p, d) {
		return
	}
	if destructive := st.reg.HasDestructive(d); len(destructive) > 0 && f.DryRunHash != engine.DefinitionHash(f.Draft) {
		reason := strings.TrimSpace(req.SkipDryRunReason)
		if reason == "" || !rbac.Authorize(p.Grants, rbac.AutomationSkipTest) {
			msg := "steps " + strings.Join(destructive, ", ") + " change things: run a test of this draft before publishing"
			if reason != "" {
				msg += "; only a SuperAdmin can publish without a test"
			}
			writeJSON(w, http.StatusConflict, map[string]any{"error": msg, "needs_dry_run": true, "destructive": destructive})
			return
		}
		s.audit(r, p.User.ClusterID, p.User.ID, "automation.flow.publish.skip_dry_run", "ok", f.ID+": "+reason)
	}
	s.publishVersion(w, r, p, f, d, strings.TrimSpace(req.Note))
}

func (s *Server) setAutomationFlowEnabled(enabled bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p, ok := s.requireAutomation(w, r, rbac.PolicyApply)
		if !ok {
			return
		}
		f, ok := s.loadFlow(w, r, p)
		if !ok {
			return
		}
		if enabled && f.PublishedVersion == 0 {
			writeErr(w, http.StatusConflict, "publish the flow before turning it on")
			return
		}
		// A flow the approvals update turned off ran before protected steps
		// needed approval: below SuperAdmin it must be approved first.
		if enabled && f.DisabledReason == flowDisabledByUpdate && !isOwner(p) {
			if def, err := s.automation().svc.Definition(r.Context(), *f); err == nil && len(s.protectedSteps(r.Context(), p.User.ClusterID, def)) > 0 {
				writeErr(w, http.StatusConflict, "this flow has protected steps: publish it again so a SuperAdmin can approve it, or ask a SuperAdmin to turn it on")
				return
			}
		}
		f.Enabled = enabled
		if enabled {
			f.ConsecutiveFailures, f.DisabledReason = 0, ""
		}
		if err := s.Store.UpdateAutomationFlow(r.Context(), *f); err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		if err := s.automation().svc.SyncTrigger(r.Context(), *f); err != nil {
			writeErr(w, http.StatusUnprocessableEntity, err.Error())
			return
		}
		action := "automation.flow.disable"
		if enabled {
			action = "automation.flow.enable"
		}
		s.audit(r, p.User.ClusterID, p.User.ID, action, "ok", f.ID)
		writeJSON(w, http.StatusOK, s.flowJSON(r.Context(), *f, nil))
	}
}

func (s *Server) cloneAutomationFlow(w http.ResponseWriter, r *http.Request) {
	p, ok := s.requireAutomationEdit(w, r)
	if !ok {
		return
	}
	f, ok := s.loadFlow(w, r, p)
	if !ok {
		return
	}
	var req flowBody
	_ = readJSON(r, &req)
	name := f.Name + " (copy)"
	if req.Name != nil && strings.TrimSpace(*req.Name) != "" {
		name = strings.TrimSpace(*req.Name)
	}
	if d, err := flowdef.Parse(f.Draft); err == nil && !s.allowOwnAPI(w, r, p, d) {
		return
	}
	c := appdb.AutomationFlow{
		ID: uuid.NewString(), ClusterID: f.ClusterID, Name: name, Description: f.Description, Draft: f.Draft, Limits: f.Limits, CreatedBy: p.User.Username,
		DraftTainted: !isOwner(p) || f.DraftTainted,
	}
	if err := s.Store.CreateAutomationFlow(r.Context(), c); err != nil {
		writeErr(w, http.StatusConflict, err.Error())
		return
	}
	s.audit(r, p.User.ClusterID, p.User.ID, "automation.flow.create", "ok", c.ID+" clone of "+f.ID)
	writeJSON(w, http.StatusCreated, s.flowJSON(r.Context(), c, nil))
}

func (s *Server) executeAutomationFlow(w http.ResponseWriter, r *http.Request) {
	p, ok := s.requireAutomation(w, r, rbac.PolicyRun)
	if !ok {
		return
	}
	f, ok := s.loadFlow(w, r, p)
	if !ok {
		return
	}
	var req flowBody
	_ = readJSON(r, &req)
	actorID, err := s.ensureAutomationActor(r.Context(), p.User.ClusterID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	res, err := s.automation().svc.Start(r.Context(), engine.StartRequest{
		ClusterID: p.User.ClusterID, FlowID: f.ID, TriggerKind: "manual", Payload: req.Payload, Inputs: req.Inputs,
		IdempotencyKey: strings.TrimSpace(r.Header.Get("Idempotency-Key")), ActorID: actorID, StartedBy: p.User.Username, UserStarted: true,
	})
	if err != nil {
		writeErr(w, autoStatus(err), err.Error())
		return
	}
	s.audit(r, p.User.ClusterID, p.User.ID, "automation.flow.execute", res.Run.Status, f.ID+" "+res.Run.ID)
	writeJSON(w, http.StatusAccepted, map[string]any{"run": runJSON(*res.Run, f.Name), "existing": res.Existing})
}

func autoStatus(err error) int {
	switch {
	case errors.Is(err, engine.ErrNotFound):
		return http.StatusNotFound
	case errors.Is(err, engine.ErrNotPublished), errors.Is(err, engine.ErrDisabled), errors.Is(err, engine.ErrBusy), errors.Is(err, engine.ErrNotApproved):
		return http.StatusConflict
	}
	return http.StatusUnprocessableEntity
}

// dryRunAutomationFlow tests a flow. The run is stepped right away: test
// runs never wait, so the report comes back in the answer.
func (s *Server) dryRunAutomationFlow(w http.ResponseWriter, r *http.Request) {
	p, ok := s.requireAutomation(w, r, rbac.PolicyRun)
	if !ok {
		return
	}
	f, ok := s.loadFlow(w, r, p)
	if !ok {
		return
	}
	var req struct {
		Published bool           `json:"published"`
		Payload   map[string]any `json:"payload"`
		Inputs    map[string]any `json:"inputs"`
		Sample    string         `json:"sample"`
	}
	_ = readJSON(r, &req)
	st := s.automation()
	payload := req.Payload
	if req.Sample == "recent" {
		raw := f.Draft
		if req.Published {
			if def, err := st.svc.Definition(r.Context(), *f); err == nil {
				raw = def.Encode()
			}
		}
		if d, err := flowdef.Parse(raw); err == nil {
			switch d.Trigger.Type {
			case flowdef.TriggerEvent:
				if recent := st.bus.Recent(p.User.ClusterID, d.EventKind()); len(recent) > 0 {
					payload = engine.EventPayload(recent[0])
				}
			case flowdef.TriggerWebhook:
				if ds, _ := s.Store.ListAutomationWebhookDeliveries(r.Context(), p.User.ClusterID, f.ID, 1); len(ds) > 0 {
					_ = json.Unmarshal(ds[0].Payload, &payload)
				}
			}
		}
	}
	actorID, err := s.ensureAutomationActor(r.Context(), p.User.ClusterID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	res, err := st.svc.DryRun(r.Context(), p.User.ClusterID, f.ID, !req.Published, payload, req.Inputs, actorID, p.User.Username)
	if err != nil {
		writeErr(w, autoStatus(err), err.Error())
		return
	}
	run := res.Run
	if run.Status == appdb.AutoRunPending {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
		err := st.eng.Step(ctx, p.User.ClusterID, run.ID)
		cancel()
		if err != nil && !errors.Is(err, engine.ErrBusy) {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		if got, _ := s.Store.GetAutomationRun(r.Context(), p.User.ClusterID, run.ID); got != nil {
			run = got
		}
	}
	s.audit(r, p.User.ClusterID, p.User.ID, "automation.flow.dry_run", run.Status, f.ID+" "+run.ID)
	updated, _ := s.Store.GetAutomationFlow(r.Context(), p.User.ClusterID, f.ID)
	if updated == nil {
		updated = f
	}
	writeJSON(w, http.StatusOK, map[string]any{"run": runJSON(*run, f.Name), "flow": s.flowJSON(r.Context(), *updated, nil)})
}

func (s *Server) listAutomationFlowVersions(w http.ResponseWriter, r *http.Request) {
	p, ok := s.requireAutomation(w, r, rbac.PolicyRead)
	if !ok {
		return
	}
	f, ok := s.loadFlow(w, r, p)
	if !ok {
		return
	}
	versions, err := s.Store.ListAutomationFlowVersions(r.Context(), p.User.ClusterID, f.ID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := make([]map[string]any, 0, len(versions))
	for _, v := range versions {
		out = append(out, map[string]any{
			"version": v.Version, "definition": autoDecode(v.Definition), "published_by": v.PublishedBy, "note": v.Note,
			"created_at": v.CreatedAt.UTC().Format(time.RFC3339), "current": v.Version == f.PublishedVersion,
			"status": v.Status, "needs_approval": v.NeedsApproval, "decided_by": v.DecidedBy, "decision_note": v.DecisionNote,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": out})
}

func (s *Server) listAutomationRunsFor(w http.ResponseWriter, r *http.Request, p *principal, flowID string) {
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	runs, err := s.Store.ListAutomationRuns(r.Context(), p.User.ClusterID, appdb.AutomationRunFilter{
		FlowID: firstNonEmpty(flowID, q.Get("flow_id")), Status: q.Get("status"), Mode: q.Get("mode"), Limit: limit,
	})
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	names := map[string]string{}
	flows, _ := s.Store.ListAutomationFlows(r.Context(), p.User.ClusterID)
	for _, f := range flows {
		names[f.ID] = f.Name
	}
	out := make([]map[string]any, 0, len(runs))
	for _, run := range runs {
		item := runJSON(run, names[run.FlowID])
		delete(item, "report")
		out = append(out, item)
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": out})
}

func (s *Server) listAutomationFlowRuns(w http.ResponseWriter, r *http.Request) {
	p, ok := s.requireAutomation(w, r, rbac.PolicyRead)
	if !ok {
		return
	}
	f, ok := s.loadFlow(w, r, p)
	if !ok {
		return
	}
	s.listAutomationRunsFor(w, r, p, f.ID)
}

func (s *Server) listAutomationRuns(w http.ResponseWriter, r *http.Request) {
	p, ok := s.requireAutomation(w, r, rbac.PolicyRead)
	if !ok {
		return
	}
	s.listAutomationRunsFor(w, r, p, "")
}

func (s *Server) loadRun(w http.ResponseWriter, r *http.Request, p *principal) (*appdb.AutomationRun, string, bool) {
	run, err := s.Store.GetAutomationRun(r.Context(), p.User.ClusterID, r.PathValue("id"))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return nil, "", false
	}
	if run == nil {
		writeErr(w, http.StatusNotFound, "run not found")
		return nil, "", false
	}
	name := ""
	if f, _ := s.Store.GetAutomationFlow(r.Context(), p.User.ClusterID, run.FlowID); f != nil {
		name = f.Name
	}
	return run, name, true
}

func (s *Server) getAutomationRun(w http.ResponseWriter, r *http.Request) {
	p, ok := s.requireAutomation(w, r, rbac.PolicyRead)
	if !ok {
		return
	}
	run, name, ok := s.loadRun(w, r, p)
	if !ok {
		return
	}
	out := runJSON(*run, name)
	out["definition"] = autoDecode(run.Definition)
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) cancelAutomationRun(w http.ResponseWriter, r *http.Request) {
	p, ok := s.requireAutomation(w, r, rbac.PolicyRun)
	if !ok {
		return
	}
	run, name, ok := s.loadRun(w, r, p)
	if !ok {
		return
	}
	if err := s.automation().svc.Cancel(r.Context(), p.User.ClusterID, run.ID, p.User.Username); err != nil {
		writeErr(w, http.StatusConflict, err.Error())
		return
	}
	s.audit(r, p.User.ClusterID, p.User.ID, "automation.run.cancel", "ok", run.ID)
	got, _ := s.Store.GetAutomationRun(r.Context(), p.User.ClusterID, run.ID)
	if got == nil {
		got = run
	}
	writeJSON(w, http.StatusOK, runJSON(*got, name))
}

func (s *Server) retryAutomationRun(w http.ResponseWriter, r *http.Request) {
	p, ok := s.requireAutomation(w, r, rbac.PolicyRun)
	if !ok {
		return
	}
	run, name, ok := s.loadRun(w, r, p)
	if !ok {
		return
	}
	actorID, err := s.ensureAutomationActor(r.Context(), p.User.ClusterID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	res, err := s.automation().svc.Retry(r.Context(), p.User.ClusterID, run.ID, actorID, p.User.Username)
	if err != nil {
		writeErr(w, autoStatus(err), err.Error())
		return
	}
	s.audit(r, p.User.ClusterID, p.User.ID, "automation.run.retry", res.Run.Status, run.ID+" -> "+res.Run.ID)
	writeJSON(w, http.StatusAccepted, runJSON(*res.Run, name))
}

func (s *Server) approveAutomationRun(w http.ResponseWriter, r *http.Request) {
	p, ok := s.requireAutomation(w, r, rbac.PolicyRun)
	if !ok {
		return
	}
	run, name, ok := s.loadRun(w, r, p)
	if !ok {
		return
	}
	var req struct {
		NodeID   string `json:"node_id"`
		Approved *bool  `json:"approved"`
		Note     string `json:"note"`
	}
	if err := readJSON(r, &req); err != nil || req.Approved == nil {
		writeErr(w, http.StatusBadRequest, "approved (true or false) is required")
		return
	}
	st := s.automation()
	if err := st.eng.Approve(r.Context(), p.User.ClusterID, run.ID, req.NodeID, p.User.Username, *req.Approved, req.Note); err != nil {
		writeErr(w, http.StatusConflict, err.Error())
		return
	}
	result := "rejected"
	if *req.Approved {
		result = "approved"
	}
	s.audit(r, p.User.ClusterID, p.User.ID, "automation.run.approve", result, run.ID+" "+req.NodeID)
	s.wakeAutomations()
	got, _ := s.Store.GetAutomationRun(r.Context(), p.User.ClusterID, run.ID)
	if got == nil {
		got = run
	}
	writeJSON(w, http.StatusOK, runJSON(*got, name))
}

func (s *Server) listAutomationRunNodes(w http.ResponseWriter, r *http.Request) {
	p, ok := s.requireAutomation(w, r, rbac.PolicyRead)
	if !ok {
		return
	}
	run, _, ok := s.loadRun(w, r, p)
	if !ok {
		return
	}
	nodes, err := s.Store.ListAutomationNodeRuns(r.Context(), p.User.ClusterID, run.ID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	attempts, _ := s.Store.ListAutomationNodeAttempts(r.Context(), p.User.ClusterID, run.ID)
	byNode := map[string][]map[string]any{}
	for _, a := range attempts {
		byNode[a.NodeRunID] = append(byNode[a.NodeRunID], map[string]any{
			"attempt": a.Attempt, "status": a.Status, "error": a.Error,
			"started_at": a.StartedAt.UTC().Format(time.RFC3339), "finished_at": autoTimeJSON(a.FinishedAt),
		})
	}
	out := make([]map[string]any, 0, len(nodes))
	for _, n := range nodes {
		at := byNode[n.ID]
		if at == nil {
			at = []map[string]any{}
		}
		out = append(out, map[string]any{
			"id": n.ID, "node_id": n.NodeID, "scope": n.Scope, "type": n.Type, "status": n.Status, "port": n.Port,
			"input": autoDecode(n.Input), "output": autoDecode(n.Output), "error": n.Error, "attempts": at,
			"started_at": autoTimeJSON(n.StartedAt), "finished_at": autoTimeJSON(n.FinishedAt),
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": out})
}

func (s *Server) listAutomationRunLogs(w http.ResponseWriter, r *http.Request) {
	p, ok := s.requireAutomation(w, r, rbac.PolicyRead)
	if !ok {
		return
	}
	run, _, ok := s.loadRun(w, r, p)
	if !ok {
		return
	}
	logs, err := s.Store.ListAutomationRunLogs(r.Context(), p.User.ClusterID, run.ID, 1000)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := make([]map[string]any, 0, len(logs))
	for _, l := range logs {
		out = append(out, map[string]any{"node_id": l.NodeID, "level": l.Level, "message": engine.LogLine(l.Message), "at": l.CreatedAt.UTC().Format(time.RFC3339)})
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": out})
}

// --- webhooks ---

func randomSecret() string {
	b := make([]byte, 24)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func (s *Server) getAutomationWebhook(w http.ResponseWriter, r *http.Request) {
	p, ok := s.requireAutomation(w, r, rbac.PolicyRead)
	if !ok {
		return
	}
	f, ok := s.loadFlow(w, r, p)
	if !ok {
		return
	}
	hook, _ := s.Store.GetAutomationWebhook(r.Context(), p.User.ClusterID, f.ID)
	deliveries, _ := s.Store.ListAutomationWebhookDeliveries(r.Context(), p.User.ClusterID, f.ID, flowdef.KeepWebhookDelivery)
	items := make([]map[string]any, 0, len(deliveries))
	for _, d := range deliveries {
		items = append(items, map[string]any{
			"id": d.ID, "status": d.Status, "run_id": d.RunID, "error": d.Error, "payload": autoDecode(d.Payload), "at": d.CreatedAt.UTC().Format(time.RFC3339),
		})
	}
	out := map[string]any{"exists": hook != nil, "deliveries": items, "path_prefix": "/api/v1/hooks/flows/"}
	if hook != nil {
		out["rotated_at"] = hook.RotatedAt.UTC().Format(time.RFC3339)
	}
	writeJSON(w, http.StatusOK, out)
}

// putAutomationWebhook creates the endpoint (or a new token and secret on
// rotate). The token and secret are shown once; only hashes are stored.
func (s *Server) putAutomationWebhook(rotate bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p, ok := s.requireAutomationEdit(w, r)
		if !ok {
			return
		}
		f, ok := s.loadFlow(w, r, p)
		if !ok {
			return
		}
		existing, _ := s.Store.GetAutomationWebhook(r.Context(), p.User.ClusterID, f.ID)
		if existing != nil && !rotate {
			writeErr(w, http.StatusConflict, "the webhook already exists; rotate it to get a new token and secret")
			return
		}
		if existing == nil && rotate {
			writeErr(w, http.StatusNotFound, "the flow has no webhook yet")
			return
		}
		token, secret := randomSecret(), randomSecret()
		if err := s.Store.PutAutomationWebhook(r.Context(), appdb.AutomationWebhook{
			FlowID: f.ID, ClusterID: f.ClusterID, TokenHash: secutil.HashSHA256(token), SecretHash: secutil.HashSHA256(secret),
		}); err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		action := "automation.webhook.create"
		if rotate {
			action = "automation.webhook.rotate"
		}
		s.audit(r, p.User.ClusterID, p.User.ID, action, "ok", f.ID)
		writeJSON(w, http.StatusOK, map[string]any{
			"path": "/api/v1/hooks/flows/" + token, "token": token, "secret": secret, "secret_header": "X-Nodal-Webhook-Secret",
		})
	}
}

// automationHookIn receives an inbound webhook. The token finds the flow
// and the secret (X-Nodal-Webhook-Secret or a Bearer header) must match;
// both are compared as SHA-256 hashes in constant time.
func (s *Server) automationHookIn(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	token := strings.TrimSpace(r.PathValue("token"))
	hook, err := s.Store.GetAutomationWebhookByToken(ctx, secutil.HashSHA256(token))
	if err != nil || hook == nil {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	if !s.automationEnabled(ctx, hook.ClusterID) {
		writeErr(w, http.StatusConflict, "automation is disabled")
		return
	}
	secret := strings.TrimSpace(r.Header.Get("X-Nodal-Webhook-Secret"))
	if secret == "" {
		if auth := r.Header.Get("Authorization"); strings.HasPrefix(strings.ToLower(auth), "bearer ") {
			secret = strings.TrimSpace(auth[7:])
		}
	}
	record := func(status int, runID, msg string, payload any) {
		_ = s.Store.AddAutomationWebhookDelivery(context.WithoutCancel(ctx), appdb.AutomationWebhookDelivery{
			ID: uuid.NewString(), ClusterID: hook.ClusterID, FlowID: hook.FlowID, Status: status, RunID: runID, Error: msg,
			Payload: engine.RedactJSON(payload), CreatedAt: s.now(),
		}, flowdef.KeepWebhookDelivery)
	}
	got := []byte(secutil.HashSHA256(secret))
	want := []byte(hook.SecretHash)
	if secret == "" || subtle.ConstantTimeCompare(got, want) != 1 {
		record(http.StatusUnauthorized, "", "the secret does not match", map[string]any{"remote": r.RemoteAddr})
		writeErr(w, http.StatusUnauthorized, "the secret does not match")
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, flowdef.MaxWebhookBodyBytes+1))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "could not read the body")
		return
	}
	if len(body) > flowdef.MaxWebhookBodyBytes {
		record(http.StatusRequestEntityTooLarge, "", "the body is larger than 1 MB", map[string]any{"bytes": len(body)})
		writeErr(w, http.StatusRequestEntityTooLarge, "the body is larger than 1 MB")
		return
	}
	payload := map[string]any{}
	if len(strings.TrimSpace(string(body))) > 0 {
		var v any
		if json.Unmarshal(body, &v) != nil {
			payload["body"] = string(body)
		} else if m, ok := v.(map[string]any); ok {
			payload = m
		} else {
			payload["body"] = v
		}
	}
	headers := map[string]any{}
	for _, h := range []string{"Content-Type", "User-Agent", "X-GitHub-Event", "X-Request-Id"} {
		if v := r.Header.Get(h); v != "" {
			headers[strings.ToLower(h)] = v
		}
	}
	payload["headers"] = headers
	key := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if key == "" {
		if k, ok := payload["idempotency_key"].(string); ok {
			key = k
		}
	}
	actorID, _ := s.ensureAutomationActor(ctx, hook.ClusterID)
	res, err := s.automation().svc.Start(ctx, engine.StartRequest{
		ClusterID: hook.ClusterID, FlowID: hook.FlowID, TriggerKind: "webhook", Payload: payload, IdempotencyKey: key,
		ActorID: actorID, StartedBy: "webhook",
	})
	if err != nil {
		status := autoStatus(err)
		record(status, "", err.Error(), payload)
		writeErr(w, status, err.Error())
		return
	}
	status := http.StatusAccepted
	if res.Dropped {
		status = http.StatusTooManyRequests
	}
	record(status, res.Run.ID, res.Run.Error, payload)
	writeJSON(w, status, map[string]any{"run_id": res.Run.ID, "status": res.Run.Status, "existing": res.Existing})
}

// --- connections ---

func connectionJSON(c appdb.AutomationConnection, fields []string) map[string]any {
	if fields == nil {
		fields = []string{}
	}
	return map[string]any{
		"id": c.ID, "name": c.Name, "kind": c.Kind, "config": autoDecode(c.Config), "secret_fields": fields,
		"created_by": c.CreatedBy, "created_at": c.CreatedAt.UTC().Format(time.RFC3339), "updated_at": c.UpdatedAt.UTC().Format(time.RFC3339),
	}
}

// secretFields names (never shows) the secret fields a connection holds.
func (s *Server) secretFields(ctx context.Context, c appdb.AutomationConnection) []string {
	if c.Sealed == "" {
		return nil
	}
	plain, err := s.cfSealer().Open(automationConnectionAAD(c.ClusterID, c.ID), c.Sealed)
	if err != nil {
		return []string{"(cannot be read)"}
	}
	var m map[string]any
	_ = json.Unmarshal([]byte(plain), &m)
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j] < out[j-1]; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

type connectionBody struct {
	Name   *string        `json:"name"`
	Kind   string         `json:"kind"`
	Config map[string]any `json:"config"`
	Secret map[string]any `json:"secret"`
}

var connectionKinds = map[string]bool{"http": true, "bearer": true, "basic": true, "headers": true}

func (s *Server) sealConnection(c *appdb.AutomationConnection, secret map[string]any) error {
	var cfg map[string]any
	_ = json.Unmarshal(c.Config, &cfg)
	for k := range cfg {
		if engine.SecretKey(k) {
			return fmt.Errorf("put %s under secret, not config", k)
		}
	}
	if len(secret) == 0 {
		c.Sealed = ""
		return nil
	}
	raw, _ := json.Marshal(secret)
	sealed, err := s.cfSealer().Seal(automationConnectionAAD(c.ClusterID, c.ID), string(raw))
	if err != nil {
		return err
	}
	c.Sealed = sealed
	return nil
}

func (s *Server) listAutomationConnections(w http.ResponseWriter, r *http.Request) {
	p, ok := s.requireAutomation(w, r, rbac.PolicyRead)
	if !ok {
		return
	}
	items, err := s.Store.ListAutomationConnections(r.Context(), p.User.ClusterID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := make([]map[string]any, 0, len(items))
	for _, c := range items {
		out = append(out, connectionJSON(c, s.secretFields(r.Context(), c)))
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": out})
}

func (s *Server) createAutomationConnection(w http.ResponseWriter, r *http.Request) {
	p, ok := s.requireAutomationEdit(w, r)
	if !ok {
		return
	}
	var req connectionBody
	if err := readJSON(r, &req); err != nil || req.Name == nil || strings.TrimSpace(*req.Name) == "" {
		writeErr(w, http.StatusBadRequest, "name is required")
		return
	}
	kind := firstNonEmpty(req.Kind, "http")
	if !connectionKinds[kind] {
		writeErr(w, http.StatusBadRequest, "kind must be http, bearer, basic or headers")
		return
	}
	cfg, _ := json.Marshal(nonNilMap(req.Config))
	c := appdb.AutomationConnection{ID: uuid.NewString(), ClusterID: p.User.ClusterID, Name: strings.TrimSpace(*req.Name), Kind: kind, Config: cfg, CreatedBy: p.User.Username}
	if err := s.sealConnection(&c, req.Secret); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.Store.CreateAutomationConnection(r.Context(), c); err != nil {
		writeErr(w, http.StatusConflict, err.Error())
		return
	}
	s.audit(r, p.User.ClusterID, p.User.ID, "automation.connection.create", "ok", c.ID+" "+c.Name)
	writeJSON(w, http.StatusCreated, connectionJSON(c, s.secretFields(r.Context(), c)))
}

func (s *Server) loadConnection(w http.ResponseWriter, r *http.Request, p *principal) (*appdb.AutomationConnection, bool) {
	c, err := s.Store.GetAutomationConnection(r.Context(), p.User.ClusterID, r.PathValue("id"))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return nil, false
	}
	if c == nil {
		writeErr(w, http.StatusNotFound, "connection not found")
		return nil, false
	}
	return c, true
}

func (s *Server) patchAutomationConnection(w http.ResponseWriter, r *http.Request) {
	p, ok := s.requireAutomationEdit(w, r)
	if !ok {
		return
	}
	c, ok := s.loadConnection(w, r, p)
	if !ok {
		return
	}
	var req connectionBody
	if err := readJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request")
		return
	}
	if req.Name != nil && strings.TrimSpace(*req.Name) != "" {
		c.Name = strings.TrimSpace(*req.Name)
	}
	if req.Kind != "" {
		if !connectionKinds[req.Kind] {
			writeErr(w, http.StatusBadRequest, "kind must be http, bearer, basic or headers")
			return
		}
		c.Kind = req.Kind
	}
	if req.Config != nil {
		c.Config, _ = json.Marshal(req.Config)
	}
	if req.Secret != nil {
		if err := s.sealConnection(c, req.Secret); err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	if err := s.Store.UpdateAutomationConnection(r.Context(), *c); err != nil {
		writeErr(w, http.StatusConflict, err.Error())
		return
	}
	s.audit(r, p.User.ClusterID, p.User.ID, "automation.connection.update", "ok", c.ID)
	writeJSON(w, http.StatusOK, connectionJSON(*c, s.secretFields(r.Context(), *c)))
}

func (s *Server) deleteAutomationConnection(w http.ResponseWriter, r *http.Request) {
	p, ok := s.requireAutomationEdit(w, r)
	if !ok {
		return
	}
	c, ok := s.loadConnection(w, r, p)
	if !ok {
		return
	}
	if err := s.Store.DeleteAutomationConnection(r.Context(), p.User.ClusterID, c.ID); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.audit(r, p.User.ClusterID, p.User.ID, "automation.connection.delete", "ok", c.ID+" "+c.Name)
	w.WriteHeader(http.StatusNoContent)
}

// testAutomationConnection opens the credential and, when the connection
// has a test_url, sends one GET with it through the SSRF-safe client.
func (s *Server) testAutomationConnection(w http.ResponseWriter, r *http.Request) {
	p, ok := s.requireAutomationEdit(w, r)
	if !ok {
		return
	}
	c, ok := s.loadConnection(w, r, p)
	if !ok {
		return
	}
	conn, err := s.openAutomationConnection(r.Context(), p.User.ClusterID, c.ID)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	testURL, _ := conn["test_url"].(string)
	if strings.TrimSpace(testURL) == "" {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "message": "the credential opens; add a test_url to the config to try it against a server"})
		return
	}
	reg := s.automation().reg
	t, _ := reg.Get("action.http")
	in := &engine.ExecInput{
		ClusterID: p.User.ClusterID, Config: map[string]any{"url": testURL, "method": "GET", "connection": c.ID, "fail_on_status": false, "timeout_ms": 10000,
			"allow_private_network": conn["allow_private_network"] == true},
		Connection: func(id string) (map[string]any, error) { return conn, nil }, Now: s.now(),
	}
	res, err := t.Execute(r.Context(), in)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	out, _ := res.Output.(map[string]any)
	code, _ := out["status"].(int)
	s.audit(r, p.User.ClusterID, p.User.ID, "automation.connection.test", strconv.Itoa(code), c.ID)
	writeJSON(w, http.StatusOK, map[string]any{"ok": code > 0 && code < 400, "status": code})
}

func nonNilMap(m map[string]any) map[string]any {
	if m == nil {
		return map[string]any{}
	}
	return m
}

// --- catalogue, targets, templates ---

var eventLabels = map[string][2]string{
	"workload.started":           {"A workload started", "workload_id, name, kind, node_id"},
	"workload.stopped":           {"A workload stopped", "workload_id, name, kind, node_id"},
	"workload.restarted":         {"A workload restarted", "workload_id, name, kind, node_id"},
	"workload.deleted":           {"A workload was deleted", "workload_id, name, kind, node_id"},
	"uptime.monitor_down":        {"An Uptime monitor went down", "monitor_id, name, target, status, error, failures"},
	"uptime.monitor_recovered":   {"An Uptime monitor recovered", "monitor_id, name, target, status"},
	"docker.container_unhealthy": {"A Docker container became unhealthy", "ref, name, machine_id, machine_name, project, health, reason"},
	"backup.succeeded":           {"A backup succeeded", "workload_id, workload_name, run_id, policy_id, status"},
	"backup.failed":              {"A backup failed", "workload_id, workload_name, run_id, policy_id, error"},
	"storage.pool_usage_high":    {"A storage pool passed 70, 75, 80, 85, 90 or 95% used", "pool_id, pool_name, percent, band, backend"},
}

func (s *Server) automationCatalogue(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAutomation(w, r, rbac.PolicyRead); !ok {
		return
	}
	events := make([]map[string]any, 0, len(flowdef.EventKinds))
	for _, k := range flowdef.EventKinds {
		l := eventLabels[k]
		fields := strings.Split(l[1], ", ")
		events = append(events, map[string]any{"kind": k, "label": l[0], "fields": fields})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"items":  s.automation().reg.Catalogue(),
		"events": events,
		"triggers": []map[string]any{
			{"type": flowdef.TriggerManual, "label": "Run by hand"},
			{"type": flowdef.TriggerSchedule, "label": "On a schedule"},
			{"type": flowdef.TriggerWebhook, "label": "When a webhook is called"},
			{"type": flowdef.TriggerEvent, "label": "When something happens in No-dal"},
		},
		"schedule_presets": flowdef.SchedulePresets,
		"condition_ops":    flowdef.ConditionOps,
		"limits":           flowdef.LimitCaps(),
		"fixed_limits": map[string]any{
			"max_running_flows": flowdef.MaxRunningFlows, "max_chain_depth": flowdef.MaxChainDepth,
			"destructive_per_target": flowdef.DestructivePerTarget, "destructive_window_minutes": int(flowdef.DestructiveWindow.Minutes()),
			"max_nodes": flowdef.MaxNodes, "max_webhook_body_bytes": flowdef.MaxWebhookBodyBytes,
		},
	})
}

func (s *Server) automationTargetsAPI(w http.ResponseWriter, r *http.Request) {
	p, ok := s.requireAutomation(w, r, rbac.PolicyRead)
	if !ok {
		return
	}
	kind := r.URL.Query().Get("kind")
	if kind == "" {
		writeErr(w, http.StatusBadRequest, "kind is required")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": s.automationTargets(r.Context(), p.User.ClusterID, kind)})
}

func (s *Server) automationTemplatesAPI(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAutomation(w, r, rbac.PolicyRead); !ok {
		return
	}
	out := make([]map[string]any, 0, len(automationTemplates))
	for _, t := range automationTemplates {
		out = append(out, map[string]any{"id": t.ID, "name": t.Name, "description": t.Description, "definition": t.definition(), "limits": t.Limits.Normalized()})
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": out})
}
