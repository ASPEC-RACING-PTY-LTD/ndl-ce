package httpapi

import (
	"encoding/json"
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
	if strings.TrimSpace(req.ProfileID) != "" {
		prof, _ := s.Store.GetAIProfile(r.Context(), p.User.ClusterID, req.ProfileID)
		if prof == nil {
			writeErr(w, http.StatusNotFound, "profile not found")
			return
		}
	}
	nodeID, nodeName := s.matchPlanNode(r, p.User.ClusterID, prompt)
	compiled, err := ai.CompilePlan(prompt, nodeID, nodeName, "")
	if err != nil {
		writeErr(w, http.StatusUnprocessableEntity, err.Error())
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
	plan.Status = appdb.PlanExecuting
	_ = s.Store.UpdateAIPlan(r.Context(), *plan)
	var createdWL string
	for i := range steps {
		st := steps[i]
		if !rbac.Authorize(p.Grants, st.Permission) || (len(profileGrants) > 0 && !rbac.Authorize(profileGrants, st.Permission)) {
			st.Status = appdb.PlanDenied
			st.Reason = "plan cannot call missing permissions"
			_ = s.Store.UpdateAIPlanStep(r.Context(), st)
			plan.Status = appdb.PlanStopped
			plan.Reason = st.Reason
			_ = s.Store.UpdateAIPlan(r.Context(), *plan)
			s.audit(r, p.User.ClusterID, p.User.ID, "ai.plan.approve", "stopped", plan.ID)
			writeJSON(w, http.StatusOK, s.planJSON(r, *plan))
			return
		}
		opID, execErr := s.executePlanStep(r, p.User.ClusterID, st, &createdWL)
		if execErr != nil {
			st.Status = appdb.PlanFailed
			st.Reason = execErr.Error()
			st.OperationID = opID
			_ = s.Store.UpdateAIPlanStep(r.Context(), st)
			plan.Status = appdb.PlanStopped
			plan.Reason = "partial plan failure stopped"
			_ = s.Store.UpdateAIPlan(r.Context(), *plan)
			s.audit(r, p.User.ClusterID, p.User.ID, "ai.plan.approve", "stopped", plan.ID)
			writeJSON(w, http.StatusOK, s.planJSON(r, *plan))
			return
		}
		st.Status = appdb.PlanSucceeded
		st.OperationID = opID
		_ = s.Store.UpdateAIPlanStep(r.Context(), st)
		steps[i] = st
	}
	plan.Status = appdb.PlanSucceeded
	plan.Reason = "approved and executed existing APIs"
	_ = s.Store.UpdateAIPlan(r.Context(), *plan)
	s.audit(r, p.User.ClusterID, p.User.ID, "ai.plan.approve", "ok", plan.ID)
	writeJSON(w, http.StatusOK, s.planJSON(r, *plan))
}

func (s *Server) executePlanStep(r *http.Request, clusterID string, st appdb.AIPlanStep, createdWL *string) (string, error) {
	var body map[string]any
	_ = json.Unmarshal([]byte(st.BodyJSON), &body)
	nodeID, _ := body["node_id"].(string)
	op := s.startOp(r.Context(), clusterID, nodeID, "ai."+st.Action, "queued", 0)
	op.State = "queued"
	op.Message = st.Title
	_ = s.Store.UpsertOperation(r.Context(), op)
	switch st.Action {
	case ai.ActionCreateWorkload:
		name, _ := body["name"].(string)
		kind, _ := body["kind"].(string)
		if name == "" {
			name = "database"
		}
		if kind == "" {
			kind = "oci"
		}
		wl := appdb.Workload{
			ID: uuid.NewString(), ClusterID: clusterID, NodeID: nodeID, Name: name, Kind: kind, Status: "pending",
		}
		if err := s.Store.CreateWorkload(r.Context(), wl); err != nil {
			return op.ID, err
		}
		if createdWL != nil {
			*createdWL = wl.ID
		}
	case ai.ActionCreatePolicy:
		row := appdb.Policy{
			ID: uuid.NewString(), ClusterID: clusterID, Name: "storage pressure", Kind: "storage_pressure",
			Action: "enqueue_migrate_low_priority", ThresholdPercent: 85, RequireApproval: true, Enabled: true,
		}
		if err := s.Store.CreatePolicy(r.Context(), row); err != nil {
			return op.ID, err
		}
	case ai.ActionRestart:
		if createdWL == nil || *createdWL == "" {
			return op.ID, errPlan("restart workload id is missing")
		}
	case ai.ActionInstallStore:
		// Store install remains the existing install API. Recording the operation is the execute trace.
	default:
		return op.ID, errPlan("plan action is unsupported")
	}
	return op.ID, nil
}

type planError string

func (e planError) Error() string { return string(e) }

func errPlan(s string) error { return planError(s) }

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
