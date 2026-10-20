package httpapi

import (
	"context"
	"fmt"
	"io/fs"
	"log"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/no-dal/ndl-ce/internal/appdb"
	"github.com/no-dal/ndl-ce/internal/hostos"
	"github.com/no-dal/ndl-ce/migrations"
)

// An apply or rollback ("host change") runs in the background: preflight,
// pausing backups and the checkpoint can take longer than a proxied request
// may stay open. The operation row is created first and reported as running;
// the UI polls GET /updates, which shows the current stage.

// statusCacheTTL bounds how often GET /updates asks the host for installed
// package versions.
const statusCacheTTL = time.Minute

type updateStage struct {
	mu     sync.Mutex
	stage  string
	paused int
	wg     sync.WaitGroup
}

type updateStatusCache struct {
	mu  sync.Mutex
	at  time.Time
	res hostos.UpdateResult
}

// rollbackPlan is what a rollback moves back to.
type rollbackPlan struct {
	ApplyID      string
	Version      string
	CheckpointID string
	RestoresDB   bool
	// Candidates are the package moves, recorded on the rollback so that
	// undoing it knows which versions to put back.
	Candidates []appdb.UpdateCandidate
	// WindowEnds is when the checkpoint is wiped and rollback ends.
	WindowEnds time.Time
}

// actionUndoRollback puts back what a rollback replaced, from the checkpoint
// the rollback took first.
const actionUndoRollback = "undo-rollback"

func hostChange(action string) bool {
	return action == "apply" || action == "rollback" || action == actionUndoRollback
}

func (s *Server) setStage(stage string) {
	s.updateStage.mu.Lock()
	s.updateStage.stage = stage
	s.updateStage.mu.Unlock()
}

func (s *Server) stage() (string, int) {
	s.updateStage.mu.Lock()
	defer s.updateStage.mu.Unlock()
	return s.updateStage.stage, s.updateStage.paused
}

// waitHostChange blocks until a background apply or rollback has handed
// over to the host (tests).
func (s *Server) waitHostChange() {
	s.updateStage.wg.Wait()
}

// cachedStatus returns installed package versions, asking the host at most
// once per statusCacheTTL.
func (s *Server) cachedStatus(ctx context.Context) (hostos.UpdateResult, error) {
	s.updateStatus.mu.Lock()
	defer s.updateStatus.mu.Unlock()
	if !s.updateStatus.at.IsZero() && s.now().Sub(s.updateStatus.at) < statusCacheTTL {
		return s.updateStatus.res, nil
	}
	res, err := s.updater().HostUpdate(ctx, hostos.UpdateRequest{Action: "status", Channel: hostos.ChannelStable, DryRun: true})
	if err != nil {
		return res, err
	}
	s.updateStatus.res, s.updateStatus.at = res, s.now()
	return res, nil
}

func (s *Server) dropStatusCache() {
	s.updateStatus.mu.Lock()
	s.updateStatus.at = time.Time{}
	s.updateStatus.mu.Unlock()
}

// latestSchemaVersion is the newest migration this control plane ships.
func latestSchemaVersion() string {
	names, err := fs.Glob(migrations.FS, "*.sql")
	if err != nil || len(names) == 0 {
		return ""
	}
	sort.Strings(names)
	return names[len(names)-1]
}

// updateBlockers names work an update cannot pause and must not interrupt.
// Backups are not blockers: they are paused and resumed.
func (s *Server) updateBlockers(ctx context.Context, clusterID string) string {
	ops, err := s.Store.ListOperations(ctx, clusterID, 200)
	if err != nil {
		return ""
	}
	now := s.now()
	var names []string
	for _, op := range ops {
		if !strings.EqualFold(op.State, appdb.OpStateRunning) || now.Sub(op.UpdatedAt) > appdb.OpHeartbeatStale {
			continue
		}
		if strings.HasPrefix(op.Kind, "migration.") {
			names = append(names, op.Kind+" ("+firstNonEmpty(op.Stage, "running")+")")
		}
	}
	if len(names) == 0 {
		return ""
	}
	return "A workload import or export is running: " + strings.Join(names, ", ") + ". Cancel it or let it finish, then update."
}

// controlChecks are the preflight rows only the control plane can judge.
func (s *Server) controlChecks(ctx context.Context, clusterID string) []hostos.PreflightCheck {
	var out []hostos.PreflightCheck
	if msg := s.updateBlockers(ctx, clusterID); msg != "" {
		out = append(out, hostos.PreflightCheck{Name: "running_work", Status: "failed", Detail: msg})
	} else {
		out = append(out, hostos.PreflightCheck{Name: "running_work", Status: "ok", Detail: "No import or export is running."})
	}
	s.hold.mu.Lock()
	running := len(s.hold.cancels)
	s.hold.mu.Unlock()
	if running > 0 {
		out = append(out, hostos.PreflightCheck{Name: "backups", Status: "warning",
			Detail: fmt.Sprintf("%d backup(s) running. They are paused for the update and run again automatically afterwards.", running)})
	} else {
		out = append(out, hostos.PreflightCheck{Name: "backups", Status: "ok", Detail: "No backup is running. Scheduled backups wait until the update is done."})
	}
	return out
}

// startHostChange records an apply or rollback and runs it in the
// background. It refuses while another one is preparing or running.
func (s *Server) startHostChange(ctx context.Context, clusterID, action string, plan *rollbackPlan) (appdb.UpdateOperation, error) {
	s.updateMu.Lock()
	defer s.updateMu.Unlock()
	if s.applyPreparing.Load() {
		return appdb.UpdateOperation{}, errUpdateRunning
	}
	if last, err := s.Store.GetLatestUpdateOperation(ctx, clusterID); err == nil && last != nil {
		if settled := s.settleApplyLocked(ctx, *last); hostChange(settled.Action) && settled.Status == appdb.UpdateRunning && !settled.DryRun {
			return settled, errUpdateRunning
		}
	}
	op := appdb.UpdateOperation{
		ID: uuid.NewString(), ClusterID: clusterID, Action: action, Status: appdb.UpdateRunning,
		StartedAt: s.now(), Packages: append([]string{}, hostos.PackageNames...),
	}
	if plan != nil {
		op.Version = plan.Version
		op.Candidates = plan.Candidates
		op.RestoredDB = plan.RestoresDB
		op.SchemaVersion = latestSchemaVersion()
	}
	if err := s.Store.CreateUpdateOperation(ctx, op); err != nil {
		return op, errInternal("could not record update operation")
	}
	s.applyPreparing.Store(true)
	s.setStage("Checking the host")
	s.updateStage.wg.Add(1)
	go s.runHostChange(context.WithoutCancel(ctx), op, plan)
	return op, nil
}

func (s *Server) runHostChange(ctx context.Context, op appdb.UpdateOperation, plan *rollbackPlan) {
	defer s.updateStage.wg.Done()
	defer func() {
		s.applyPreparing.Store(false)
		s.setStage("")
		s.dropStatusCache()
	}()
	done := func(status, msg string) {
		now := s.now()
		op.Status, op.Error, op.FinishedAt = status, msg, &now
		s.persistHostChange(ctx, op)
		if status != appdb.UpdateRunning {
			s.resumeBackups()
		}
		s.emitUpdateEvent(ctx, op.ClusterID, "update."+op.Action, map[string]any{"status": op.Status})
	}
	fail := func(msg string) { done(appdb.UpdateFailed, msg) }
	req := hostos.UpdateRequest{Action: op.Action, Channel: hostos.ChannelStable}
	if op.Action == actionUndoRollback {
		// The host moves packages the same way for both directions.
		req.Action = "rollback"
	}
	if op.Action == "apply" {
		pre, err := s.updater().HostUpdate(ctx, hostos.UpdateRequest{Action: "preflight", Channel: hostos.ChannelStable, DryRun: true})
		switch {
		case err != nil:
			fail("update agent is unavailable")
			return
		case !pre.Supported:
			done(appdb.UpdateUnsupported, pre.Reason)
			return
		case !pre.PreflightOK:
			fail("Preflight failed, so nothing was changed: " + firstNonEmpty(pre.Reason, "a host check failed"))
			return
		case pre.CandidateVersion == "":
			fail("This host already runs the newest version, so nothing was changed.")
			return
		}
		req.Version = pre.CandidateVersion
		op.Version = pre.CandidateVersion
		op.SchemaVersion = latestSchemaVersion()
		for _, it := range pre.Items {
			if it.CurrentVersion != "" {
				op.Candidates = append(op.Candidates, appdb.UpdateCandidate{
					Name: it.Name, CurrentVersion: it.CurrentVersion, CandidateVersion: it.CandidateVersion,
				})
			}
		}
	} else if plan != nil {
		req.Version = plan.Version
		if plan.RestoresDB {
			req.CheckpointID = plan.CheckpointID
		}
	}
	if msg := s.updateBlockers(ctx, op.ClusterID); msg != "" {
		fail(msg)
		return
	}
	s.setStage("Pausing backups")
	paused := s.suspendBackups("a platform "+op.Action+" is in progress", backupDrainWait)
	s.updateStage.mu.Lock()
	s.updateStage.paused = paused
	s.updateStage.mu.Unlock()
	// Every change is preceded by a checkpoint (packages and database), so
	// an update can be rolled back and a rollback can be undone.
	s.setStage("Saving a checkpoint")
	id := uuid.NewString()
	cp, err := s.updater().HostUpdate(ctx, hostos.UpdateRequest{Action: "checkpoint", Channel: hostos.ChannelStable, CheckpointID: id})
	if err != nil || cp.Status != appdb.UpdateSucceeded {
		reason := "update agent is unavailable"
		if err == nil {
			reason = firstNonEmpty(cp.Reason, "checkpoint failed")
		}
		fail("The checkpoint could not be saved, so nothing was changed: " + reason)
		return
	}
	op.CheckpointID = id
	s.persistHostChange(ctx, op)
	if op.Action == "apply" {
		s.setStage("Installing the update")
	} else {
		s.setStage("Rolling back")
	}
	res, err := s.updater().HostUpdate(ctx, req)
	switch {
	case err != nil:
		fail("update agent is unavailable")
	case !res.Supported:
		done(appdb.UpdateUnsupported, res.Reason)
	case res.Status == appdb.UpdateFailed:
		fail(applyFailure(res))
	case res.Status == appdb.UpdateRunning:
		// Detached on the host: settleApply records the outcome. Backups
		// stay paused until then.
		op.Error = ""
		s.persistHostChange(ctx, op)
		s.emitUpdateEvent(ctx, op.ClusterID, "update."+op.Action, map[string]any{"status": op.Status})
	default:
		done(appdb.UpdateSucceeded, "")
	}
}

func (s *Server) persistHostChange(ctx context.Context, op appdb.UpdateOperation) {
	if err := s.Store.UpdateUpdateOperation(ctx, op); err != nil {
		log.Printf("update %s %s: %v", op.Action, op.ID, err)
	}
}

// planRollback finds what the last apply replaced. It refuses honestly when
// nothing usable was recorded.
func (s *Server) planRollback(ctx context.Context, clusterID string) (rollbackPlan, string) {
	last, err := s.Store.GetLatestUpdateOperationByAction(ctx, clusterID, "apply")
	if err != nil || last == nil || last.DryRun {
		return rollbackPlan{}, "No update has been applied here, so there is nothing to roll back to."
	}
	if last.Status == appdb.UpdateRunning {
		return rollbackPlan{}, "The last update is still running."
	}
	if rb, err := s.Store.GetLatestUpdateOperationByAction(ctx, clusterID, "rollback"); err == nil && rb != nil &&
		rb.StartedAt.After(last.StartedAt) && rb.Status == appdb.UpdateSucceeded && !s.rollbackUndone(ctx, clusterID, *rb) {
		return rollbackPlan{}, "The last update was already rolled back."
	}
	if last.CheckpointID == "" {
		return rollbackPlan{}, "The last update stopped before it installed anything, so there is nothing to roll back."
	}
	if at, _ := s.Store.CheckpointWiped(ctx, last.ID); at != nil {
		return rollbackPlan{}, "The previous version was wiped on " + at.UTC().Format("2 Jan 2006 15:04 UTC") + ", so the last update can no longer be rolled back."
	}
	ends := rollbackEnds(*last)
	if !s.now().Before(ends) {
		return rollbackPlan{}, "The 3-day rollback window closed on " + ends.UTC().Format("2 Jan 2006 15:04 UTC") + "."
	}
	version := ""
	for _, c := range last.Candidates {
		if c.CurrentVersion == "" {
			continue
		}
		if c.Name == "nodal" {
			version = c.CurrentVersion
			break
		}
		if version == "" {
			version = c.CurrentVersion
		}
	}
	if version == "" {
		return rollbackPlan{}, "The versions installed before the last update were not recorded, so it cannot be rolled back."
	}
	plan := rollbackPlan{ApplyID: last.ID, Version: version, CheckpointID: last.CheckpointID, WindowEnds: ends}
	for _, c := range last.Candidates {
		plan.Candidates = append(plan.Candidates, appdb.UpdateCandidate{Name: c.Name, CurrentVersion: c.CandidateVersion, CandidateVersion: c.CurrentVersion})
	}
	// The database is only restored when the update changed its schema:
	// otherwise everything done since the update is kept.
	plan.RestoresDB = last.SchemaVersion == "" || last.SchemaVersion != latestSchemaVersion()
	return plan, ""
}

// rollbackUndone reports whether an undo finished after the rollback rb.
func (s *Server) rollbackUndone(ctx context.Context, clusterID string, rb appdb.UpdateOperation) bool {
	u, err := s.Store.GetLatestUpdateOperationByAction(ctx, clusterID, actionUndoRollback)
	return err == nil && u != nil && u.StartedAt.After(rb.StartedAt) && u.Status == appdb.UpdateSucceeded
}

// planUndoRollback finds what the last rollback replaced, from the
// checkpoint it took before it ran. It has the same 3-day window.
func (s *Server) planUndoRollback(ctx context.Context, clusterID string) (rollbackPlan, string) {
	rb, err := s.Store.GetLatestUpdateOperationByAction(ctx, clusterID, "rollback")
	if err != nil || rb == nil || rb.DryRun {
		return rollbackPlan{}, "Nothing has been rolled back, so there is nothing to undo."
	}
	if rb.Status == appdb.UpdateRunning {
		return rollbackPlan{}, "The rollback is still running."
	}
	if rb.Status != appdb.UpdateSucceeded {
		return rollbackPlan{}, "The last rollback did not finish, so there is nothing to undo."
	}
	if apply, err := s.Store.GetLatestUpdateOperationByAction(ctx, clusterID, "apply"); err == nil && apply != nil && apply.StartedAt.After(rb.StartedAt) {
		return rollbackPlan{}, "An update was installed after the rollback, so it can no longer be undone."
	}
	if s.rollbackUndone(ctx, clusterID, *rb) {
		return rollbackPlan{}, "The last rollback was already undone."
	}
	if rb.CheckpointID == "" {
		return rollbackPlan{}, "No checkpoint was saved before the last rollback, so it cannot be undone."
	}
	if at, _ := s.Store.CheckpointWiped(ctx, rb.ID); at != nil {
		return rollbackPlan{}, "The copy saved before the rollback was wiped on " + at.UTC().Format("2 Jan 2006 15:04 UTC") + ", so the rollback can no longer be undone."
	}
	ends := rollbackEnds(*rb)
	if !s.now().Before(ends) {
		return rollbackPlan{}, "The 3-day window to undo the rollback closed on " + ends.UTC().Format("2 Jan 2006 15:04 UTC") + "."
	}
	version := ""
	for _, c := range rb.Candidates {
		if c.CurrentVersion != "" && (c.Name == "nodal" || version == "") {
			version = c.CurrentVersion
		}
	}
	if version == "" {
		return rollbackPlan{}, "The versions installed before the rollback were not recorded, so it cannot be undone."
	}
	plan := rollbackPlan{ApplyID: rb.ID, Version: version, CheckpointID: rb.CheckpointID, WindowEnds: ends, RestoresDB: rb.RestoredDB}
	for _, c := range rb.Candidates {
		plan.Candidates = append(plan.Candidates, appdb.UpdateCandidate{Name: c.Name, CurrentVersion: c.CandidateVersion, CandidateVersion: c.CurrentVersion})
	}
	return plan, ""
}

func rollbackJSON(plan rollbackPlan, reason string) map[string]any {
	if reason != "" {
		return map[string]any{"available": false, "reason": reason}
	}
	return map[string]any{
		"available": true, "version": plan.Version, "restores_database": plan.RestoresDB,
		"window_ends_at": plan.WindowEnds.UTC().Format(time.RFC3339),
	}
}
