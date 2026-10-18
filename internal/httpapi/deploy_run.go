package httpapi

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/no-dal/ndl-ce/internal/agentrpc"
	"github.com/no-dal/ndl-ce/internal/appdb"
	"github.com/no-dal/ndl-ce/internal/deploy"
)

// RunDeployments starts queued deployments within the limits, follows
// running ones on the node, checks projects for updates and expires old
// logs. Only the writer runs it.
func (s *Server) RunDeployments(ctx context.Context) {
	rt := s.deployRT()
	t := time.NewTicker(2 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		case <-rt.kick:
		}
		s.tickDeployments(ctx)
	}
}

func (s *Server) tickDeployments(ctx context.Context) {
	rt := s.deployRT()
	if !rt.busy.CompareAndSwap(false, true) {
		return
	}
	defer rt.busy.Store(false)
	cluster, err := s.Store.GetCluster(ctx)
	if err != nil || cluster == nil || cluster.SetupCompletedAt == nil {
		return
	}
	if !s.dockerEnabled(ctx, cluster.ID) {
		return
	}
	s.dispatchDeployments(ctx, cluster.ID)
	s.tickDeployDetect(ctx, cluster.ID)
	s.tickDeployRetention(ctx, cluster.ID)
}

// dispatchDeployments follows running deployments and starts queued ones,
// oldest first, while there is room globally and in each container.
func (s *Server) dispatchDeployments(ctx context.Context, clusterID string) {
	settings := s.deploySettings(ctx, clusterID)
	active, err := s.Store.ListDeployments(ctx, clusterID, appdb.DeploymentFilter{Active: true})
	if err != nil {
		return
	}
	sort.Slice(active, func(i, j int) bool { return active[i].QueuedAt.Before(active[j].QueuedAt) })
	running := 0
	perCT := map[string]int{}
	for _, d := range active {
		if d.Status != appdb.DeployRunning && d.Status != appdb.DeployStarting {
			continue
		}
		if s.superviseDeployment(ctx, d) {
			running++
			perCT[d.WorkloadID]++
		}
	}
	var backupBusy map[string]bool
	for _, d := range active {
		if d.Status != appdb.DeployQueued && d.Status != appdb.DeployWaiting {
			continue
		}
		proj, err := s.Store.GetDeployProject(ctx, clusterID, d.ProjectID)
		if err != nil || proj == nil {
			continue
		}
		reason := ""
		switch {
		case running >= settings.MaxConcurrent:
			reason = fmt.Sprintf("waiting for a free slot (%d of %d deploying)", running, settings.MaxConcurrent)
		case perCT[d.WorkloadID] >= settings.PerContainer:
			reason = "waiting for another deployment in this container"
		case !d.Detail.Force && !proj.Config.DeployDuringBackup:
			if backupBusy == nil {
				backupBusy = s.backupBusyWorkloads(ctx, clusterID)
			}
			if backupBusy[d.WorkloadID] {
				reason = "waiting for a backup of this container to finish"
			}
		}
		if reason != "" {
			s.markWaiting(ctx, d, reason)
			continue
		}
		if s.startDeployment(ctx, settings, d, *proj) {
			running++
			perCT[d.WorkloadID]++
		}
	}
}

// backupBusyWorkloads lists containers a backup run is working on.
func (s *Server) backupBusyWorkloads(ctx context.Context, clusterID string) map[string]bool {
	out := map[string]bool{}
	runs, err := s.Store.ListBackupRuns(ctx, clusterID)
	if err != nil {
		return out
	}
	for _, r := range runs {
		if r.Status == appdb.BackupRunning && r.WorkloadID != "" {
			out[r.WorkloadID] = true
		}
	}
	return out
}

func (s *Server) markWaiting(ctx context.Context, d appdb.Deployment, reason string) {
	if d.Status == appdb.DeployWaiting && d.Detail.WaitingFor == reason {
		return
	}
	rt := s.deployRT()
	rt.mu.Lock()
	defer rt.mu.Unlock()
	cur, err := s.Store.GetDeployment(ctx, d.ClusterID, d.ID)
	if err != nil || cur == nil || (cur.Status != appdb.DeployQueued && cur.Status != appdb.DeployWaiting) {
		return
	}
	cur.Status, cur.Detail.WaitingFor = appdb.DeployWaiting, reason
	_ = s.Store.PutDeployment(ctx, *cur)
}

// gitHead reads the commit a project's folder is on, or "".
func (s *Server) gitHead(ctx context.Context, proj appdb.DeployProject) string {
	rpc, err := s.deployRPC()
	if err != nil {
		return ""
	}
	var res deploy.ProbeResult
	cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if err := rpc.Deploy(cctx, "deploy-probe", agentrpc.DeployRequest{
		MachineID: proj.WorkloadID, Dir: proj.WorkingDir, RunAs: proj.Config.RunAs,
		Script: "git rev-parse HEAD 2>/dev/null", Timeout: 25,
	}, &res); err != nil || res.ExitCode != 0 {
		return ""
	}
	head := strings.TrimSpace(res.Output)
	if !isCommitID(head) {
		return ""
	}
	return head
}

func isCommitID(s string) bool {
	if len(s) != 40 && len(s) != 64 {
		return false
	}
	for _, c := range s {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			return false
		}
	}
	return true
}

// startDeployment hands a queued deployment to the node. It reports
// whether it is now running.
func (s *Server) startDeployment(ctx context.Context, settings appdb.DeploySettings, d appdb.Deployment, proj appdb.DeployProject) bool {
	rt := s.deployRT()
	rt.mu.Lock()
	cur, err := s.Store.GetDeployment(ctx, d.ClusterID, d.ID)
	if err != nil || cur == nil || (cur.Status != appdb.DeployQueued && cur.Status != appdb.DeployWaiting) {
		rt.mu.Unlock()
		return false
	}
	d = *cur
	d.Status, d.Detail.WaitingFor, d.Detail.LogDir = appdb.DeployStarting, "", settings.LogDir
	_ = s.Store.PutDeployment(ctx, d)
	rt.starting[d.ID] = true
	rt.mu.Unlock()
	defer func() {
		rt.mu.Lock()
		delete(rt.starting, d.ID)
		rt.mu.Unlock()
	}()

	fail := func(msg string) bool {
		rt.mu.Lock()
		now := s.now()
		d.Status, d.FinishedAt, d.Detail.Error = appdb.DeployFailed, &now, msg
		if d.StartedAt == nil {
			d.StartedAt = &now
		}
		_ = s.Store.PutDeployment(ctx, d)
		rt.mu.Unlock()
		s.afterDeployment(ctx, d)
		return false
	}
	wl, err := s.deployWorkload(ctx, d.ClusterID, d.WorkloadID)
	if err != nil {
		return fail(err.Error())
	}
	if wl.Status != "" && wl.Status != "running" {
		return fail(fmt.Sprintf("%s is %s; start it first", wl.Name, wl.Status))
	}
	rpc, err := s.deployRPC()
	if err != nil {
		return fail(err.Error())
	}
	env, _, err := s.Store.DeployProjectSecrets(ctx, d.ClusterID, proj.ID)
	if err != nil {
		return fail("the project's environment could not be read")
	}
	if head := s.gitHead(ctx, proj); head != "" {
		d.Detail.CommitBefore = head
	}
	cmds := make([]deploy.Command, len(d.Detail.Commands))
	for i, c := range d.Detail.Commands {
		cmds[i] = deploy.Command{Run: c.Run, ContinueOnError: c.ContinueOnError}
	}
	spec := deploy.Spec{
		ID: d.ID, MachineID: d.WorkloadID, Dir: d.Detail.WorkingDir, RunAs: d.Detail.RunAs,
		Commands: cmds, Env: env, TimeoutSec: d.Detail.TimeoutSec, LogDir: settings.LogDir,
	}
	var st deploy.Status
	cctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	err = rpc.Deploy(cctx, "deploy-start", agentrpc.DeployRequest{Spec: spec}, &st)
	cancel()
	if err != nil {
		return fail("the node could not start it: " + err.Error())
	}
	rt.mu.Lock()
	cur, _ = s.Store.GetDeployment(ctx, d.ClusterID, d.ID)
	cancelBy := ""
	if cur != nil {
		cancelBy = cur.Detail.CancelBy
	}
	now := s.now()
	d.Status, d.StartedAt, d.Detail.CancelBy = appdb.DeployRunning, &now, cancelBy
	applyRunStatus(&d, st)
	_ = s.Store.PutDeployment(ctx, d)
	rt.mu.Unlock()
	if cancelBy != "" {
		cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		_ = rpc.Deploy(cctx, "deploy-cancel", agentrpc.DeployRequest{LogDir: d.Detail.LogDir, ID: d.ID}, nil)
		cancel()
	}
	s.auditInternal(d.ClusterID, "", "docker.deploy.run", "started", fmt.Sprintf("%s (%s) in %s as %s, trigger %s by %s",
		d.Detail.ProjectName, d.ID, d.Detail.WorkingDir, firstNonEmpty(st.RunAs, d.Detail.RunAs, "the folder's owner"), d.Trigger, firstNonEmpty(d.Actor, "NDL-CE")))
	return true
}

func applyRunStatus(d *appdb.Deployment, st deploy.Status) {
	d.Detail.LogBytes = st.LogBytes
	d.Detail.Mode = st.Mode
	if st.RunAs != "" {
		d.Detail.RunAs = st.RunAs
	}
	if len(st.Steps) > 0 {
		steps := make([]appdb.DeployStep, len(st.Steps))
		for i, x := range st.Steps {
			steps[i] = appdb.DeployStep{Command: x.Command, StartedAt: x.StartedAt, FinishedAt: x.FinishedAt, ExitCode: x.ExitCode, Tolerated: x.Tolerated}
		}
		d.Detail.Steps = steps
	}
}

// superviseDeployment reads a running deployment from the node and
// finishes it when it ends. It reports whether it is still running.
func (s *Server) superviseDeployment(ctx context.Context, d appdb.Deployment) bool {
	rt := s.deployRT()
	if d.Status == appdb.DeployStarting {
		rt.mu.Lock()
		mine := rt.starting[d.ID]
		rt.mu.Unlock()
		if mine {
			// Being handed over by startDeployment; it owns it.
			return true
		}
		// This control plane restarted while handing it over: the node
		// either has it or never got it.
		return s.adoptStarting(ctx, d)
	}
	rpc, err := s.deployRPC()
	if err != nil {
		return true
	}
	var st deploy.Status
	cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	err = rpc.Deploy(cctx, "deploy-status", agentrpc.DeployRequest{LogDir: d.Detail.LogDir, ID: d.ID}, &st)
	cancel()
	if err != nil {
		rt.mu.Lock()
		rt.misses[d.ID]++
		misses := rt.misses[d.ID]
		rt.mu.Unlock()
		// The agent may be restarting; give it a few minutes.
		if misses < 90 {
			return true
		}
		st = deploy.Status{State: deploy.StateInterrupted, Error: "the node stopped answering about this deployment: " + err.Error()}
	}
	rt.mu.Lock()
	delete(rt.misses, d.ID)
	cur, gerr := s.Store.GetDeployment(ctx, d.ClusterID, d.ID)
	if gerr != nil || cur == nil || cur.Status != appdb.DeployRunning {
		rt.mu.Unlock()
		return false
	}
	d = *cur
	applyRunStatus(&d, st)
	if !st.Done() {
		_ = s.Store.PutDeployment(ctx, d)
		rt.mu.Unlock()
		return true
	}
	now := s.now()
	if st.FinishedAt != nil {
		now = *st.FinishedAt
	}
	d.FinishedAt = &now
	d.Status = deployStatusFrom(st.State)
	d.Detail.ExitCode, d.Detail.FailedStep, d.Detail.Error = st.ExitCode, st.FailedStep, st.Error
	if d.Status == appdb.DeployCancelled && d.Detail.CancelBy != "" {
		d.Detail.Error = "cancelled by " + d.Detail.CancelBy
	}
	_ = s.Store.PutDeployment(ctx, d)
	rt.mu.Unlock()
	s.afterDeployment(ctx, d)
	return false
}

// adoptStarting settles a deployment left "starting" by a restart.
func (s *Server) adoptStarting(ctx context.Context, d appdb.Deployment) bool {
	rpc, err := s.deployRPC()
	if err != nil {
		return true
	}
	var st deploy.Status
	cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	err = rpc.Deploy(cctx, "deploy-status", agentrpc.DeployRequest{LogDir: d.Detail.LogDir, ID: d.ID}, &st)
	cancel()
	rt := s.deployRT()
	rt.mu.Lock()
	cur, _ := s.Store.GetDeployment(ctx, d.ClusterID, d.ID)
	if cur == nil || cur.Status != appdb.DeployStarting || rt.starting[d.ID] {
		rt.mu.Unlock()
		return cur != nil && appdb.DeployActive(cur.Status)
	}
	d = *cur
	now := s.now()
	if err == nil {
		d.Status, d.StartedAt = appdb.DeployRunning, &st.StartedAt
		applyRunStatus(&d, st)
		_ = s.Store.PutDeployment(ctx, d)
		rt.mu.Unlock()
		return true
	}
	d.Status, d.FinishedAt, d.StartedAt = appdb.DeployInterrupted, &now, &now
	d.Detail.Error = "NDL-CE restarted while starting this deployment; it did not run"
	_ = s.Store.PutDeployment(ctx, d)
	rt.mu.Unlock()
	s.afterDeployment(ctx, d)
	return false
}

func deployStatusFrom(state string) string {
	switch state {
	case deploy.StateSucceeded:
		return appdb.DeploySucceeded
	case deploy.StateCancelled:
		return appdb.DeployCancelled
	case deploy.StateTimedOut:
		return appdb.DeployTimedOut
	case deploy.StateInterrupted:
		return appdb.DeployInterrupted
	default:
		return appdb.DeployFailed
	}
}

// afterDeployment records a finished deployment on its project, audits it,
// notifies, and reads the containers' state for information.
func (s *Server) afterDeployment(ctx context.Context, d appdb.Deployment) {
	proj, err := s.Store.GetDeployProject(ctx, d.ClusterID, d.ProjectID)
	if err == nil && proj != nil {
		if d.StartedAt != nil && d.Status != appdb.DeployCancelled {
			if head := s.gitHead(ctx, *proj); head != "" {
				d.Detail.CommitAfter = head
				rt := s.deployRT()
				rt.mu.Lock()
				if cur, _ := s.Store.GetDeployment(ctx, d.ClusterID, d.ID); cur != nil {
					cur.Detail.CommitAfter = head
					_ = s.Store.PutDeployment(ctx, *cur)
				}
				rt.mu.Unlock()
			}
		}
		st := proj.State
		st.LastDeployID, st.LastDeployStatus, st.LastDeployAt = d.ID, d.Status, d.FinishedAt
		if d.Status == appdb.DeploySucceeded {
			st.LastSuccessAt = d.FinishedAt
		}
		if d.Detail.CommitAfter != "" {
			st.LocalCommit = d.Detail.CommitAfter
			if st.RemoteCommit != "" && st.RemoteCommit == st.LocalCommit {
				st.Due, st.DueSince, st.Behind = false, nil, 0
			}
			// Look again soon: the commands may have moved the branch.
			next := s.now().Add(5 * time.Second)
			st.NextCheckAt = &next
		}
		_ = s.Store.UpdateDeployProjectState(ctx, d.ClusterID, d.ProjectID, st)
	}
	detail := fmt.Sprintf("%s (%s): %s", d.Detail.ProjectName, d.ID, d.Status)
	if d.Detail.ExitCode != nil && *d.Detail.ExitCode != 0 {
		detail += fmt.Sprintf(", exit code %d at command %d", *d.Detail.ExitCode, d.Detail.FailedStep)
	}
	if d.Detail.Error != "" {
		detail += ": " + d.Detail.Error
	}
	result := "ok"
	if d.Status != appdb.DeploySucceeded {
		result = d.Status
	}
	s.auditInternal(d.ClusterID, "", "docker.deploy.finish", result, detail)
	s.deployFinished(ctx, d, proj)
	if proj != nil && d.Status == appdb.DeploySucceeded {
		go s.deployHealth(context.WithoutCancel(ctx), d, *proj)
	}
	s.deployKick()
}

// deployHealth notes the project's containers' state after a deployment.
// It is information only: it never changes the deployment's result.
func (s *Server) deployHealth(ctx context.Context, d appdb.Deployment, proj appdb.DeployProject) {
	select {
	case <-ctx.Done():
		return
	case <-time.After(10 * time.Second):
	}
	inv, err := s.dockerInventory(ctx, d.ClusterID, true)
	if err != nil {
		return
	}
	var notes []string
	for _, pr := range inv.Projects {
		if pr.MachineID != proj.WorkloadID || pr.WorkingDir != proj.WorkingDir {
			continue
		}
		for _, c := range pr.Containers {
			note := c.Name + ": " + c.State
			if c.HealthStatus != "" {
				note += " (" + c.HealthStatus + ")"
			}
			notes = append(notes, note)
		}
	}
	if len(notes) == 0 {
		return
	}
	sort.Strings(notes)
	rt := s.deployRT()
	rt.mu.Lock()
	defer rt.mu.Unlock()
	if cur, _ := s.Store.GetDeployment(ctx, d.ClusterID, d.ID); cur != nil {
		cur.Detail.Health = notes
		_ = s.Store.PutDeployment(ctx, *cur)
	}
}

// DeploymentsBusy lists running deployments; a live update waits for them.
func (s *Server) deploymentsBusy(ctx context.Context, clusterID string) int {
	deps, err := s.Store.ListDeployments(ctx, clusterID, appdb.DeploymentFilter{Active: true})
	if err != nil {
		return 0
	}
	n := 0
	for _, d := range deps {
		if d.Status == appdb.DeployRunning || d.Status == appdb.DeployStarting {
			n++
		}
	}
	return n
}
