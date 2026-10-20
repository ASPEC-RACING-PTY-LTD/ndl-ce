package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"time"

	"github.com/no-dal/ndl-ce/internal/appdb"
	"github.com/no-dal/ndl-ce/internal/backup"
	"github.com/no-dal/ndl-ce/internal/backuphost"
	"github.com/no-dal/ndl-ce/internal/qemu"
	"github.com/no-dal/ndl-ce/internal/rbac"
)

// Pausing a backup policy stops it at once: its running captures are
// cancelled, its not-yet-started backups never start, and its pending
// uploads are taken off the upload queue. Each stopped backup is recorded
// as interrupted. Resuming runs every stopped backup again from the start
// and puts the uploads back on the queue. Run now on a paused policy only
// queues one run, made on resume; Clear queue drops all of that.

const (
	policyPausedError  = "stopped because its policy was paused; it runs again from the start when the policy is resumed"
	policyResumedError = "stopped because its policy was paused and run again when it was resumed"
	policyClearedError = "stopped because its policy was paused; the queue was cleared, so it does not run again"
	// policyStopWait bounds how long a pause waits for cancelled captures
	// to let go of the agent before it answers.
	policyStopWait = 15 * time.Second
	// resumePoll is how often a resume retries while another policy runs.
	resumePoll = 5 * time.Second
)

var (
	errPolicyPaused = errConflict("backup policy is paused")
	errPolicyBusy   = errConflict("backup policy is already running")
)

// stopPolicyBackups cancels every running or waiting backup of a policy and
// waits briefly for them to finish cancelling. It returns how many it stopped.
func (s *Server) stopPolicyBackups(policyID string) int {
	s.hold.mu.Lock()
	if s.hold.stopped == nil {
		s.hold.stopped = map[string]bool{}
	}
	s.hold.stopped[policyID] = true
	var keys []string
	for key, pid := range s.hold.policyOf {
		if pid == policyID {
			keys = append(keys, key)
			if cancel := s.hold.cancels[key]; cancel != nil {
				cancel()
			}
		}
	}
	s.hold.mu.Unlock()
	deadline := time.Now().Add(policyStopWait)
	for time.Now().Before(deadline) {
		s.hold.mu.Lock()
		left := 0
		for _, key := range keys {
			if _, ok := s.hold.cancels[key]; ok {
				left++
			}
		}
		s.hold.mu.Unlock()
		if left == 0 {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	return len(keys)
}

// policyStopped reports whether a pause stopped this policy's backups.
func (s *Server) policyStopped(policyID string) bool {
	if policyID == "" {
		return false
	}
	s.hold.mu.Lock()
	defer s.hold.mu.Unlock()
	return s.hold.stopped[policyID]
}

func (s *Server) clearPolicyStopped(policyID string) {
	s.hold.mu.Lock()
	delete(s.hold.stopped, policyID)
	s.hold.mu.Unlock()
}

// recordPolicyPaused records a policy backup that a pause stopped before it
// started, so resuming runs it like one cancelled mid-way.
func (s *Server) recordPolicyPaused(ctx context.Context, clusterID, policyID, targetID, workloadID string) appdb.BackupRun {
	run := s.recordSuspended(ctx, clusterID, policyID, targetID, workloadID)
	run.Error = policyPausedError
	_ = s.Store.UpdateBackupRun(context.WithoutCancel(ctx), run)
	return run
}

// pendingUpload is a restore point still waiting to reach its target.
type pendingUpload struct {
	appdb.BackupArtifact
	TargetID string
}

// pendingUploadArtifacts are a policy's restore points still waiting to
// reach their remote target.
func (s *Server) pendingUploadArtifacts(ctx context.Context, clusterID, policyID string) []pendingUpload {
	runs, err := s.Store.ListBackupRuns(ctx, clusterID)
	if err != nil {
		return nil
	}
	mine := map[string]string{}
	for _, r := range runs {
		if r.PolicyID == policyID {
			mine[r.ID] = r.TargetID
		}
	}
	arts, err := s.Store.ListBackupArtifacts(ctx, clusterID)
	if err != nil {
		return nil
	}
	var out []pendingUpload
	for _, a := range arts {
		target, ok := mine[a.RunID]
		if !ok || a.BackupID == "" || a.Namespace == "" {
			continue
		}
		switch backup.RemoteState(a.RemoteState) {
		case backup.RemoteQueued, backup.RemoteUploading, backup.RemoteFailed:
			out = append(out, pendingUpload{a, target})
		}
	}
	return out
}

// moveUploads takes a policy's pending uploads off the queue (action
// dequeue) or puts them back (enqueue).
func (s *Server) moveUploads(ctx context.Context, clusterID, policyID, action, op string) int {
	if s.Backup == nil {
		return 0
	}
	n := 0
	for _, a := range s.pendingUploadArtifacts(ctx, clusterID, policyID) {
		tgt, err := s.Store.GetBackupTarget(ctx, clusterID, a.TargetID)
		if err != nil || tgt == nil || !isObjectBackupKind(tgt.Kind) {
			continue
		}
		raw, _ := json.Marshal(backuphost.Request{Action: action, Namespace: a.Namespace, BackupID: a.BackupID, Target: s.v2TargetSpec(ctx, *tgt)})
		if _, err := s.Backup.CopyBackup(ctx, op, "", string(raw)); err != nil {
			log.Printf("backup: %s upload of %s: %v", action, a.BackupID, err)
			continue
		}
		n++
	}
	return n
}

// pausedRuns are a policy's backups a pause stopped that wait for resume.
func (s *Server) pausedRuns(ctx context.Context, clusterID, policyID string) []appdb.BackupRun {
	runs, err := s.Store.ListBackupRuns(ctx, clusterID)
	if err != nil {
		return nil
	}
	var out []appdb.BackupRun
	for _, r := range runs {
		if r.PolicyID == policyID && r.Status == appdb.BackupInterrupted && r.Error == policyPausedError {
			out = append(out, r)
		}
	}
	return out
}

// pausePolicy stops everything a policy is doing; called once the policy
// is saved as paused.
func (s *Server) pausePolicy(ctx context.Context, clusterID, policyID string) {
	if n := s.stopPolicyBackups(policyID); n > 0 {
		log.Printf("backup: policy %s paused; stopped %d backup(s)", policyID, n)
	}
	s.moveUploads(ctx, clusterID, policyID, backuphost.ActionDequeue, qemu.BackupV2Dequeue)
}

// resumePolicy puts a resumed policy's uploads back on the queue and runs
// its stopped backups again from the start, or the whole policy when a Run
// now was queued while it was paused.
func (s *Server) resumePolicy(ctx context.Context, clusterID, policyID string) {
	s.clearPolicyStopped(policyID)
	s.moveUploads(ctx, clusterID, policyID, backuphost.ActionEnqueue, qemu.BackupV2Enqueue)
	pol, err := s.Store.GetBackupPolicy(ctx, clusterID, policyID)
	if err != nil || pol == nil || pol.Paused {
		return
	}
	only := map[string]bool{}
	for _, r := range s.pausedRuns(ctx, clusterID, policyID) {
		r.Error = policyResumedError
		if s.Store.UpdateBackupRun(ctx, r) == nil {
			only[r.WorkloadID] = true
		}
	}
	if pol.RunQueued {
		_ = s.Store.SetBackupPolicyRunQueued(ctx, clusterID, policyID, false)
		only = nil
	} else if len(only) == 0 {
		return
	}
	// Only one policy runs at a time, and the stopped run may still be
	// winding down: wait for the slot, giving up if paused again.
	for deadline := time.Now().Add(resumeWindow); time.Now().Before(deadline); time.Sleep(resumePoll) {
		if cur, _ := s.Store.GetBackupPolicy(ctx, clusterID, policyID); cur == nil || cur.Paused {
			return
		}
		_, err := s.executePolicyBackupsFor(ctx, clusterID, policyID, only)
		if !errors.Is(err, errPolicyBusy) {
			if err != nil {
				log.Printf("backup: resuming policy %s: %v", policyID, err)
			}
			return
		}
	}
}

// clearPolicyQueue drops a policy's queued Run now and the backups a pause
// stopped, so resuming does not run them.
func (s *Server) clearPolicyQueue(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.BackupCreate)
	if err != nil {
		return
	}
	ctx := r.Context()
	pol, err := s.Store.GetBackupPolicy(ctx, p.User.ClusterID, r.PathValue("id"))
	if err != nil || pol == nil {
		writeErr(w, http.StatusNotFound, "backup policy not found")
		return
	}
	cleared := 0
	if pol.RunQueued {
		if err := s.Store.SetBackupPolicyRunQueued(ctx, p.User.ClusterID, pol.ID, false); err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		cleared++
	}
	for _, run := range s.pausedRuns(ctx, p.User.ClusterID, pol.ID) {
		run.Error = policyClearedError
		if s.Store.UpdateBackupRun(ctx, run) == nil {
			cleared++
		}
	}
	s.audit(r, p.User.ClusterID, p.User.ID, "backup.policy.clear_queue", "ok", pol.ID)
	writeJSON(w, http.StatusOK, map[string]any{"cleared": cleared})
}

// queuedRunCount is how many runs wait for a paused policy: its queued Run
// now plus the backups a pause stopped.
func (s *Server) queuedRunCount(ctx context.Context, clusterID string, pol appdb.BackupPolicy) int {
	n := len(s.pausedRuns(ctx, clusterID, pol.ID))
	if pol.RunQueued {
		n++
	}
	return n
}
