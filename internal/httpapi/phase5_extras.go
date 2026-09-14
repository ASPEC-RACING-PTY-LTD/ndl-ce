package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/no-dal/ndl-ce/internal/appdb"
	"github.com/no-dal/ndl-ce/internal/guestextras"
	"github.com/no-dal/ndl-ce/internal/lxc"
	"github.com/no-dal/ndl-ce/internal/rbac"
)

func (s *Server) listWorkloadExtras(w http.ResponseWriter, r *http.Request) {
	if _, err := s.require(w, r, rbac.ComputeRead); err != nil {
		return
	}
	pin := strings.TrimSpace(r.URL.Query().Get("image_pin"))
	writeJSON(w, http.StatusOK, map[string]any{"items": guestextras.Describe(pin)})
}

func (s *Server) setupWorkloadExtras(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.ComputeModify)
	if err != nil {
		return
	}
	row, err := s.Store.GetWorkload(r.Context(), p.User.ClusterID, r.PathValue("id"))
	if err != nil || row == nil {
		writeErr(w, http.StatusNotFound, "workload not found")
		return
	}
	if row.Kind != lxc.KindSystemContainer {
		writeErr(w, http.StatusUnprocessableEntity, "extras are only supported on system containers")
		return
	}
	var req struct {
		Extras []string `json:"extras"`
	}
	if err := readJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request")
		return
	}
	warnings := s.applyWorkloadExtras(r.Context(), p.User.ClusterID, row.NodeID, *row, req.Extras, row.DesiredPower)
	s.audit(r, p.User.ClusterID, p.User.ID, "workload.setup-extras", extrasAuditResult(warnings), row.ID)
	writeJSON(w, http.StatusOK, s.workloadJSONWithSetup(r.Context(), *row, warnings))
}

func extrasAuditResult(warnings []lxc.SetupWarning) string {
	if len(warnings) == 0 {
		return "ok"
	}
	return "warning"
}

func (s *Server) applyWorkloadExtras(ctx context.Context, clusterID, nodeID string, row appdb.Workload, selected []string, desiredPower string) []lxc.SetupWarning {
	extras := guestextras.Resolve(selected)
	if len(extras) == 0 {
		return nil
	}
	if s.Workloads == nil {
		return []lxc.SetupWarning{{Extra: "setup", Message: "workload agent is unavailable"}}
	}
	msg, _ := json.Marshal(map[string]any{"workload_id": row.ID, "name": row.Name, "extras": extras})
	op := s.startOpKeyed(ctx, clusterID, nodeID, "workload.setup-extras", "installing", "", string(msg), 10)
	startedForSetup := false
	if desiredPower == "stopped" {
		if _, err := s.Workloads.LifecycleCT(ctx, lxc.LifecycleRequest{WorkloadID: row.ID, Action: "start"}); err == nil {
			startedForSetup = true
		}
	}
	res, err := s.Workloads.LifecycleCT(ctx, lxc.LifecycleRequest{
		WorkloadID: row.ID,
		Action:     lxc.ActionGuestSetup,
		Extras:     extras,
	})
	if startedForSetup {
		_, _ = s.Workloads.LifecycleCT(ctx, lxc.LifecycleRequest{WorkloadID: row.ID, Action: "stop"})
	}
	warnings := append([]lxc.SetupWarning{}, res.SetupWarnings...)
	if err != nil {
		warnings = append(warnings, lxc.SetupWarning{Extra: "setup", Message: sanitizeGuestSetupErr(err)})
	}
	if len(warnings) > 0 {
		s.finishOp(ctx, op, "failed", extrasWarningMessage(row.Name, warnings), 100)
		return warnings
	}
	s.finishOp(ctx, op, "succeeded", extrasOKMessage(row.Name, extras), 100)
	return nil
}

func extrasWarningMessage(name string, warnings []lxc.SetupWarning) string {
	ids := make([]string, 0, len(warnings))
	for _, w := range warnings {
		if w.Extra != "" {
			ids = append(ids, w.Extra)
		}
	}
	body, _ := json.Marshal(map[string]any{"name": name, "error": "optional extras failed", "extras": ids})
	return string(body)
}

func extrasOKMessage(name string, extras []string) string {
	body, _ := json.Marshal(map[string]any{"name": name, "extras": extras})
	return string(body)
}

func sanitizeGuestSetupErr(err error) string {
	if err == nil {
		return ""
	}
	msg := strings.TrimSpace(err.Error())
	msg = strings.ReplaceAll(msg, "\n", " ")
	if len(msg) > 240 {
		msg = msg[:240]
	}
	return msg
}

func (s *Server) workloadJSONWithSetup(ctx context.Context, w appdb.Workload, warnings []lxc.SetupWarning) map[string]any {
	out := s.workloadJSON(ctx, w)
	if len(warnings) == 0 {
		return out
	}
	out["setup_warnings"] = warnings
	out["setup_status"] = "warnings"
	return out
}
