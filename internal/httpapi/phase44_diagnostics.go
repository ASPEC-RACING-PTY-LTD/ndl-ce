package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/no-dal/ndl-ce/internal/appdb"
	"github.com/no-dal/ndl-ce/internal/journald"
	"github.com/no-dal/ndl-ce/internal/migration"
	"github.com/no-dal/ndl-ce/internal/rbac"
)

func (s *Server) getMigrationJobDiagnostics(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.MigrationRead)
	if err != nil {
		return
	}
	j, err := s.Store.GetMigrationJob(r.Context(), p.User.ClusterID, r.PathValue("id"))
	if err != nil || j == nil {
		writeErr(w, http.StatusNotFound, "migration job not found")
		return
	}
	writeJSON(w, http.StatusOK, s.migrationDiagnosticsBundle(r.Context(), p.User.ClusterID, *j))
}

func (s *Server) getWorkloadMigrationDiagnostics(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.MigrationRead)
	if err != nil {
		return
	}
	wl, err := s.Store.GetWorkload(r.Context(), p.User.ClusterID, r.PathValue("id"))
	if err != nil || wl == nil {
		writeErr(w, http.StatusNotFound, "workload not found")
		return
	}
	j := s.findMigrationJobForWorkload(r.Context(), p.User.ClusterID, wl.ID, wl.Name)
	if j == nil {
		writeErr(w, http.StatusNotFound, "migration job not found for workload")
		return
	}
	writeJSON(w, http.StatusOK, s.migrationDiagnosticsBundle(r.Context(), p.User.ClusterID, *j))
}

func (s *Server) findMigrationJobForWorkload(ctx context.Context, clusterID, workloadID, name string) *appdb.MigrationJob {
	jobs, err := s.Store.ListMigrationJobs(ctx, clusterID, 100)
	if err != nil {
		return nil
	}
	for i := range jobs {
		if jobMentionsWorkload(jobs[i], workloadID, name) {
			return &jobs[i]
		}
	}
	return nil
}

func jobMentionsWorkload(j appdb.MigrationJob, workloadID, name string) bool {
	if len(j.StatusJSON) > 0 {
		var st migration.JobStatus
		if json.Unmarshal(j.StatusJSON, &st) == nil {
			for _, rep := range st.Reports {
				if workloadID != "" && rep.WorkloadID == workloadID {
					return true
				}
				if name != "" && strings.EqualFold(rep.Name, name) && rep.WorkloadID != "" {
					return true
				}
			}
		}
	}
	if len(j.PlanJSON) == 0 || name == "" {
		return false
	}
	var plan migration.Plan
	if json.Unmarshal(j.PlanJSON, &plan) != nil {
		return false
	}
	for _, item := range plan.Items {
		if strings.EqualFold(item.Name, name) {
			return true
		}
	}
	return false
}

func (s *Server) migrationDiagnosticsBundle(ctx context.Context, clusterID string, j appdb.MigrationJob) map[string]any {
	var plan migration.Plan
	_ = json.Unmarshal(j.PlanJSON, &plan)
	var st migration.JobStatus
	_ = json.Unmarshal(j.StatusJSON, &st)
	bundle := map[string]any{
		"job":           migrationJobJSON(j),
		"plan":          plan,
		"status":        st,
		"stages":        migrationStages(st, plan),
		"logs":          s.migrationDiagnosticLogs(ctx, st),
		"events":        s.migrationDiagnosticEvents(ctx, clusterID, j.ID),
		"mappings":      plan.Mapping,
		"source":        s.migrationDiagnosticSource(ctx, clusterID, plan.SourceID, j.SourceID),
		"destinations":  s.migrationDiagnosticDests(ctx, clusterID, st, plan),
		"health_checks": s.migrationHealthChecks(ctx, clusterID, plan, st.Reports),
		"operation":     s.migrationDiagnosticOperation(ctx, clusterID, j.OperationID),
		"health":        s.platformHealthJSON(ctx),
	}
	return bundle
}

func migrationStages(st migration.JobStatus, plan migration.Plan) []map[string]any {
	out := []map[string]any{{
		"name":    firstNonEmpty(st.Stage, "unknown"),
		"state":   firstNonEmpty(st.State, "unknown"),
		"message": st.Message,
		"workload": st.Workload,
	}}
	seen := map[string]bool{}
	for _, rep := range st.Reports {
		key := firstNonEmpty(rep.SourceID, rep.Name)
		seen[key] = true
		out = append(out, map[string]any{
			"name":        "verified",
			"state":       "succeeded",
			"workload":    rep.Name,
			"workload_id": rep.WorkloadID,
			"source_id":   rep.SourceID,
			"observed":    rep.Observed,
		})
	}
	for _, item := range plan.Items {
		key := firstNonEmpty(item.SourceID, item.Name)
		if seen[key] {
			continue
		}
		state := "pending"
		if st.Workload != "" && strings.EqualFold(st.Workload, item.Name) {
			state = firstNonEmpty(st.State, "running")
		}
		out = append(out, map[string]any{
			"name":      firstNonEmpty(st.Stage, "planned"),
			"state":     state,
			"workload":  item.Name,
			"source_id": item.SourceID,
			"mode":      item.Mode,
		})
	}
	return out
}

func (s *Server) migrationDiagnosticLogs(ctx context.Context, st migration.JobStatus) []map[string]any {
	out := []map[string]any{}
	if st.Message != "" {
		out = append(out, map[string]any{
			"source": "job", "stage": st.Stage, "state": st.State, "message": st.Message,
		})
	}
	if s.Logs == nil {
		return out
	}
	for _, unit := range []string{journald.UnitControl, journald.UnitAgent} {
		res, err := s.Logs.GetLogs(ctx, unit, 200, time.Time{})
		row := map[string]any{"source": "journal", "unit": unit}
		if err != nil {
			row["ok"] = false
			row["detail"] = err.Error()
			out = append(out, row)
			continue
		}
		row["ok"] = res.Status == journald.StatusAvailable
		row["status"] = res.Status
		row["lines"] = res.Lines
		if res.Message != "" {
			row["detail"] = res.Message
		}
		out = append(out, row)
	}
	return out
}

func (s *Server) migrationDiagnosticEvents(ctx context.Context, clusterID, jobID string) []map[string]any {
	events, err := s.Store.ListEvents(ctx, clusterID, 200)
	if err != nil {
		return []map[string]any{}
	}
	out := []map[string]any{}
	for _, ev := range events {
		if !eventMentionsJob(ev, jobID) {
			continue
		}
		row := map[string]any{"id": ev.ID, "type": ev.Type, "payload": json.RawMessage(ev.Payload)}
		if !ev.CreatedAt.IsZero() {
			row["created_at"] = ev.CreatedAt.UTC().Format(time.RFC3339)
		}
		out = append(out, row)
	}
	return out
}

func eventMentionsJob(ev appdb.Event, jobID string) bool {
	return jobID != "" && strings.Contains(string(ev.Payload), jobID)
}

func (s *Server) migrationDiagnosticSource(ctx context.Context, clusterID, planSourceID, jobSourceID string) map[string]any {
	id := firstNonEmpty(planSourceID, jobSourceID)
	if id == "" {
		return map[string]any{"present": false}
	}
	src, token, username, _, err := s.Store.GetMigrationSource(ctx, clusterID, id)
	if err != nil || src == nil {
		return map[string]any{"present": false, "id": id}
	}
	out := s.migrationSourceJSON(*src, token != "" || username != "")
	out["present"] = true
	return out
}

func (s *Server) migrationDiagnosticDests(ctx context.Context, clusterID string, st migration.JobStatus, plan migration.Plan) []map[string]any {
	seen := map[string]bool{}
	out := []map[string]any{}
	add := func(wl *appdb.Workload) {
		if wl == nil || seen[wl.ID] {
			return
		}
		seen[wl.ID] = true
		out = append(out, s.workloadJSON(ctx, *wl))
	}
	for _, rep := range st.Reports {
		add(s.lookupWorkload(ctx, clusterID, rep.WorkloadID, rep.Name))
	}
	for _, item := range plan.Items {
		add(s.lookupWorkload(ctx, clusterID, "", item.Name))
	}
	return out
}

func (s *Server) migrationDiagnosticOperation(ctx context.Context, clusterID, operationID string) map[string]any {
	if operationID == "" {
		return map[string]any{"present": false}
	}
	ops, err := s.Store.ListOperations(ctx, clusterID, 200)
	if err != nil {
		return map[string]any{"present": false, "id": operationID}
	}
	for _, op := range ops {
		if op.ID != operationID {
			continue
		}
		row := map[string]any{
			"present": true, "id": op.ID, "kind": op.Kind, "state": op.State, "stage": op.Stage, "message": op.Message,
		}
		if op.Progress != nil {
			row["progress"] = *op.Progress
		}
		return row
	}
	return map[string]any{"present": false, "id": operationID}
}

func (s *Server) platformHealthJSON(ctx context.Context) map[string]any {
	open, _ := s.setupOpen(ctx)
	return map[string]any{
		"status":      "ok",
		"service":     "ndl-control",
		"setup_open":  open,
		"tls_enabled": s.TLSRequired,
	}
}

func (s *Server) migrationHealthChecks(ctx context.Context, clusterID string, plan migration.Plan, reports []migration.Report) []map[string]any {
	if plan.Direction == "export" {
		return []map[string]any{healthCheck("export_plan", true, "export jobs do not import a destination")}
	}
	req := bodyFromPlan(plan)
	exists, creds := s.probeMigrationSource(ctx, clusterID, req)
	out := []map[string]any{
		healthCheck("source_access", exists && creds, healthDetail(exists && creds, "source is reachable", "source access or credentials failed")),
	}
	if err := migration.CheckDuplicateDests(plan.Items); err != nil {
		out = append(out, healthCheck("duplicate_destination", false, err.Error()))
	} else {
		out = append(out, healthCheck("duplicate_destination", true, "destination ids and names are unique in this plan"))
	}
	discovered := s.discoverForPreflight(ctx, clusterID, req)
	for _, item := range plan.Items {
		prefix := item.Name
		if prefix == "" {
			prefix = item.SourceID
		}
		if done, ok := migration.CompletedReport(reports, item.SourceID, item.Name); ok && s.destinationImportOK(ctx, clusterID, done.WorkloadID, item.Name) {
			out = append(out, healthCheck(prefix+"_already_imported", true, "retry will skip this destination"))
			continue
		}
		if wl := s.lookupWorkload(ctx, clusterID, "", item.Name); wl != nil && migration.DestLooksPartial(wl.Status, wl.ImagePin, wl.ImageVerified) {
			out = append(out, healthCheck(prefix+"_partial_destination", false, "a partial or failed destination already exists"))
		} else {
			out = append(out, healthCheck(prefix+"_partial_destination", true, "no corrupt partial destination"))
		}
		caps := s.capsFor(plan.Adapter, item, discovered)
		env, err := s.preflightEnv(ctx, clusterID, req, item, discovered)
		if err != nil {
			out = append(out, healthCheck(prefix+"_preflight", false, err.Error()))
			continue
		}
		out = append(out, healthCheck(prefix+"_destination_storage", env.DestPoolExists, healthDetail(env.DestPoolExists, "destination storage exists", "destination storage does not exist")))
		out = append(out, healthCheck(prefix+"_free_space", env.DestCapacityOK, healthDetail(env.DestCapacityOK, "destination has capacity", "destination storage has insufficient capacity")))
		out = append(out, healthCheck(prefix+"_destination_network", env.DestNetExists, healthDetail(env.DestNetExists, "destination network exists", "destination network does not exist")))
		out = append(out, healthCheck(prefix+"_archive_supported", !env.ArchiveUnsupported, firstNonEmpty(env.ArchiveReason, "archive type is supported")))
		out = append(out, healthCheck(prefix+"_archive_readable", !env.ArchiveUnreadable, firstNonEmpty(env.ArchiveReason, "backup or archive path is readable")))
		if err := migration.Preflight(item, caps, env); err != nil {
			out = append(out, healthCheck(prefix+"_preflight", false, err.Error()))
		} else {
			out = append(out, healthCheck(prefix+"_preflight", true, "preflight passed"))
		}
	}
	return out
}

func healthCheck(name string, ok bool, detail string) map[string]any {
	row := map[string]any{"name": name, "ok": ok}
	if detail != "" {
		row["detail"] = detail
	}
	return row
}

func healthDetail(ok bool, good, bad string) string {
	if ok {
		return good
	}
	return bad
}
