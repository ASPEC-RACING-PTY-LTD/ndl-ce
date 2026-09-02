package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/no-dal/ndl-ce/internal/appdb"
	"github.com/no-dal/ndl-ce/internal/hostos"
	"github.com/no-dal/ndl-ce/internal/rbac"
)

const (
	applyUpdateConfirm    = "apply-update"
	rollbackUpdateConfirm = "rollback-update"
)

// UpdateRPC is the privileged agent surface for host-platform package updates.
type UpdateRPC interface {
	HostUpdate(ctx context.Context, req hostos.UpdateRequest) (hostos.UpdateResult, error)
}

type updateUnavailable struct{}

func (updateUnavailable) HostUpdate(context.Context, hostos.UpdateRequest) (hostos.UpdateResult, error) {
	return hostos.UpdateResult{
		Supported: false,
		Reason:    "update agent is unavailable",
		Status:    appdb.UpdateUnsupported,
		Channel:   hostos.ChannelStable,
		Packages:  hostos.EvaluateUpdate(hostos.Platform{}, hostos.UpdateRequest{}).Packages,
	}, nil
}

func AdaptUpdate(client any) UpdateRPC {
	if v, ok := client.(UpdateRPC); ok {
		return v
	}
	return updateUnavailable{}
}

func (s *Server) updater() UpdateRPC {
	if s.Update != nil {
		return s.Update
	}
	return updateUnavailable{}
}

func updateOperationJSON(op appdb.UpdateOperation) map[string]any {
	out := map[string]any{
		"id":         op.ID,
		"action":     op.Action,
		"status":     op.Status,
		"dry_run":    op.DryRun,
		"started_at": op.StartedAt.UTC().Format("2006-01-02T15:04:05Z"),
	}
	if op.Error != "" {
		out["error"] = op.Error
	}
	if op.FinishedAt != nil {
		out["finished_at"] = op.FinishedAt.UTC().Format("2006-01-02T15:04:05Z")
	}
	pkgs := make([]string, 0, len(op.Packages))
	for _, name := range op.Packages {
		allowed := false
		for _, n := range hostos.PackageNames {
			if n == name {
				allowed = true
				break
			}
		}
		if allowed {
			pkgs = append(pkgs, name)
		}
	}
	if len(pkgs) > 0 {
		out["packages"] = pkgs
	}
	return out
}

func packageJSON(p hostos.PackageStatus) map[string]any {
	status := p.Status
	if status == "" {
		status = "not_reported"
	}
	return map[string]any{"name": p.Name, "version": p.Version, "status": status}
}

func (s *Server) getUpdates(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.NodeRead)
	if err != nil {
		return
	}
	res, err := s.updater().HostUpdate(r.Context(), hostos.UpdateRequest{Action: "status", Channel: hostos.ChannelStable, DryRun: true})
	if err != nil {
		res = hostos.UpdateResult{Supported: false, Reason: "update agent is unavailable", Status: appdb.UpdateUnsupported, Channel: hostos.ChannelStable}
	}
	body := map[string]any{
		"channel":        hostos.ChannelStable,
		"host_supported": res.Supported,
		"host_reason":    res.Reason,
		"packages":       packageListJSON(res.Packages),
	}
	if last, err := s.Store.GetLatestUpdateOperation(r.Context(), p.User.ClusterID); err == nil && last != nil {
		body["last_operation"] = updateOperationJSON(*last)
	}
	writeJSON(w, http.StatusOK, body)
}

func packageListJSON(pkgs []hostos.PackageStatus) []map[string]any {
	if len(pkgs) == 0 {
		out := make([]map[string]any, 0, len(hostos.PackageNames))
		for _, name := range hostos.PackageNames {
			out = append(out, map[string]any{"name": name, "version": "", "status": "not_reported"})
		}
		return out
	}
	out := make([]map[string]any, 0, len(pkgs))
	for _, p := range pkgs {
		out = append(out, packageJSON(p))
	}
	return out
}

func (s *Server) checkUpdates(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.NodeUpdate)
	if err != nil {
		return
	}
	res, _, _ := s.runUpdateOp(r, p, hostos.UpdateRequest{Action: "check", Channel: hostos.ChannelStable, DryRun: true})
	items := make([]map[string]any, 0, len(res.Items))
	for _, it := range res.Items {
		items = append(items, map[string]any{
			"name": it.Name, "current_version": it.CurrentVersion,
			"candidate_version": it.CandidateVersion, "action": it.Action,
		})
	}
	changelog := res.Changelog
	if changelog == "" {
		changelog = "Changelog is not reported."
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"channel":   hostos.ChannelStable,
		"items":     items,
		"changelog": changelog,
		"dry_run":   true,
	})
}

func (s *Server) preflightUpdates(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.NodeUpdate)
	if err != nil {
		return
	}
	res, _, _ := s.runUpdateOp(r, p, hostos.UpdateRequest{Action: "preflight", Channel: hostos.ChannelStable, DryRun: true})
	checks := make([]map[string]any, 0, len(res.Checks))
	for _, c := range res.Checks {
		name, status, detail := c.Name, c.Status, c.Detail
		if name == "store_compatibility" {
			if status == "ok" || status == "" {
				status = "skipped"
			}
			if detail == "" {
				detail = hostos.StoreCompatDetail
			}
			if (status == "skipped" || status == "unavailable") && !strings.Contains(strings.ToLower(detail), "not implemented") {
				detail = "Store compatibility check is not implemented. " + detail
			}
		}
		checks = append(checks, map[string]any{"name": name, "status": status, "detail": detail})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":        res.PreflightOK && res.Supported,
		"checks":    checks,
		"kernel_ok": res.KernelOK,
		"zfs_ok":    res.ZFSOK,
		"nvidia_ok": res.NvidiaOK,
	})
}

func (s *Server) checkpointUpdates(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.NodeUpdate)
	if err != nil {
		return
	}
	id := uuid.NewString()
	res, _, _ := s.runUpdateOp(r, p, hostos.UpdateRequest{Action: "checkpoint", Channel: hostos.ChannelStable, CheckpointID: id})
	status := res.Status
	if !res.Supported {
		status = appdb.UpdateUnsupported
	}
	if status == "" {
		status = appdb.UpdateFailed
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"id":            id,
		"locator":       res.Locator,
		"postgres_dump": res.PostgresDump,
		"status":        status,
	})
}

func (s *Server) applyUpdates(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.NodeUpdate)
	if err != nil {
		return
	}
	if strings.TrimSpace(r.Header.Get(confirmHeader)) != applyUpdateConfirm {
		writeErr(w, http.StatusUnprocessableEntity, "apply requires X-Nodal-Confirm: apply-update")
		return
	}
	version := s.recordedControlVersion(r.Context(), p.User.ClusterID)
	_, op, err := s.runUpdateOp(r, p, hostos.UpdateRequest{Action: "apply", Channel: hostos.ChannelStable, Version: version, DryRun: false})
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, updateOperationJSON(op))
}

func (s *Server) rollbackUpdates(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.NodeUpdate)
	if err != nil {
		return
	}
	if strings.TrimSpace(r.Header.Get(confirmHeader)) != rollbackUpdateConfirm {
		writeErr(w, http.StatusUnprocessableEntity, "rollback requires X-Nodal-Confirm: rollback-update")
		return
	}
	version := s.recordedControlVersion(r.Context(), p.User.ClusterID)
	_, op, err := s.runUpdateOp(r, p, hostos.UpdateRequest{Action: "rollback", Channel: hostos.ChannelStable, Version: version, DryRun: false})
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, updateOperationJSON(op))
}

func (s *Server) recordedControlVersion(ctx context.Context, clusterID string) string {
	ops, err := s.Store.ListUpdateOperations(ctx, clusterID, 20)
	if err != nil {
		return ""
	}
	for _, op := range ops {
		if op.Action == "check" && op.Version != "" {
			return op.Version
		}
	}
	return ""
}

func (s *Server) runUpdateOp(r *http.Request, p *principal, req hostos.UpdateRequest) (hostos.UpdateResult, appdb.UpdateOperation, error) {
	now := s.now()
	op := appdb.UpdateOperation{
		ID:        uuid.NewString(),
		ClusterID: p.User.ClusterID,
		Action:    req.Action,
		Status:    appdb.UpdateRunning,
		DryRun:    req.DryRun,
		StartedAt: now,
		Packages:  append([]string{}, hostos.PackageNames...),
	}
	if req.Action == "apply" {
		op.Packages = []string{"nodal"}
	}
	if req.Action == "rollback" {
		op.Packages = []string{"ndl-control"}
		op.Version = req.Version
	}
	_ = s.Store.CreateUpdateOperation(r.Context(), op)
	res, err := s.updater().HostUpdate(r.Context(), req)
	finished := s.now()
	op.FinishedAt = &finished
	if err != nil {
		op.Status = appdb.UpdateFailed
		op.Error = "update agent is unavailable"
	} else if !res.Supported {
		op.Status = appdb.UpdateUnsupported
		op.Error = res.Reason
	} else if res.Status == appdb.UpdateFailed {
		op.Status = appdb.UpdateFailed
		op.Error = res.Reason
	} else {
		op.Status = appdb.UpdateSucceeded
		op.Error = ""
	}
	if res.Version != "" {
		op.Version = res.Version
	}
	if req.Action == "check" {
		op.DryRun = true
		names := make([]string, 0, len(res.Packages))
		for _, pkg := range res.Packages {
			names = append(names, pkg.Name)
		}
		if len(names) > 0 {
			op.Packages = names
		}
	}
	if err := s.Store.UpdateUpdateOperation(r.Context(), op); err != nil {
		return res, op, errInternal("could not record update operation")
	}
	s.audit(r, p.User.ClusterID, p.User.ID, "update."+req.Action, op.Status, op.ID)
	payload, _ := json.Marshal(map[string]string{"status": op.Status})
	_ = s.Store.InsertEvent(r.Context(), appdb.Event{
		ID: uuid.NewString(), ClusterID: p.User.ClusterID, Type: "update." + req.Action,
		Payload: payload, CreatedAt: time.Now().UTC(),
	})
	return res, op, nil
}
