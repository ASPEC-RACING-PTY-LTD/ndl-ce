package appdb

import (
	"encoding/json"
	"strings"
	"time"
)

const (
	OpStateRunning   = "running"
	OpStateSucceeded = "succeeded"
	OpStateFailed    = "failed"
	OpStateCanceled  = "canceled"
	OpStateCanceling = "canceling"

	// OpHeartbeatStale is how long a running task may sit without an update
	// before reconciliation treats it as abandoned.
	OpHeartbeatStale = 20 * time.Minute
	// OpStaleAlertAfter is the operator-visible threshold for stale-task alerts.
	OpStaleAlertAfter = 10 * time.Minute
)

// OpFacts is the live resource/job picture used to close orphaned tasks.
type OpFacts struct {
	Pools     []StoragePool
	Networks  []Network
	Workloads []Workload
	Jobs      []MigrationJob
	Now       time.Time
}

// OpDecision is one idempotent reconciliation result.
type OpDecision struct {
	Operation Operation
	Changed   bool
	Reason    string
}

func terminalOpState(state string) bool {
	switch strings.ToLower(strings.TrimSpace(state)) {
	case OpStateSucceeded, OpStateFailed, OpStateCanceled:
		return true
	default:
		return false
	}
}

func jobTerminalState(state string) string {
	switch strings.ToLower(strings.TrimSpace(state)) {
	case OpStateSucceeded:
		return OpStateSucceeded
	case OpStateFailed:
		return OpStateFailed
	case OpStateCanceled:
		return OpStateCanceled
	default:
		return ""
	}
}

func finishDecision(op Operation, state, message, stage string, progress int, reason string, now time.Time) OpDecision {
	next := op
	next.State = state
	next.Message = message
	if stage != "" {
		next.Stage = stage
	}
	next.Progress = &progress
	next.UpdatedAt = now
	return OpDecision{Operation: next, Changed: true, Reason: reason}
}

func heartbeatDecision(op Operation, stage string, progress int, reason string, now time.Time) OpDecision {
	next := op
	if stage != "" {
		next.Stage = stage
	}
	if progress > 0 {
		next.Progress = &progress
	}
	next.UpdatedAt = now
	return OpDecision{Operation: next, Changed: true, Reason: reason}
}

// ReconcileOperation closes or heartbeats one running task from real state.
// It never blindly fails every running row.
func ReconcileOperation(op Operation, facts OpFacts) OpDecision {
	if terminalOpState(op.State) {
		return OpDecision{Operation: op}
	}
	if !strings.EqualFold(op.State, OpStateRunning) && !strings.EqualFold(op.State, OpStateCanceling) {
		return OpDecision{Operation: op}
	}
	now := facts.Now
	if now.IsZero() {
		now = time.Now().UTC()
	}
	stale := now.Sub(op.UpdatedAt) >= OpHeartbeatStale
	if op.UpdatedAt.IsZero() {
		stale = now.Sub(op.CreatedAt) >= OpHeartbeatStale
	}

	switch {
	case strings.HasPrefix(op.Kind, "migration."):
		return reconcileMigrationOp(op, facts, stale, now)
	case op.Kind == "pool.create":
		if poolExists(facts.Pools) {
			return finishDecision(op, OpStateSucceeded, "directory pool created", "done", 100,
				"pool exists; task row never closed", now)
		}
		if stale {
			return finishDecision(op, OpStateFailed, "pool create did not persist", op.Stage, 0,
				"pool is missing and the task has no heartbeat", now)
		}
	case op.Kind == "network.create":
		if networkExists(facts.Networks) {
			return finishDecision(op, OpStateSucceeded, "network created", "done", 100,
				"network exists; task row never closed", now)
		}
		if stale {
			return finishDecision(op, OpStateFailed, "network create did not persist", op.Stage, 0,
				"network is missing and the task has no heartbeat", now)
		}
	case op.Kind == "workload.delete":
		if len(facts.Workloads) == 0 || !workloadExists(facts.Workloads) {
			return finishDecision(op, OpStateSucceeded, "deleted", "done", 100,
				"workload is gone; delete task row never closed", now)
		}
		if stale {
			return finishDecision(op, OpStateFailed, "workload delete did not finish", op.Stage, 0,
				"workload still exists and the delete task has no heartbeat", now)
		}
	case strings.HasPrefix(op.Kind, "workload."):
		action := strings.TrimPrefix(op.Kind, "workload.")
		if !workloadExists(facts.Workloads) {
			return finishDecision(op, OpStateCanceled, "underlying workload is gone", op.Stage, 0,
				"workload no longer exists", now)
		}
		if action == "start" && workloadPower(facts.Workloads, "running") {
			return finishDecision(op, OpStateSucceeded, "start", "done", 100,
				"workload is running; start task row never closed", now)
		}
		if stale {
			return finishDecision(op, OpStateFailed, action+" did not finish", op.Stage, 0,
				"workload operation has no heartbeat", now)
		}
	default:
		if stale {
			return finishDecision(op, OpStateFailed, "task abandoned without a heartbeat", op.Stage, 0,
				"running task exceeded heartbeat timeout", now)
		}
	}
	return OpDecision{Operation: op}
}

func reconcileMigrationOp(op Operation, facts OpFacts, stale bool, now time.Time) OpDecision {
	job := jobForOperation(facts.Jobs, op.ID)
	if job == nil {
		if stale {
			return finishDecision(op, OpStateFailed, "migration job is missing", op.Stage, 0,
				"no migration job is linked to this task", now)
		}
		return OpDecision{Operation: op}
	}
	if term := jobTerminalState(job.State); term != "" {
		msg := job.Stage
		if term == OpStateSucceeded {
			msg = "Migration verified. Source remains unchanged."
			return finishDecision(op, term, msg, "done", 100,
				"migration job already reached "+term, now)
		}
		if term == OpStateCanceled {
			return finishDecision(op, term, "Migration canceled. Source remains unchanged.", job.Stage, 0,
				"migration job already reached "+term, now)
		}
		return finishDecision(op, term, firstNonEmpty(jobMessage(job), "migration failed"), job.Stage, 0,
			"migration job already reached "+term, now)
	}
	if strings.EqualFold(job.State, OpStateCanceling) && stale {
		return finishDecision(op, OpStateCanceled, "Migration canceled. Source remains unchanged.", "canceled", 0,
			"cancel requested and the worker has no heartbeat", now)
	}
	if strings.EqualFold(job.State, OpStateRunning) && !job.UpdatedAt.IsZero() && now.Sub(job.UpdatedAt) < OpHeartbeatStale {
		progress := 5
		if op.Progress != nil && *op.Progress > progress {
			progress = *op.Progress
		}
		if job.Stage != "" && (job.Stage != op.Stage || now.Sub(op.UpdatedAt) >= time.Minute) {
			return heartbeatDecision(op, job.Stage, progress, "mirror live migration job", now)
		}
		return OpDecision{Operation: op}
	}
	if stale {
		return finishDecision(op, OpStateFailed, "migration worker stopped without a terminal job state", job.Stage, 0,
			"migration job is not terminal and the task has no heartbeat", now)
	}
	return OpDecision{Operation: op}
}

func jobForOperation(jobs []MigrationJob, opID string) *MigrationJob {
	if opID == "" {
		return nil
	}
	for i := range jobs {
		if jobs[i].OperationID == opID {
			j := jobs[i]
			return &j
		}
	}
	return nil
}

func jobMessage(job *MigrationJob) string {
	if job == nil || len(job.StatusJSON) == 0 {
		return ""
	}
	var st struct {
		Message string `json:"message"`
	}
	if json.Unmarshal(job.StatusJSON, &st) != nil {
		return ""
	}
	return st.Message
}

func poolExists(pools []StoragePool) bool {
	return len(pools) > 0
}

func networkExists(nets []Network) bool {
	return len(nets) > 0
}

func workloadExists(items []Workload) bool {
	return len(items) > 0
}

func workloadPower(items []Workload, power string) bool {
	for _, w := range items {
		if strings.EqualFold(w.Status, power) || strings.EqualFold(w.DesiredPower, power) {
			return true
		}
	}
	return false
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

// StaleRunningOps returns running tasks older than the alert threshold.
func StaleRunningOps(ops []Operation, now time.Time, after time.Duration) []Operation {
	if after <= 0 {
		after = OpStaleAlertAfter
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	var out []Operation
	for _, op := range ops {
		if terminalOpState(op.State) {
			continue
		}
		if !strings.EqualFold(op.State, OpStateRunning) && !strings.EqualFold(op.State, OpStateCanceling) {
			continue
		}
		mark := op.UpdatedAt
		if mark.IsZero() {
			mark = op.CreatedAt
		}
		if now.Sub(mark) >= after {
			out = append(out, op)
		}
	}
	return out
}
