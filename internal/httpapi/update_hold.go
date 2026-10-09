package httpapi

import (
	"context"
	"errors"
	"log"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/no-dal/ndl-ce/internal/appdb"
)

// A platform update restarts the agent, which would kill any backup in
// flight. Instead of waiting for backups, the update pauses them: running
// captures are cancelled at once (their partial data is discarded by the
// repository's crash recovery and reclaimed by GC), queued ones never start,
// and every paused backup runs again automatically once the update is over.
// Uploads already captured stay in the agent's persistent queue and carry on
// after the restart, so no finished backup is lost.

const (
	backupSuspendedError = "paused for a platform update; it runs again automatically when the update finishes"
	backupResumedError   = "paused for a platform update and run again afterwards"
	// backupDrainWait bounds how long an update waits for cancelled
	// backups to release the agent.
	backupDrainWait = 90 * time.Second
	// resumeWindow is how old a paused backup may be and still be resumed.
	resumeWindow = 24 * time.Hour
)

var errBackupSuspended = errConflict("backups are paused for a platform update and run again when it finishes")

type backupHold struct {
	mu      sync.Mutex
	reason  string
	cancels map[string]context.CancelFunc
}

// trackBackup registers a backup so an update can cancel it. It refuses
// while backups are paused.
func (s *Server) trackBackup(ctx context.Context, workloadID string) (context.Context, func(), error) {
	s.hold.mu.Lock()
	defer s.hold.mu.Unlock()
	if s.hold.reason != "" {
		return ctx, func() {}, errBackupSuspended
	}
	if s.hold.cancels == nil {
		s.hold.cancels = map[string]context.CancelFunc{}
	}
	cctx, cancel := context.WithCancel(ctx)
	key := workloadID + "\x00" + time.Now().Format(time.RFC3339Nano)
	s.hold.cancels[key] = cancel
	return cctx, func() {
		s.hold.mu.Lock()
		delete(s.hold.cancels, key)
		s.hold.mu.Unlock()
		cancel()
	}, nil
}

// backupSuspended reports whether ctx ended because backups were paused.
func (s *Server) backupSuspended(ctx context.Context) bool {
	return ctx.Err() != nil && s.backupsPaused() != ""
}

// backupsPaused returns why backups are paused, or "".
func (s *Server) backupsPaused() string {
	s.hold.mu.Lock()
	defer s.hold.mu.Unlock()
	return s.hold.reason
}

// suspendBackups pauses all backup work for reason, cancels what runs and
// waits briefly for it to let go of the agent. It returns how many backups
// were stopped.
func (s *Server) suspendBackups(reason string, wait time.Duration) int {
	s.hold.mu.Lock()
	s.hold.reason = reason
	n := len(s.hold.cancels)
	for _, cancel := range s.hold.cancels {
		cancel()
	}
	s.hold.mu.Unlock()
	deadline := time.Now().Add(wait)
	for time.Now().Before(deadline) {
		s.hold.mu.Lock()
		left := len(s.hold.cancels)
		s.hold.mu.Unlock()
		if left == 0 {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	return n
}

// resumeBackups lifts a pause. Paused backups are run again by
// ResumeSuspendedBackups.
func (s *Server) resumeBackups() {
	s.hold.mu.Lock()
	s.hold.reason = ""
	s.hold.mu.Unlock()
}

// recordSuspended records a policy backup that never started because
// backups were paused, so it is resumed like a cancelled one.
func (s *Server) recordSuspended(ctx context.Context, clusterID, policyID, targetID, workloadID string) appdb.BackupRun {
	now := s.now()
	run := appdb.BackupRun{
		ClusterID: clusterID, PolicyID: policyID, TargetID: targetID, WorkloadID: workloadID,
		Status: appdb.BackupInterrupted, Error: backupSuspendedError, StartedAt: now, FinishedAt: &now,
	}
	run.ID = uuid.NewString()
	_ = s.Store.CreateBackupRun(context.WithoutCancel(ctx), run)
	return run
}

// updateInProgress reports whether an update or rollback is being prepared
// or is running on the host.
func (s *Server) updateInProgress(ctx context.Context, clusterID string) bool {
	if s.applyPreparing.Load() {
		return true
	}
	last, err := s.Store.GetLatestUpdateOperation(ctx, clusterID)
	return err == nil && last != nil && hostChange(last.Action) && last.Status == appdb.UpdateRunning && !last.DryRun
}

// ResumeSuspendedBackups runs every backup an update paused, once no update
// is in progress. Each paused run is marked resumed first, so it runs once.
func (s *Server) ResumeSuspendedBackups(ctx context.Context, clusterID string) int {
	if s.backupsPaused() != "" || s.updateInProgress(ctx, clusterID) {
		return 0
	}
	runs, err := s.Store.ListBackupRuns(ctx, clusterID)
	if err != nil {
		return 0
	}
	now := s.now()
	seen := map[string]bool{}
	type job struct{ workloadID, targetID, policyID string }
	var jobs []job
	for _, run := range runs {
		if run.Status != appdb.BackupInterrupted || run.Error != backupSuspendedError || now.Sub(run.StartedAt) > resumeWindow {
			continue
		}
		run.Error = backupResumedError
		if err := s.Store.UpdateBackupRun(ctx, run); err != nil {
			continue
		}
		key := run.WorkloadID + "\x00" + run.TargetID
		if seen[key] {
			continue
		}
		seen[key] = true
		jobs = append(jobs, job{run.WorkloadID, run.TargetID, run.PolicyID})
	}
	for _, j := range jobs {
		if _, err := s.executeBackup(ctx, clusterID, j.workloadID, j.targetID, j.policyID, "", nil); err != nil && !errors.Is(err, errBackupSuspended) {
			log.Printf("backup: resuming paused backup of %s: %v", j.workloadID, err)
		}
		if j.policyID != "" {
			s.afterBackupAttempt(ctx, clusterID, j.workloadID, j.targetID, j.policyID)
		}
	}
	return len(jobs)
}
