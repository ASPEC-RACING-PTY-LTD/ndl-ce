package httpapi

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/google/uuid"
	"github.com/no-dal/ndl-ce/internal/ai"
	"github.com/no-dal/ndl-ce/internal/appdb"
	"github.com/no-dal/ndl-ce/internal/rbac"
)

func (s *Server) createAIPlan(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.AIAsk)
	if err != nil {
		return
	}
	var req struct {
		Prompt    string `json:"prompt"`
		ProfileID string `json:"profile_id"`
	}
	if err := readJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request")
		return
	}
	prompt := strings.TrimSpace(req.Prompt)
	if prompt == "" {
		writeErr(w, http.StatusBadRequest, "prompt is required")
		return
	}
	var prof *appdb.AIProfile
	if strings.TrimSpace(req.ProfileID) != "" {
		prof, _ = s.Store.GetAIProfile(r.Context(), p.User.ClusterID, req.ProfileID)
		if prof == nil {
			writeErr(w, http.StatusNotFound, "profile not found")
			return
		}
	}
	nodeID, nodeName := s.matchPlanNode(r, p.User.ClusterID, prompt)
	storeAppID := s.matchPlanStoreApp(r, p.User.ClusterID, prompt)
	compiled, err := ai.CompilePlan(prompt, nodeID, nodeName, storeAppID)
	if err != nil {
		writeErr(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	if prof != nil && ai.ForbidsMutatePlans(prof.Mode) && ai.PlanMutates(compiled) {
		writeErr(w, http.StatusForbidden, "ask profile cannot operate")
		return
	}
	plan := appdb.AIPlan{
		ID: uuid.NewString(), ClusterID: p.User.ClusterID, ProfileID: strings.TrimSpace(req.ProfileID),
		Prompt: prompt, Status: appdb.PlanPreview, ActorType: ai.ActorTypeAI, CreatedBy: p.User.ID,
	}
	steps := make([]appdb.AIPlanStep, 0, len(compiled))
	for _, st := range compiled {
		body, _ := json.Marshal(st.Body)
		steps = append(steps, appdb.AIPlanStep{
			ID: uuid.NewString(), ClusterID: p.User.ClusterID, PlanID: plan.ID, Ordinal: st.Ordinal,
			Action: st.Action, Permission: st.Permission, Method: st.Method, Path: st.Path,
			Title: st.Title, BodyJSON: string(body), Status: appdb.PlanPreview,
		})
	}
	if err := s.Store.CreateAIPlan(r.Context(), plan, steps); err != nil {
		writeErr(w, http.StatusConflict, "could not record plan")
		return
	}
	s.audit(r, p.User.ClusterID, p.User.ID, "ai.plan.create", "ok", plan.ID)
	writeJSON(w, http.StatusCreated, s.planJSON(r, plan))
}

func (s *Server) listAIPlans(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.AIAsk)
	if err != nil {
		return
	}
	items, err := s.Store.ListAIPlans(r.Context(), p.User.ClusterID, 50)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := make([]map[string]any, 0, len(items))
	for _, plan := range items {
		out = append(out, s.planJSON(r, plan))
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": out})
}

func (s *Server) getAIPlan(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.AIAsk)
	if err != nil {
		return
	}
	plan, err := s.Store.GetAIPlan(r.Context(), p.User.ClusterID, r.PathValue("id"))
	if err != nil || plan == nil {
		writeErr(w, http.StatusNotFound, "plan not found")
		return
	}
	writeJSON(w, http.StatusOK, s.planJSON(r, *plan))
}

func (s *Server) approveAIPlan(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.AIManage)
	if err != nil {
		return
	}
	if strings.TrimSpace(r.Header.Get(confirmHeader)) != ai.ApproveConfirm {
		writeErr(w, http.StatusUnprocessableEntity, "approving a plan requires X-Nodal-Confirm: approve-plan")
		return
	}
	plan, err := s.Store.GetAIPlan(r.Context(), p.User.ClusterID, r.PathValue("id"))
	if err != nil || plan == nil {
		writeErr(w, http.StatusNotFound, "plan not found")
		return
	}
	if plan.Status != appdb.PlanPreview {
		writeErr(w, http.StatusConflict, "plan is not awaiting approval")
		return
	}
	var profileGrants []string
	if plan.ProfileID != "" {
		prof, _ := s.Store.GetAIProfile(r.Context(), p.User.ClusterID, plan.ProfileID)
		if prof == nil {
			writeErr(w, http.StatusNotFound, "profile not found")
			return
		}
		if prof.Mode != ai.ModeOperate {
			writeErr(w, http.StatusForbidden, "ask profile cannot operate")
			return
		}
		profileGrants = prof.Grants
	}
	steps, _ := s.Store.ListAIPlanSteps(r.Context(), p.User.ClusterID, plan.ID)
	for i := range steps {
		st := steps[i]
		if !rbac.Authorize(p.Grants, st.Permission) || (len(profileGrants) > 0 && !rbac.Authorize(profileGrants, st.Permission)) {
			st.Status = appdb.PlanDenied
			st.Reason = "plan cannot call missing permissions"
			if err := s.Store.UpdateAIPlanStep(r.Context(), st); err != nil {
				writeErr(w, http.StatusInternalServerError, "could not record AI plan")
				return
			}
			plan.Status = appdb.PlanStopped
			plan.Reason = st.Reason
			if err := s.Store.UpdateAIPlan(r.Context(), *plan); err != nil {
				writeErr(w, http.StatusInternalServerError, "could not record AI plan")
				return
			}
			s.audit(r, p.User.ClusterID, p.User.ID, "ai.plan.approve", "stopped", plan.ID)
			writeJSON(w, http.StatusOK, s.planJSON(r, *plan))
			return
		}
	}
	plan.Status = appdb.PlanExecuting
	if err := s.Store.UpdateAIPlan(r.Context(), *plan); err != nil {
		writeErr(w, http.StatusInternalServerError, "could not record AI plan")
		return
	}
	var createdWL string
	for i := range steps {
		st := steps[i]
		opID, execErr := s.executePlanStep(r, p.User.ClusterID, st, &createdWL)
		if execErr != nil {
			st.Status = appdb.PlanFailed
			st.Reason = execErr.Error()
			st.OperationID = opID
			if err := s.Store.UpdateAIPlanStep(r.Context(), st); err != nil {
				writeErr(w, http.StatusInternalServerError, "could not record AI plan")
				return
			}
			plan.Status = appdb.PlanStopped
			plan.Reason = "partial plan failure stopped"
			if err := s.Store.UpdateAIPlan(r.Context(), *plan); err != nil {
				writeErr(w, http.StatusInternalServerError, "could not record AI plan")
				return
			}
			s.audit(r, p.User.ClusterID, p.User.ID, "ai.plan.approve", "stopped", plan.ID)
			writeJSON(w, http.StatusOK, s.planJSON(r, *plan))
			return
		}
		st.Status = appdb.PlanSucceeded
		st.OperationID = opID
		if err := s.Store.UpdateAIPlanStep(r.Context(), st); err != nil {
			writeErr(w, http.StatusInternalServerError, "could not record AI plan")
			return
		}
		steps[i] = st
	}
	plan.Status = appdb.PlanSucceeded
	plan.Reason = "approved and executed existing APIs"
	if err := s.Store.UpdateAIPlan(r.Context(), *plan); err != nil {
		writeErr(w, http.StatusInternalServerError, "could not record AI plan")
		return
	}
	s.audit(r, p.User.ClusterID, p.User.ID, "ai.plan.approve", "ok", plan.ID)
	writeJSON(w, http.StatusOK, s.planJSON(r, *plan))
}

func (s *Server) executePlanStep(r *http.Request, clusterID string, st appdb.AIPlanStep, createdWL *string) (string, error) {
	var body map[string]any
	_ = json.Unmarshal([]byte(st.BodyJSON), &body)
	if err := planBodyForbidden(body); err != nil {
		return "", err
	}
	nodeID, _ := body["node_id"].(string)
	op := s.startOp(r.Context(), clusterID, nodeID, "ai."+st.Action, "queued", 0)
	op.State = "queued"
	op.Message = st.Title
	if err := s.Store.UpsertOperation(r.Context(), op); err != nil {
		return "", errInternal("could not record AI plan task")
	}
	fail := func(err error) (string, error) {
		s.finishOp(r.Context(), op, "failed", err.Error(), 0)
		return op.ID, err
	}
	switch st.Action {
	case ai.ActionCreateWorkload:
		id, err := s.executeCreateWorkload(r, body)
		if err != nil {
			return fail(err)
		}
		if createdWL != nil {
			*createdWL = id
		}
	case ai.ActionCreatePolicy:
		payload := st.BodyJSON
		if strings.TrimSpace(payload) == "" {
			payload = "{}"
		}
		code, raw := s.invokeExistingAPI(r, s.createAutomationPolicy, http.MethodPost, "/api/v1/policies", []byte(payload), "")
		if err := existingAPIError(code, raw); err != nil {
			return fail(err)
		}
	case ai.ActionRestart:
		wlID := planWorkloadID(body, createdWL)
		if wlID == "" {
			return fail(errPlan("restart workload id is missing"))
		}
		code, raw := s.invokeExistingAPI(r, s.lifecycleWorkload("restart"), http.MethodPost, "/api/v1/workloads/"+wlID+"/restart", []byte(`{}`), wlID)
		if err := existingAPIError(code, raw); err != nil {
			return fail(err)
		}
	case ai.ActionInstallStore:
		appID := storeAppIDFromStep(st, body)
		if appID == "" {
			return fail(errUnprocessable("store install must use POST /api/v1/store/apps/{id}/install"))
		}
		payload := st.BodyJSON
		if strings.TrimSpace(payload) == "" {
			payload = "{}"
		}
		code, raw := s.invokeExistingAPI(r, s.installStoreApp, http.MethodPost, "/api/v1/store/apps/"+appID+"/install", []byte(payload), appID)
		if err := existingAPIError(code, raw); err != nil {
			return fail(err)
		}
		if createdWL != nil {
			if id := existingAPIID(raw, "workload_id"); id != "" {
				*createdWL = id
			}
		}
	default:
		return fail(errPlan("plan action is unsupported"))
	}
	s.finishOp(r.Context(), op, "succeeded", st.Title, 100)
	return op.ID, nil
}

func (s *Server) executeCreateWorkload(r *http.Request, body map[string]any) (string, error) {
	kind, _ := body["kind"].(string)
	if strings.TrimSpace(kind) == "" {
		return "", errPlan("kind is required")
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return "", err
	}
	code, out := s.invokeExistingAPI(r, s.createWorkload, http.MethodPost, "/api/v1/workloads", raw, "")
	if err := existingAPIError(code, out); err != nil {
		return "", err
	}
	id := existingAPIID(out, "id")
	if id == "" {
		return "", errPlan("workload id is missing")
	}
	return id, nil
}

type planError string

func (e planError) Error() string { return string(e) }

func errPlan(s string) error { return planError(s) }

func (s *Server) matchPlanStoreApp(r *http.Request, clusterID, prompt string) string {
	pkgs, _ := s.Store.ListStorePackages(r.Context(), clusterID)
	lower := strings.ToLower(prompt)
	for _, pkg := range pkgs {
		if pkg.Name != "" && strings.Contains(lower, strings.ToLower(pkg.Name)) {
			return pkg.ID
		}
		if pkg.Title != "" && strings.Contains(lower, strings.ToLower(pkg.Title)) {
			return pkg.ID
		}
	}
	return ""
}

func (s *Server) invokeExistingAPI(r *http.Request, handler http.HandlerFunc, method, path string, body []byte, pathID string) (int, []byte) {
	rec := &captureResponse{}
	req := r.Clone(r.Context())
	req.Method = method
	if req.URL != nil {
		u := *req.URL
		if path != "" {
			u.Path = path
		}
		req.URL = &u
	}
	req.Body = io.NopCloser(bytes.NewReader(body))
	req.ContentLength = int64(len(body))
	req.GetBody = func() (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(body)), nil
	}
	if req.Header == nil {
		req.Header = make(http.Header)
	} else {
		req.Header = req.Header.Clone()
	}
	req.Header.Set("Content-Type", "application/json")
	if pathID != "" {
		req.SetPathValue("id", pathID)
	}
	handler(rec, req)
	return rec.code, rec.buf.Bytes()
}

type captureResponse struct {
	code int
	hdr  http.Header
	buf  bytes.Buffer
}

func (c *captureResponse) Header() http.Header {
	if c.hdr == nil {
		c.hdr = make(http.Header)
	}
	return c.hdr
}

func (c *captureResponse) Write(b []byte) (int, error) {
	if c.code == 0 {
		c.code = http.StatusOK
	}
	return c.buf.Write(b)
}

func (c *captureResponse) WriteHeader(status int) {
	if c.code != 0 {
		return
	}
	c.code = status
}

func existingAPIError(code int, raw []byte) error {
	if code == 0 {
		return errPlan("existing API produced no HTTP status")
	}
	if code < 400 {
		return nil
	}
	var m map[string]string
	if json.Unmarshal(raw, &m) == nil && strings.TrimSpace(m["error"]) != "" {
		return errPlan(m["error"])
	}
	if msg := strings.TrimSpace(string(raw)); msg != "" {
		return errPlan(msg)
	}
	return errPlan(http.StatusText(code))
}

func existingAPIID(raw []byte, key string) string {
	var m map[string]any
	if json.Unmarshal(raw, &m) != nil {
		return ""
	}
	id, _ := m[key].(string)
	return strings.TrimSpace(id)
}

func planWorkloadID(body map[string]any, createdWL *string) string {
	for _, key := range []string{"workload_id", "id"} {
		if id, _ := body[key].(string); strings.TrimSpace(id) != "" {
			return strings.TrimSpace(id)
		}
	}
	if createdWL != nil {
		return strings.TrimSpace(*createdWL)
	}
	return ""
}

func storeAppIDFromStep(st appdb.AIPlanStep, body map[string]any) string {
	if id, _ := body["store_app_id"].(string); strings.TrimSpace(id) != "" {
		return strings.TrimSpace(id)
	}
	const prefix = "/api/v1/store/apps/"
	const suffix = "/install"
	if strings.HasPrefix(st.Path, prefix) && strings.HasSuffix(st.Path, suffix) {
		id := strings.TrimSuffix(strings.TrimPrefix(st.Path, prefix), suffix)
		return strings.TrimSpace(id)
	}
	return ""
}

func planBodyForbidden(body map[string]any) error {
	raw, _ := json.Marshal(body)
	lower := strings.ToLower(string(raw))
	for _, bad := range []string{"host.exec", "host_exec", "/bin/sh", "/bin/bash"} {
		if strings.Contains(lower, bad) {
			return errPlan("plan cannot include " + bad)
		}
	}
	return nil
}

func (s *Server) matchPlanNode(r *http.Request, clusterID, prompt string) (string, string) {
	nodes, _ := s.Store.ListClusterNodes(r.Context(), clusterID)
	lower := strings.ToLower(prompt)
	for _, n := range nodes {
		if n.Name != "" && strings.Contains(lower, strings.ToLower(n.Name)) {
			return n.ID, n.Name
		}
	}
	return "", ""
}

func (s *Server) planJSON(r *http.Request, plan appdb.AIPlan) map[string]any {
	steps, _ := s.Store.ListAIPlanSteps(r.Context(), plan.ClusterID, plan.ID)
	outSteps := make([]map[string]any, 0, len(steps))
	for _, st := range steps {
		var body any
		if err := json.Unmarshal([]byte(st.BodyJSON), &body); err != nil {
			body = map[string]any{}
		}
		outSteps = append(outSteps, map[string]any{
			"id": st.ID, "ordinal": st.Ordinal, "action": st.Action, "permission": st.Permission,
			"method": st.Method, "path": st.Path, "title": st.Title, "body": body,
			"status": st.Status, "reason": st.Reason, "operation_id": st.OperationID,
		})
	}
	return map[string]any{
		"id": plan.ID, "prompt": plan.Prompt, "status": plan.Status, "actor_type": plan.ActorType,
		"reason": plan.Reason, "profile_id": plan.ProfileID, "steps": outSteps,
	}
}
