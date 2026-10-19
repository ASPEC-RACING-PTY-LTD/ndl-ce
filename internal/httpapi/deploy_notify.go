package httpapi

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/no-dal/ndl-ce/internal/agentrpc"
	"github.com/no-dal/ndl-ce/internal/appdb"
)

// --- notifications ---

func wantsEvent(events []string, ev string) bool {
	for _, e := range events {
		if e == ev {
			return true
		}
	}
	return false
}

// notifyChannels posts payload to the cluster's webhook channels, or only
// to ids when some are given.
func (s *Server) notifyChannels(ctx context.Context, clusterID string, ids []string, payload []byte) {
	if len(ids) == 0 {
		s.notify(ctx, clusterID, payload)
		return
	}
	only := map[string]bool{}
	for _, id := range ids {
		only[id] = true
	}
	chs, err := s.Store.ListNotificationChannels(ctx, clusterID)
	if err != nil {
		return
	}
	cli := s.HTTPClient
	if cli == nil {
		cli = webhookHTTPClient()
	}
	for _, ch := range chs {
		if !only[ch.ID] || ch.Kind != appdb.NotifyWebhook {
			continue
		}
		hook, _, err := s.Store.NotificationSecrets(ctx, clusterID, ch.ID)
		if err != nil || hook == "" || checkWebhookDestination(hook) != nil {
			continue
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, hook, bytes.NewReader(payload))
		if err != nil {
			continue
		}
		req.Header.Set("Content-Type", "application/json")
		if res, err := cli.Do(req); err == nil {
			_, _ = io.Copy(io.Discard, res.Body)
			_ = res.Body.Close()
		}
	}
}

// deployMessage is a notification body. text and content carry the same
// line so Slack and Discord webhooks show it without any adapter.
func deployMessage(event, line string, fields map[string]any) []byte {
	body := map[string]any{"event": event, "text": line, "content": line, "at": time.Now().UTC().Format(time.RFC3339)}
	for k, v := range fields {
		body[k] = v
	}
	raw, _ := json.Marshal(body)
	return raw
}

var ansiRE = regexp.MustCompile("\x1b\\[[0-9;?]*[A-Za-z]")

// logTail is the last lines of a deployment's log, without colours.
func (s *Server) logTail(ctx context.Context, d appdb.Deployment, lines int) string {
	rpc, err := s.deployRPC()
	if err != nil || d.Detail.LogDir == "" {
		return ""
	}
	off := d.Detail.LogBytes - 8192
	if off < 0 {
		off = 0
	}
	var lg agentrpc.DeployLog
	cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := rpc.Deploy(cctx, "deploy-log", agentrpc.DeployRequest{LogDir: d.Detail.LogDir, ID: d.ID, Offset: off, Max: 8192}, &lg); err != nil {
		return ""
	}
	all := strings.Split(strings.TrimRight(ansiRE.ReplaceAllString(string(utf8Prefix(lg.Data)), ""), "\n"), "\n")
	if len(all) > lines {
		all = all[len(all)-lines:]
	}
	return strings.Join(all, "\n")
}

// deployFinished publishes a finished deployment and notifies the
// project's channels, then reports a bulk run when its last one ends.
func (s *Server) deployFinished(ctx context.Context, d appdb.Deployment, proj *appdb.DeployProject) {
	s.emitEvent(ctx, d.ClusterID, "", "docker.deploy."+d.Status, map[string]string{
		"deployment_id": d.ID, "project_id": d.ProjectID, "project": d.Detail.ProjectName, "status": d.Status,
	})
	if proj == nil {
		return
	}
	event := "deploy.failed"
	if d.Status == appdb.DeploySucceeded {
		event = "deploy.succeeded"
	}
	if d.Status != appdb.DeployCancelled && wantsEvent(proj.Config.NotifyEvents, event) {
		ct := s.workloadNames(ctx, d.ClusterID)[d.WorkloadID].Name
		line := fmt.Sprintf("%s in %s: deployment %s", proj.Name, ct, deployStatusWord(d.Status))
		fields := map[string]any{
			"project": proj.Name, "project_id": proj.ID, "container": ct, "working_dir": proj.WorkingDir,
			"deployment_id": d.ID, "status": d.Status, "trigger": d.Trigger, "actor": d.Actor,
			"commit_before": d.Detail.CommitBefore, "commit_after": d.Detail.CommitAfter,
		}
		if d.StartedAt != nil && d.FinishedAt != nil {
			fields["duration_sec"] = int(d.FinishedAt.Sub(*d.StartedAt).Seconds())
		}
		if d.Status != appdb.DeploySucceeded {
			if d.Detail.ExitCode != nil {
				fields["exit_code"] = *d.Detail.ExitCode
			}
			if d.Detail.FailedStep > 0 && d.Detail.FailedStep <= len(d.Detail.Commands) {
				fields["failed_command"] = d.Detail.Commands[d.Detail.FailedStep-1].Run
				line += fmt.Sprintf(" at command %d", d.Detail.FailedStep)
			}
			if d.Detail.Error != "" {
				fields["error"] = d.Detail.Error
				line += ": " + d.Detail.Error
			}
			fields["log_tail"] = s.logTail(ctx, d, 20)
		}
		s.notifyChannels(ctx, d.ClusterID, proj.Config.NotifyChannels, deployMessage(event, line, fields))
	}
	if d.BatchID != "" {
		s.bulkFinished(ctx, d)
	}
}

func deployStatusWord(status string) string {
	switch status {
	case appdb.DeploySucceeded:
		return "succeeded"
	case appdb.DeployTimedOut:
		return "timed out"
	default:
		return status
	}
}

// bulkFinished notifies once when the last deployment of a bulk run ends.
func (s *Server) bulkFinished(ctx context.Context, d appdb.Deployment) {
	rt := s.deployRT()
	rt.mu.Lock()
	if rt.batchesDone[d.BatchID] {
		rt.mu.Unlock()
		return
	}
	deps, err := s.Store.ListDeployments(ctx, d.ClusterID, appdb.DeploymentFilter{Limit: 2000})
	if err != nil {
		rt.mu.Unlock()
		return
	}
	var batch []appdb.Deployment
	for _, x := range deps {
		if x.BatchID == d.BatchID {
			if appdb.DeployActive(x.Status) {
				rt.mu.Unlock()
				return
			}
			batch = append(batch, x)
		}
	}
	rt.batchesDone[d.BatchID] = true
	rt.mu.Unlock()
	counts := map[string]int{}
	var failed []string
	channels := map[string]bool{}
	all := false
	for _, x := range batch {
		counts[x.Status]++
		if x.Status != appdb.DeploySucceeded && x.Status != appdb.DeployCancelled {
			failed = append(failed, x.Detail.ProjectName)
		}
		proj, _ := s.Store.GetDeployProject(ctx, x.ClusterID, x.ProjectID)
		if proj == nil || !wantsEvent(proj.Config.NotifyEvents, "bulk.finished") {
			continue
		}
		if len(proj.Config.NotifyChannels) == 0 {
			all = true
		}
		for _, id := range proj.Config.NotifyChannels {
			channels[id] = true
		}
	}
	line := fmt.Sprintf("Bulk deployment finished: %d succeeded, %d failed", counts[appdb.DeploySucceeded], len(failed))
	if n := counts[appdb.DeployCancelled]; n > 0 {
		line += fmt.Sprintf(", %d cancelled", n)
	}
	if len(failed) > 0 {
		line += " (" + strings.Join(failed, ", ") + ")"
	}
	s.emitEvent(ctx, d.ClusterID, "", "docker.deploy.bulk.finished", map[string]string{"batch_id": d.BatchID, "summary": line})
	if !all && len(channels) == 0 {
		return
	}
	payload := deployMessage("bulk.finished", line, map[string]any{"batch_id": d.BatchID, "counts": counts, "failed": failed})
	if all {
		s.notifyChannels(ctx, d.ClusterID, nil, payload)
		return
	}
	ids := make([]string, 0, len(channels))
	for id := range channels {
		ids = append(ids, id)
	}
	s.notifyChannels(ctx, d.ClusterID, ids, payload)
}

// updateAvailable runs once per new remote commit: it notifies when the
// project asks for it and deploys when automatic deployment is on.
func (s *Server) updateAvailable(ctx context.Context, proj appdb.DeployProject) {
	st := proj.State
	st.NotifiedDue = st.RemoteCommit
	_ = s.Store.UpdateDeployProjectState(ctx, proj.ClusterID, proj.ID, st)
	proj.State = st
	if wantsEvent(proj.Config.NotifyEvents, "update.available") {
		ct := s.workloadNames(ctx, proj.ClusterID)[proj.WorkloadID].Name
		behind := "new commits"
		if st.Behind > 0 {
			behind = fmt.Sprintf("%d new commit(s)", st.Behind)
		}
		line := fmt.Sprintf("%s in %s has %s", proj.Name, ct, behind)
		if st.RemoteSubject != "" {
			line += ": " + st.RemoteSubject
		}
		s.notifyChannels(ctx, proj.ClusterID, proj.Config.NotifyChannels, deployMessage("update.available", line, map[string]any{
			"project": proj.Name, "project_id": proj.ID, "container": ct, "branch": st.Branch,
			"local_commit": st.LocalCommit, "remote_commit": st.RemoteCommit, "behind": st.Behind,
			"subject": st.RemoteSubject, "author": st.RemoteAuthor,
		}))
	}
	s.maybeAutoDeploy(ctx, proj)
}

// inAutoWindow reports whether now is inside "HH:MM-HH:MM" (local time); a
// window may run past midnight. An empty window is always open.
func inAutoWindow(window string, now time.Time) bool {
	if window == "" {
		return true
	}
	m := autoWindowR.FindStringSubmatch(window)
	if m == nil {
		return false
	}
	atoi := func(s string) int {
		n := 0
		for _, c := range s {
			n = n*10 + int(c-'0')
		}
		return n
	}
	from, to := atoi(m[1])*60+atoi(m[2]), atoi(m[3])*60+atoi(m[4])
	cur := now.Hour()*60 + now.Minute()
	if from <= to {
		return cur >= from && cur < to
	}
	return cur >= from || cur < to
}

// maybeAutoDeploy queues a project's new commit when it deploys on its own,
// once per commit, inside its window.
func (s *Server) maybeAutoDeploy(ctx context.Context, proj appdb.DeployProject) {
	st := proj.State
	if !proj.Config.AutoDeploy || !st.Due || st.RemoteCommit == "" || st.AutoCommit == st.RemoteCommit {
		return
	}
	if !inAutoWindow(proj.Config.AutoWindow, s.now().Local()) {
		return
	}
	if _, err := s.enqueueDeployment(ctx, proj, "auto", "", "", false); err != nil {
		return
	}
	cur, err := s.Store.GetDeployProject(ctx, proj.ClusterID, proj.ID)
	if err != nil || cur == nil {
		return
	}
	cur.State.AutoCommit = st.RemoteCommit
	_ = s.Store.UpdateDeployProjectState(ctx, proj.ClusterID, proj.ID, cur.State)
	s.auditInternal(proj.ClusterID, "", "docker.deploy.auto", "queued", fmt.Sprintf("%s: new commit %s", proj.Name, shortSHA(st.RemoteCommit)))
}

func shortSHA(c string) string {
	if len(c) > 7 {
		return c[:7]
	}
	return c
}

// --- webhooks ---

func (s *Server) rotateDeployWebhook(w http.ResponseWriter, r *http.Request) {
	p, ok := s.requireDeployOwner(w, r)
	if !ok {
		return
	}
	proj, ok := s.loadDeployProject(w, r, p)
	if !ok {
		return
	}
	var in struct {
		Enabled bool `json:"enabled"`
	}
	if err := readJSON(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	ctx, cid := r.Context(), p.User.ClusterID
	env, _, err := s.Store.DeployProjectSecrets(ctx, cid, proj.ID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	secret := ""
	if in.Enabled {
		b := make([]byte, 32)
		if _, err := rand.Read(b); err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		secret = hex.EncodeToString(b)
	}
	if err := s.Store.PutDeployProjectSecrets(ctx, cid, proj.ID, env, secret); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	proj.Config.Webhook = in.Enabled
	if err := s.Store.PutDeployProject(ctx, *proj); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	result := "disabled"
	if in.Enabled {
		result = "new secret"
	}
	s.audit(r, cid, p.User.ID, "docker.deploy.webhook", result, proj.Name)
	writeJSON(w, http.StatusOK, map[string]any{"enabled": in.Enabled, "secret": secret, "path": "/api/v1/hooks/deploy/" + proj.ID})
}

// webhookSigned checks a push webhook against the project's secret:
// GitHub, Gitea, Forgejo and CodeHold sign the body (X-Hub-Signature-256
// or X-Gitea-Signature); CI jobs may send the secret as a bearer token.
func webhookSigned(r *http.Request, body []byte, secret string) bool {
	if secret == "" {
		return false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	want := mac.Sum(nil)
	for _, h := range []string{"X-Hub-Signature-256", "X-Gitea-Signature", "X-Gogs-Signature", "X-Forgejo-Signature", "X-NDL-Signature"} {
		v := strings.TrimPrefix(strings.TrimSpace(r.Header.Get(h)), "sha256=")
		if v == "" {
			continue
		}
		got, err := hex.DecodeString(v)
		if err == nil && hmac.Equal(got, want) {
			return true
		}
	}
	if auth := strings.TrimSpace(r.Header.Get("Authorization")); strings.HasPrefix(auth, "Bearer ") {
		tok := strings.TrimSpace(strings.TrimPrefix(auth, "Bearer "))
		return subtle.ConstantTimeCompare([]byte(tok), []byte(secret)) == 1
	}
	return false
}

// deployWebhook receives a push. It checks the project for updates; it
// deploys only when the project deploys automatically.
func (s *Server) deployWebhook(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	cluster, err := s.Store.GetCluster(ctx)
	if err != nil || cluster == nil {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "could not read the body")
		return
	}
	proj, _ := s.Store.GetDeployProject(ctx, cluster.ID, r.PathValue("id"))
	if proj == nil || !proj.Config.Webhook {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	_, secret, err := s.Store.DeployProjectSecrets(ctx, cluster.ID, proj.ID)
	if err != nil || !webhookSigned(r, body, secret) {
		s.auditInternal(cluster.ID, "", "docker.deploy.webhook", "refused", proj.Name+": bad or missing signature from "+s.clientIP(r))
		writeErr(w, http.StatusUnauthorized, "the signature does not match")
		return
	}
	ev := firstNonEmpty(r.Header.Get("X-GitHub-Event"), r.Header.Get("X-Gitea-Event"), r.Header.Get("X-Forgejo-Event"), r.Header.Get("X-Gogs-Event"))
	if ev == "ping" {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "ping": true})
		return
	}
	var push struct {
		Ref string `json:"ref"`
	}
	_ = json.Unmarshal(body, &push)
	branch := firstNonEmpty(proj.Config.Branch, proj.State.Branch)
	if push.Ref != "" && branch != "" && push.Ref != "refs/heads/"+branch {
		writeJSON(w, http.StatusAccepted, map[string]any{"ok": true, "ignored": "not the " + branch + " branch"})
		return
	}
	rt := s.deployRT()
	rt.mu.Lock()
	if rt.hookChecks[proj.ID] {
		rt.mu.Unlock()
		writeJSON(w, http.StatusAccepted, map[string]any{"ok": true, "checking": true})
		return
	}
	rt.hookChecks[proj.ID] = true
	rt.mu.Unlock()
	s.auditInternal(cluster.ID, "", "docker.deploy.webhook", "ok", fmt.Sprintf("%s: push %s", proj.Name, firstNonEmpty(push.Ref, ev, "received")))
	go func(p appdb.DeployProject) {
		defer func() {
			rt.mu.Lock()
			delete(rt.hookChecks, p.ID)
			rt.mu.Unlock()
		}()
		cctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		_, _ = s.checkProject(cctx, p)
	}(*proj)
	writeJSON(w, http.StatusAccepted, map[string]any{"ok": true, "checking": true, "auto_deploy": proj.Config.AutoDeploy})
}
