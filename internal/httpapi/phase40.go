package httpapi

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/google/uuid"
	"github.com/no-dal/ndl-ce/internal/appdb"
	"github.com/no-dal/ndl-ce/internal/automation"
	"github.com/no-dal/ndl-ce/internal/rbac"
)

func (s *Server) listAutomationPolicies(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.PolicyRead)
	if err != nil {
		return
	}
	items, err := s.Store.ListPolicies(r.Context(), p.User.ClusterID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := make([]map[string]any, 0, len(items))
	for _, pol := range items {
		out = append(out, automationPolicyJSON(pol))
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": out})
}

func (s *Server) createAutomationPolicy(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.PolicyApply)
	if err != nil {
		return
	}
	var req struct {
		Name             string `json:"name"`
		Kind             string `json:"kind"`
		Action           string `json:"action"`
		ThresholdPercent int    `json:"threshold_percent"`
		RequireApproval  bool   `json:"require_approval"`
		YAML             string `json:"yaml"`
	}
	if err := readJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request")
		return
	}
	var spec *automation.Spec
	if strings.TrimSpace(req.YAML) != "" {
		spec, err = automation.ParseYAML([]byte(req.YAML))
	} else {
		spec, err = automation.ParseJSONMap(req.Kind, req.Action, req.ThresholdPercent, req.RequireApproval)
	}
	if err != nil {
		writeErr(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		writeErr(w, http.StatusBadRequest, "name is required")
		return
	}
	row := appdb.Policy{
		ID: uuid.NewString(), ClusterID: p.User.ClusterID, Name: name,
		Kind: spec.Kind, Action: spec.Action, ThresholdPercent: spec.ThresholdPercent,
		RequireApproval: spec.RequireApproval, Enabled: true, SpecYAML: strings.TrimSpace(req.YAML),
	}
	if err := s.Store.CreatePolicy(r.Context(), row); err != nil {
		writeErr(w, http.StatusConflict, "could not record policy")
		return
	}
	s.audit(r, p.User.ClusterID, p.User.ID, "policy.create", "ok", row.ID)
	writeJSON(w, http.StatusCreated, automationPolicyJSON(row))
}

func (s *Server) applyAutomationPolicy(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.PolicyApply)
	if err != nil {
		return
	}
	pol, err := s.Store.GetPolicy(r.Context(), p.User.ClusterID, r.PathValue("id"))
	if err != nil || pol == nil {
		writeErr(w, http.StatusNotFound, "policy not found")
		return
	}
	actorID, aerr := s.ensureAutomationActor(r.Context(), p.User.ClusterID)
	if aerr != nil {
		writeErr(w, http.StatusInternalServerError, aerr.Error())
		return
	}
	if !pol.Enabled {
		run := appdb.PolicyRun{
			ID: uuid.NewString(), ClusterID: p.User.ClusterID, PolicyID: pol.ID, ActorID: actorID,
			Status: appdb.PolicySkipped, Reason: "policy is disabled",
		}
		_ = s.Store.CreatePolicyRun(r.Context(), run)
		s.audit(r, p.User.ClusterID, actorID, "policy.apply", run.Status, pol.ID)
		writeJSON(w, http.StatusOK, automationPolicyRunJSON(run))
		return
	}
	if pol.RequireApproval && strings.TrimSpace(r.Header.Get(confirmHeader)) != automation.ApplyConfirm {
		run := appdb.PolicyRun{
			ID: uuid.NewString(), ClusterID: p.User.ClusterID, PolicyID: pol.ID, ActorID: actorID,
			Status: appdb.PolicyPending, Reason: "approval required. Send X-Nodal-Confirm: apply-policy to evaluate.",
		}
		_ = s.Store.CreatePolicyRun(r.Context(), run)
		s.audit(r, p.User.ClusterID, p.User.ID, "policy.apply", "pending", pol.ID)
		writeJSON(w, http.StatusAccepted, automationPolicyRunJSON(run))
		return
	}
	run := s.evaluatePolicy(r.Context(), p.User.ClusterID, actorID, *pol)
	_ = s.Store.CreatePolicyRun(r.Context(), run)
	s.audit(r, p.User.ClusterID, actorID, "policy.apply", run.Status, pol.ID)
	writeJSON(w, http.StatusOK, automationPolicyRunJSON(run))
}

func (s *Server) listPolicyRuns(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.PolicyRead)
	if err != nil {
		return
	}
	items, err := s.Store.ListPolicyRuns(r.Context(), p.User.ClusterID, 50)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := make([]map[string]any, 0, len(items))
	for _, run := range items {
		out = append(out, automationPolicyRunJSON(run))
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": out})
}

func (s *Server) evaluatePolicy(ctx context.Context, clusterID, actorID string, pol appdb.Policy) appdb.PolicyRun {
	run := appdb.PolicyRun{
		ID: uuid.NewString(), ClusterID: clusterID, PolicyID: pol.ID, ActorID: actorID,
		Status: appdb.PolicySkipped, Reason: "no pool exceeded threshold",
	}
	pools, _ := s.Store.ListStoragePools(ctx, clusterID)
	workloads, _ := s.Store.ListWorkloads(ctx, clusterID)
	volumes, _ := s.Store.ListVolumes(ctx, clusterID, "")
	disks, _ := s.Store.ListWorkloadDisks(ctx, clusterID, "")
	placements := map[string]appdb.WorkloadPlacement{}
	for _, wl := range workloads {
		if pl, _ := s.Store.GetWorkloadPlacement(ctx, clusterID, wl.ID); pl != nil {
			placements[wl.ID] = *pl
		}
	}
	var ops []string
	pressured := false
	failed := false
	for _, pool := range pools {
		pct, ok := automation.UsedPercent(pool.UsableBytes, pool.AllocatedBytes)
		if !ok || pct < pol.ThresholdPercent {
			continue
		}
		pressured = true
		cands := automation.SelectLowPriority(pool.ID, workloads, disks, volumes, placements)
		if len(cands) == 0 {
			continue
		}
		c := cands[0]
		op := s.startOp(ctx, clusterID, pool.NodeID, "workload.migrate", "queued", 0)
		op.State = "queued"
		op.Message = fmt.Sprintf("storage pressure %d percent on %s; dest agent is not connected", pct, pool.Name)
		var picked *appdb.Workload
		for i := range workloads {
			if workloads[i].ID == c.WorkloadID {
				picked = &workloads[i]
				break
			}
		}
		if s.Migrate != nil && picked != nil {
			dest, err := s.migrateDest(ctx, clusterID, *picked, "")
			if err == nil && dest != nil && s.destAgentReady(ctx, dest) {
				_, code, msg := s.runMigrate(ctx, *picked, dest, migrateModeFor(*picked))
				if code == http.StatusOK {
					op.State = "succeeded"
					op.Message = fmt.Sprintf("storage pressure %d percent on %s; migrated low-priority %s", pct, pool.Name, c.Name)
				} else {
					op.State = "failed"
					failed = true
					if msg != "" {
						op.Message = msg
					}
				}
			}
		}
		_ = s.Store.UpsertOperation(ctx, op)
		ops = append(ops, op.ID)
	}
	if len(ops) > 0 {
		if failed {
			run.Status = appdb.PolicyFailed
			run.Reason = "migrate operations failed for low-priority workloads"
		} else {
			run.Status = appdb.PolicySucceeded
			run.Reason = "queued migrate operations for low-priority workloads"
		}
		run.OperationIDs = ops
	} else if pressured {
		run.Reason = "pool exceeded threshold but no low-priority VM was selected"
	}
	return run
}

func (s *Server) ensureAutomationActor(ctx context.Context, clusterID string) (string, error) {
	name := "svc-" + automation.ActorName
	u, err := s.Store.GetUserByName(ctx, clusterID, name)
	if err != nil {
		return "", err
	}
	if u == nil {
		row := appdb.User{
			ID: uuid.NewString(), ClusterID: clusterID, Username: name, PasswordHash: "!", Kind: appdb.UserKindService,
		}
		if err := s.Store.CreateUser(ctx, row); err != nil {
			existing, _ := s.Store.GetUserByName(ctx, clusterID, name)
			if existing == nil {
				return "", err
			}
			u = existing
		} else {
			u = &row
			_ = s.Store.CreateServicePrincipal(ctx, appdb.ServicePrincipal{
				ID: uuid.NewString(), ClusterID: clusterID, UserID: row.ID, Name: automation.ActorName,
			})
		}
	}
	if err := s.Store.UnbindRole(ctx, clusterID, u.ID, rbac.Operator); err != nil {
		return "", err
	}
	if err := s.Store.BindRole(ctx, clusterID, u.ID, rbac.Automation); err != nil {
		return "", err
	}
	roles, rerr := s.Store.UserRoles(ctx, u.ID)
	if rerr != nil {
		return "", rerr
	}
	for _, name := range roles {
		if name == rbac.Automation {
			return u.ID, nil
		}
	}
	return "", fmt.Errorf("automation role is not bound")
}

func automationPolicyJSON(p appdb.Policy) map[string]any {
	return map[string]any{
		"id": p.ID, "name": p.Name, "kind": p.Kind, "action": p.Action,
		"threshold_percent": p.ThresholdPercent, "require_approval": p.RequireApproval,
		"enabled": p.Enabled,
	}
}

func automationPolicyRunJSON(r appdb.PolicyRun) map[string]any {
	ids := r.OperationIDs
	if ids == nil {
		ids = []string{}
	}
	return map[string]any{
		"id": r.ID, "policy_id": r.PolicyID, "actor_id": r.ActorID,
		"status": r.Status, "reason": r.Reason, "operation_ids": ids,
		"service_identity": automation.ActorName,
	}
}
