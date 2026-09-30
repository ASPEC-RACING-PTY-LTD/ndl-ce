package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/no-dal/ndl-ce/internal/appdb"
	"github.com/no-dal/ndl-ce/internal/hostos"
	"github.com/no-dal/ndl-ce/internal/rbac"
)

const (
	applyUpdateConfirm      = "apply-update"
	rollbackUpdateConfirm   = "rollback-update"
	enableRepositoryConfirm = "enable-repository"
	// applyResultGrace is how long a running apply may go unreported by the
	// host before it is recorded as failed.
	applyResultGrace = 2 * time.Hour
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
	if op.Version != "" {
		out["version"] = op.Version
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
	candidates := make([]map[string]string, 0, len(op.Candidates))
	for _, candidate := range op.Candidates {
		allowed := false
		for _, name := range hostos.PackageNames {
			if name == candidate.Name {
				allowed = true
				break
			}
		}
		if allowed && candidate.CandidateVersion != "" {
			candidates = append(candidates, map[string]string{
				"name": candidate.Name, "current_version": candidate.CurrentVersion,
				"candidate_version": candidate.CandidateVersion,
			})
		}
	}
	if len(candidates) > 0 {
		out["candidates"] = candidates
		if url := hostos.ReleaseURL(availableVersion(candidates)); url != "" && op.Action == "check" {
			out["release_url"] = url
		}
	}
	return out
}

// availableVersion is the platform version an update would install. All
// packages ship from one source, so the nodal meta package is preferred and
// any other candidate carries the same version.
func availableVersion(candidates []map[string]string) string {
	version := ""
	for _, c := range candidates {
		if c["candidate_version"] == "" {
			continue
		}
		if c["name"] == "nodal" {
			return c["candidate_version"]
		}
		if version == "" {
			version = c["candidate_version"]
		}
	}
	return version
}

func packageJSON(p hostos.PackageStatus) map[string]any {
	status := p.Status
	if status == "" {
		status = "not_reported"
	}
	return map[string]any{"name": p.Name, "version": p.Version, "status": status}
}

func (s *Server) getUpdates(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.UpdatesManage)
	if err != nil {
		return
	}
	res, err := s.updater().HostUpdate(r.Context(), hostos.UpdateRequest{Action: "status", Channel: hostos.ChannelStable, DryRun: true})
	if err != nil {
		res = hostos.UpdateResult{Supported: false, Reason: "update agent is unavailable", Status: appdb.UpdateUnsupported, Channel: hostos.ChannelStable}
	}
	body := map[string]any{
		"channel":               hostos.ChannelStable,
		"host_supported":        res.Supported,
		"host_reason":           res.Reason,
		"repository_configured": res.RepositoryConfigured,
		"packages":              packageListJSON(res.Packages),
	}
	if last, err := s.Store.GetLatestUpdateOperation(r.Context(), p.User.ClusterID); err == nil && last != nil {
		op := s.settleApply(r.Context(), *last)
		body["last_operation"] = updateOperationJSON(op)
	}
	if lastCheck, err := s.Store.GetLatestCheckUpdateOperation(r.Context(), p.User.ClusterID); err == nil && lastCheck != nil {
		body["last_check"] = updateOperationJSON(*lastCheck)
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
	p, err := s.require(w, r, rbac.UpdatesManage)
	if err != nil {
		return
	}
	res, _, err := s.runUpdateOp(r, p, hostos.UpdateRequest{Action: "check", Channel: hostos.ChannelStable, DryRun: true})
	if err != nil {
		writeUpdateErr(w, err)
		return
	}
	items := make([]map[string]any, 0, len(res.Items))
	var upgrades []map[string]string
	for _, it := range res.Items {
		items = append(items, map[string]any{
			"name": it.Name, "current_version": it.CurrentVersion,
			"candidate_version": it.CandidateVersion, "action": it.Action,
		})
		if it.Action == "upgrade" {
			upgrades = append(upgrades, map[string]string{"name": it.Name, "candidate_version": it.CandidateVersion})
		}
	}
	changelog := res.Changelog
	if changelog == "" {
		changelog = "Changelog is not reported."
	}
	body := map[string]any{
		"channel":   hostos.ChannelStable,
		"items":     items,
		"changelog": changelog,
		"dry_run":   true,
	}
	if version := availableVersion(upgrades); version != "" {
		body["version"] = version
		if url := hostos.ReleaseURL(version); url != "" {
			body["release_url"] = url
		}
	}
	writeJSON(w, http.StatusOK, body)
}

func (s *Server) preflightUpdates(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.UpdatesManage)
	if err != nil {
		return
	}
	res, _, err := s.runUpdateOp(r, p, hostos.UpdateRequest{Action: "preflight", Channel: hostos.ChannelStable, DryRun: true})
	if err != nil {
		writeUpdateErr(w, err)
		return
	}
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
	p, err := s.require(w, r, rbac.UpdatesManage)
	if err != nil {
		return
	}
	id := uuid.NewString()
	res, _, err := s.runUpdateOp(r, p, hostos.UpdateRequest{Action: "checkpoint", Channel: hostos.ChannelStable, CheckpointID: id})
	if err != nil {
		writeUpdateErr(w, err)
		return
	}
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
	p, err := s.require(w, r, rbac.UpdatesManage)
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
		writeUpdateErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, updateOperationJSON(op))
}

func (s *Server) rollbackUpdates(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.UpdatesManage)
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
		writeUpdateErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, updateOperationJSON(op))
}

func (s *Server) enableUpdateRepository(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.UpdatesManage)
	if err != nil {
		return
	}
	if strings.TrimSpace(r.Header.Get(confirmHeader)) != enableRepositoryConfirm {
		writeErr(w, http.StatusUnprocessableEntity, "enabling the release repository requires X-Nodal-Confirm: enable-repository")
		return
	}
	_, op, err := s.runUpdateOp(r, p, hostos.UpdateRequest{Action: hostos.UpdateRepoEnable, Channel: hostos.ChannelStable})
	if err != nil {
		writeUpdateErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, updateOperationJSON(op))
}

// settleApply records the outcome of an apply that ran in the host's
// detached update unit. The control plane restarts during an update, so the
// outcome is read back from the host on the next status read instead of by
// the request that started it.
func (s *Server) settleApply(ctx context.Context, op appdb.UpdateOperation) appdb.UpdateOperation {
	if op.Action != "apply" || op.Status != appdb.UpdateRunning || op.DryRun {
		return op
	}
	s.updateMu.Lock()
	defer s.updateMu.Unlock()
	if cur, err := s.Store.GetLatestUpdateOperation(ctx, op.ClusterID); err != nil || cur == nil || cur.ID != op.ID {
		return op
	} else if cur.Status != appdb.UpdateRunning {
		return *cur
	}
	return s.settleApplyLocked(ctx, op)
}

// settleApplyLocked is settleApply for callers that hold updateMu.
func (s *Server) settleApplyLocked(ctx context.Context, op appdb.UpdateOperation) appdb.UpdateOperation {
	if op.Action != "apply" || op.Status != appdb.UpdateRunning || op.DryRun {
		return op
	}
	res, err := s.updater().HostUpdate(ctx, hostos.UpdateRequest{Action: hostos.UpdateApplyStatus, Channel: hostos.ChannelStable})
	if err != nil {
		// The agent restarts during an update. Try again on the next read.
		return op
	}
	now := s.now()
	switch res.Status {
	case appdb.UpdateSucceeded:
		op.Status = appdb.UpdateSucceeded
		op.Error = ""
	case appdb.UpdateFailed:
		op.Status = appdb.UpdateFailed
		op.Error = applyFailure(res)
	case appdb.UpdateRunning:
		return op
	default:
		if now.Sub(op.StartedAt) < applyResultGrace {
			return op
		}
		op.Status = appdb.UpdateFailed
		op.Error = "The host did not report a result for this update."
	}
	op.FinishedAt = &now
	if res.Version != "" {
		op.Version = res.Version
	}
	if err := s.Store.UpdateUpdateOperation(ctx, op); err != nil {
		log.Printf("settle update apply %s: %v", op.ID, err)
		return op
	}
	s.emitUpdateEvent(ctx, op.ClusterID, "update.apply", map[string]any{"status": op.Status})
	return op
}

func applyFailure(res hostos.UpdateResult) string {
	msg := res.Reason
	if msg == "" {
		msg = "control-plane package apply failed"
	}
	if tail := strings.TrimSpace(res.Log); tail != "" {
		if len(tail) > 1500 {
			tail = tail[len(tail)-1500:]
		}
		msg += "\n" + tail
	}
	return msg
}

func (s *Server) recordedControlVersion(ctx context.Context, clusterID string) string {
	op, err := s.Store.GetLatestCheckUpdateOperation(ctx, clusterID)
	if err != nil || op == nil {
		return ""
	}
	return strings.TrimSpace(op.Version)
}

func (s *Server) runUpdateOp(r *http.Request, p *principal, req hostos.UpdateRequest) (hostos.UpdateResult, appdb.UpdateOperation, error) {
	res, op, err := s.runUpdateOperation(r.Context(), p.User.ClusterID, req)
	if err != nil {
		return res, op, err
	}
	s.audit(r, p.User.ClusterID, p.User.ID, "update."+req.Action, op.Status, op.ID)
	return res, op, nil
}

func (s *Server) TickUpdateCheck(ctx context.Context) bool {
	if !s.updateCheckBusy.CompareAndSwap(false, true) {
		return false
	}
	defer s.updateCheckBusy.Store(false)
	cluster, err := s.Store.GetCluster(ctx)
	if err != nil || cluster == nil || cluster.SetupCompletedAt == nil {
		return false
	}
	_, op, err := s.runUpdateOperation(ctx, cluster.ID, hostos.UpdateRequest{
		Action: "check", Channel: hostos.ChannelStable, DryRun: true,
	})
	if err != nil {
		log.Printf("background update check: %v", err)
		return false
	}
	return op.Status == appdb.UpdateSucceeded
}

// errUpdateRunning refuses new update work while a detached apply runs: the
// package manager holds its lock and the control plane is about to restart.
var errUpdateRunning = errors.New("a platform update is running; wait for it to finish")

func (s *Server) runUpdateOperation(ctx context.Context, clusterID string, req hostos.UpdateRequest) (hostos.UpdateResult, appdb.UpdateOperation, error) {
	s.updateMu.Lock()
	defer s.updateMu.Unlock()
	if last, err := s.Store.GetLatestUpdateOperation(ctx, clusterID); err == nil && last != nil {
		if settled := s.settleApplyLocked(ctx, *last); settled.Action == "apply" && settled.Status == appdb.UpdateRunning && !settled.DryRun {
			return hostos.UpdateResult{}, settled, errUpdateRunning
		}
	}
	var previousCandidates []appdb.UpdateCandidate
	if req.Action == "check" {
		if previous, err := s.Store.GetLatestCheckUpdateOperation(ctx, clusterID); err == nil && previous != nil {
			previousCandidates = append(previousCandidates, previous.Candidates...)
		}
	}
	now := s.now()
	op := appdb.UpdateOperation{
		ID:        uuid.NewString(),
		ClusterID: clusterID,
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
	if err := s.Store.CreateUpdateOperation(ctx, op); err != nil {
		return hostos.UpdateResult{}, op, errInternal("could not record update operation")
	}
	res, err := s.updater().HostUpdate(ctx, req)
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
	} else if res.Status == appdb.UpdateRunning {
		// A detached apply: settleApply records the outcome later.
		op.FinishedAt = nil
		op.Error = ""
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
		for _, item := range res.Items {
			if item.Action == "upgrade" && item.CandidateVersion != "" && item.CandidateVersion != item.CurrentVersion {
				names = append(names, item.Name)
				op.Candidates = append(op.Candidates, appdb.UpdateCandidate{
					Name: item.Name, CurrentVersion: item.CurrentVersion, CandidateVersion: item.CandidateVersion,
				})
			}
		}
		op.Packages = names
	}
	if err := s.Store.UpdateUpdateOperation(ctx, op); err != nil {
		return res, op, errInternal("could not record update operation")
	}
	got, gerr := s.Store.GetLatestUpdateOperation(ctx, clusterID)
	if gerr != nil || got == nil || got.ID != op.ID || got.Status != op.Status {
		return res, op, errInternal("could not record update operation")
	}
	op = *got
	if req.Action == "check" {
		s.emitUpdateEvent(ctx, clusterID, "update.check", map[string]any{"status": op.Status})
		if op.Status == appdb.UpdateSucceeded && !sameUpdateCandidates(previousCandidates, op.Candidates) {
			switch {
			case len(op.Candidates) > 0:
				payload, _ := json.Marshal(map[string]any{"candidates": op.Candidates})
				s.emitUpdatePayload(ctx, clusterID, "update.available", payload)
				s.notify(ctx, clusterID, payload)
			case len(previousCandidates) > 0:
				s.emitUpdateEvent(ctx, clusterID, "update.current", map[string]any{})
			}
		}
	} else {
		s.emitUpdateEvent(ctx, clusterID, "update."+req.Action, map[string]any{"status": op.Status})
	}
	return res, op, nil
}

func sameUpdateCandidates(left, right []appdb.UpdateCandidate) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func (s *Server) emitUpdateEvent(ctx context.Context, clusterID, eventType string, payload any) {
	body, _ := json.Marshal(payload)
	s.emitUpdatePayload(ctx, clusterID, eventType, body)
}

func (s *Server) emitUpdatePayload(ctx context.Context, clusterID, eventType string, payload []byte) {
	event := appdb.Event{
		ID: uuid.NewString(), ClusterID: clusterID, Type: eventType, Payload: payload, CreatedAt: s.now(),
	}
	if err := s.Store.InsertEvent(ctx, event); err != nil {
		log.Printf("update event %s: %v", eventType, err)
		return
	}
	if s.Hub != nil {
		s.Hub.Publish(event)
	}
}

func writeUpdateErr(w http.ResponseWriter, err error) {
	if errors.Is(err, errUpdateRunning) {
		writeErr(w, http.StatusConflict, err.Error())
		return
	}
	writeErr(w, http.StatusInternalServerError, err.Error())
}
