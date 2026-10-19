package httpapi

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/no-dal/ndl-ce/internal/agentrpc"
	"github.com/no-dal/ndl-ce/internal/appdb"
	"github.com/no-dal/ndl-ce/internal/deploy"
	"github.com/no-dal/ndl-ce/internal/features"
	"github.com/no-dal/ndl-ce/internal/rbac"
)

// localDeployGuest runs "in the container" on this machine, in a process
// session (no systemd), with each container's root mapped to a folder.
type localDeployGuest struct {
	mu   sync.Mutex
	runs []string
}

func (g *localDeployGuest) Run(ctx context.Context, _ string, argv ...string) ([]byte, error) {
	if strings.Contains(argv[len(argv)-1], "systemd-run") {
		return []byte("session\n"), nil
	}
	return exec.CommandContext(ctx, argv[0], argv[1:]...).CombinedOutput()
}

func (g *localDeployGuest) Stream(ctx context.Context, id string, stdin io.Reader, out io.Writer, argv ...string) error {
	g.mu.Lock()
	g.runs = append(g.runs, id)
	g.mu.Unlock()
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = stdin, out, out
	cmd.WaitDelay = 2 * time.Second
	return cmd.Run()
}

// fakeDeployer is the agent's deployment actions over a real manager.
type fakeDeployer struct {
	m *deploy.Manager
}

func (f *fakeDeployer) Deploy(ctx context.Context, action string, req agentrpc.DeployRequest, out any) error {
	var res any
	var err error
	switch action {
	case "deploy-start":
		res, err = f.m.Start(req.Spec)
	case "deploy-status":
		res, err = f.m.Status(req.LogDir, req.ID)
	case "deploy-cancel":
		res, err = f.m.Cancel(req.LogDir, req.ID)
	case "deploy-log":
		data, size, e := f.m.Log(req.LogDir, req.ID, req.Offset, req.Max)
		res, err = agentrpc.DeployLog{Data: data, Offset: req.Offset, Size: size}, e
	case "deploy-delete":
		n, e := f.m.Delete(req.LogDir, req.IDs)
		res, err = map[string]int{"deleted": n}, e
	case "deploy-probe":
		res, err = f.m.Probe(ctx, req.MachineID, req.Dir, "root", req.Script, req.Env, req.Timeout)
	default:
		return fmt.Errorf("unknown action %s", action)
	}
	if err != nil {
		return err
	}
	if out != nil {
		raw, _ := json.Marshal(res)
		return json.Unmarshal(raw, out)
	}
	return nil
}

type deployEnv struct {
	s      *Server
	mem    *appdb.Memory
	ts     *httptest.Server
	owner  string
	guest  *localDeployGuest
	cid    string
	ct     string
	ct2    string
	logDir string
}

func newDeployEnv(t *testing.T) *deployEnv {
	t.Helper()
	s, mem, token := testServer(t)
	s.Docker = &fakeDocker{}
	g := &localDeployGuest{}
	s.Deployer = &fakeDeployer{m: &deploy.Manager{Guest: g, KillGrace: time.Second}}
	ctx := context.Background()
	cluster, _ := mem.GetCluster(ctx)
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close)
	owner := claimAdmin(t, ts, token)
	_ = mem.UpsertFeature(ctx, appdb.Feature{ClusterID: cluster.ID, ID: features.IDDocker, Enabled: true})
	e := &deployEnv{s: s, mem: mem, ts: ts, owner: owner, guest: g, cid: cluster.ID, ct: uuid.NewString(), ct2: uuid.NewString()}
	_ = mem.CreateWorkload(ctx, appdb.Workload{ID: e.ct, ClusterID: cluster.ID, Name: "apps", Kind: "system-container", Status: "running"})
	_ = mem.CreateWorkload(ctx, appdb.Workload{ID: e.ct2, ClusterID: cluster.ID, Name: "web", Kind: "system-container", Status: "running"})
	e.logDir = filepath.Join(t.TempDir(), "deploy-logs")
	st := appdb.DefaultDeploySettings(cluster.ID)
	st.LogDir = e.logDir
	_ = mem.PutDeploySettings(ctx, *st)
	return e
}

func (e *deployEnv) call(t *testing.T, cookie, method, path, body string) (int, map[string]any) {
	t.Helper()
	return jsonCall(t, e.ts, cookie, method, path, body)
}

func (e *deployEnv) project(t *testing.T, ct, name, dir string, cmds ...string) string {
	t.Helper()
	c := []map[string]any{}
	for _, x := range cmds {
		c = append(c, map[string]any{"run": x})
	}
	cfg := map[string]any{"commands": c, "detect": false}
	if len(cmds) == 0 {
		cfg = nil
	}
	body, _ := json.Marshal(map[string]any{"workload_id": ct, "working_dir": dir, "name": name, "config": cfg})
	code, res := e.call(t, e.owner, "POST", "/api/v1/docker/deploy/projects", string(body))
	if code != http.StatusCreated {
		t.Fatalf("create project %d %v", code, res)
	}
	return res["id"].(string)
}

// settle runs the worker until the deployment finishes.
func (e *deployEnv) settle(t *testing.T, id string) map[string]any {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		e.s.tickDeployments(context.Background())
		_, d := e.call(t, e.owner, "GET", "/api/v1/docker/deployments/"+id, "")
		if s, _ := d["status"].(string); s != "" && !appdb.DeployActive(s) {
			return d
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("the deployment did not finish")
	return nil
}

func (e *deployEnv) logOf(t *testing.T, id string) string {
	t.Helper()
	_, lg := e.call(t, e.owner, "GET", "/api/v1/docker/deployments/"+id+"/log", "")
	s, _ := lg["data"].(string)
	return s
}

func TestDeployProjectDefaultsAndPermissions(t *testing.T) {
	e := newDeployEnv(t)
	id := e.project(t, e.ct, "", "/srv/shop/")
	_, p := e.call(t, e.owner, "GET", "/api/v1/docker/deploy/projects/"+id, "")
	cfg := p["config"].(map[string]any)
	cmds := cfg["commands"].([]any)
	if p["name"] != "shop" || p["working_dir"] != "/srv/shop" || len(cmds) != 1 || cmds[0].(map[string]any)["run"] != appdb.DefaultDeployCommand {
		t.Fatalf("defaults %v", p)
	}
	operator := loginRole(t, e.ts, e.mem, "deployop", rbac.Admin)
	viewer := loginRole(t, e.ts, e.mem, "deployview", rbac.User)
	if code, _ := e.call(t, operator, "PUT", "/api/v1/docker/deploy/projects/"+id, `{"config":{"commands":[{"run":"rm -rf /"}]}}`); code != http.StatusForbidden {
		t.Fatalf("operator changed commands: %d", code)
	}
	if code, _ := e.call(t, operator, "POST", "/api/v1/docker/deploy/projects", `{"workload_id":"`+e.ct+`","working_dir":"/x"}`); code != http.StatusForbidden {
		t.Fatalf("operator created a project: %d", code)
	}
	if code, _ := e.call(t, operator, "PUT", "/api/v1/docker/deploy/settings", `{}`); code != http.StatusForbidden {
		t.Fatalf("operator changed settings: %d", code)
	}
	if code, _ := e.call(t, viewer, "POST", "/api/v1/docker/deploy/projects/"+id+"/deploy", ""); code != http.StatusForbidden {
		t.Fatalf("viewer deployed: %d", code)
	}
	if code, _ := e.call(t, viewer, "GET", "/api/v1/docker/deploy/projects", ""); code != http.StatusOK {
		t.Fatalf("viewer cannot list: %d", code)
	}
	if code, _ := e.call(t, operator, "POST", "/api/v1/docker/deploy/projects/"+id+"/deploy", ""); code != http.StatusAccepted {
		t.Fatalf("operator cannot deploy: %d", code)
	}
	if code, _ := e.call(t, e.owner, "POST", "/api/v1/docker/deploy/projects", `{"workload_id":"`+e.ct+`","working_dir":"/srv/shop"}`); code != http.StatusConflict {
		t.Fatalf("duplicate project: %d", code)
	}
	if code, _ := e.call(t, e.owner, "POST", "/api/v1/docker/deploy/projects", `{"workload_id":"`+e.ct+`","working_dir":"/"}`); code != http.StatusBadRequest {
		t.Fatalf("root folder: %d", code)
	}
}

func TestDeploySucceedsWithLogsHistoryAndSecretEnv(t *testing.T) {
	e := newDeployEnv(t)
	dir := t.TempDir()
	id := e.project(t, e.ct, "shop", dir, "echo pulling in $(pwd)", `echo "key=$API_KEY"`)
	if code, res := e.call(t, e.owner, "PUT", "/api/v1/docker/deploy/projects/"+id, `{"env":{"API_KEY":"hunter2"}}`); code != http.StatusOK {
		t.Fatalf("env %d %v", code, res)
	}
	_, p := e.call(t, e.owner, "GET", "/api/v1/docker/deploy/projects/"+id, "")
	raw, _ := json.Marshal(p)
	if strings.Contains(string(raw), "hunter2") || !strings.Contains(string(raw), "API_KEY") {
		t.Fatalf("env value leaked or name missing: %s", raw)
	}
	code, d := e.call(t, e.owner, "POST", "/api/v1/docker/deploy/projects/"+id+"/deploy", "")
	if code != http.StatusAccepted || d["status"] != appdb.DeployQueued {
		t.Fatalf("deploy %d %v", code, d)
	}
	if code, _ := e.call(t, e.owner, "POST", "/api/v1/docker/deploy/projects/"+id+"/deploy", ""); code != http.StatusConflict {
		t.Fatalf("second deploy while queued: %d", code)
	}
	done := e.settle(t, d["id"].(string))
	if done["status"] != appdb.DeploySucceeded || done["current_step"].(float64) != 2 || done["actor"] != "admin" {
		t.Fatalf("result %v", done)
	}
	log := e.logOf(t, d["id"].(string))
	if !strings.Contains(log, "pulling in "+dir) || !strings.Contains(log, "key=hunter2") || !strings.Contains(log, "succeeded") {
		t.Fatalf("log %q", log)
	}
	_, hist := e.call(t, e.owner, "GET", "/api/v1/docker/deploy/projects/"+id+"/deployments", "")
	if n := len(hist["deployments"].([]any)); n != 1 {
		t.Fatalf("history %d", n)
	}
	_, p = e.call(t, e.owner, "GET", "/api/v1/docker/deploy/projects/"+id, "")
	if p["state"].(map[string]any)["last_deploy_status"] != appdb.DeploySucceeded {
		t.Fatalf("project state %v", p["state"])
	}
	audit, _ := e.mem.ListAuditEvents(context.Background(), e.cid, 100)
	seen := map[string]bool{}
	for _, a := range audit {
		seen[a.Action] = true
	}
	for _, want := range []string{"docker.deploy.project.create", "docker.deploy.start", "docker.deploy.run", "docker.deploy.finish"} {
		if !seen[want] {
			t.Errorf("no %s audit", want)
		}
	}
}

// A failed command fails the deployment with its exit code and full log.
// Nothing is reverted.
func TestDeployFailureKeepsTheLogAndRevertsNothing(t *testing.T) {
	e := newDeployEnv(t)
	dir := t.TempDir()
	id := e.project(t, e.ct, "api", dir, "echo v2 > version", "seq 1 5000; echo build broke >&2; exit 3", "echo unreachable")
	_, d := e.call(t, e.owner, "POST", "/api/v1/docker/deploy/projects/"+id+"/deploy", "")
	done := e.settle(t, d["id"].(string))
	if done["status"] != appdb.DeployFailed || done["exit_code"].(float64) != 3 || done["failed_step"].(float64) != 2 {
		t.Fatalf("result %v", done)
	}
	log := e.logOf(t, d["id"].(string))
	if !strings.Contains(log, "\n5000\n") || !strings.Contains(log, "build broke") || strings.Contains(log, "unreachable") {
		t.Fatalf("log is not complete: %d bytes", len(log))
	}
	out, _ := exec.Command("cat", filepath.Join(dir, "version")).Output()
	if strings.TrimSpace(string(out)) != "v2" {
		t.Fatal("the first command's change was reverted")
	}
	res, err := e.ts.Client().Do(func() *http.Request {
		req, _ := http.NewRequest("GET", e.ts.URL+"/api/v1/docker/deployments/"+d["id"].(string)+"/log/download", nil)
		req.AddCookie(&http.Cookie{Name: sessionCookie, Value: e.owner})
		return req
	}())
	if err != nil {
		t.Fatal(err)
	}
	full, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if string(full) != log && !strings.HasPrefix(string(full), log) {
		t.Fatal("the downloaded log differs")
	}
}

func TestDeployLimitsAndBackupWait(t *testing.T) {
	e := newDeployEnv(t)
	ctx := context.Background()
	st := e.s.deploySettings(ctx, e.cid)
	st.MaxConcurrent = 1
	_ = e.mem.PutDeploySettings(ctx, st)
	a := e.project(t, e.ct, "a", t.TempDir(), "sleep 1")
	b := e.project(t, e.ct2, "b", t.TempDir(), "true")
	_, da := e.call(t, e.owner, "POST", "/api/v1/docker/deploy/projects/"+a+"/deploy", "")
	_, db := e.call(t, e.owner, "POST", "/api/v1/docker/deploy/projects/"+b+"/deploy", "")
	e.s.tickDeployments(ctx)
	_, gotB := e.call(t, e.owner, "GET", "/api/v1/docker/deployments/"+db["id"].(string), "")
	if gotB["status"] != appdb.DeployWaiting || !strings.Contains(gotB["waiting_for"].(string), "free slot") {
		t.Fatalf("b %v", gotB)
	}
	e.settle(t, da["id"].(string))
	if res := e.settle(t, db["id"].(string)); res["status"] != appdb.DeploySucceeded {
		t.Fatalf("b %v", res)
	}

	// A backup of the container holds the deployment until Deploy anyway.
	run := appdb.BackupRun{ID: uuid.NewString(), ClusterID: e.cid, WorkloadID: e.ct2, Status: appdb.BackupRunning, StartedAt: time.Now()}
	_ = e.mem.CreateBackupRun(ctx, run)
	_, dc := e.call(t, e.owner, "POST", "/api/v1/docker/deploy/projects/"+b+"/deploy", "")
	e.s.tickDeployments(ctx)
	_, gotC := e.call(t, e.owner, "GET", "/api/v1/docker/deployments/"+dc["id"].(string), "")
	if gotC["status"] != appdb.DeployWaiting || !strings.Contains(gotC["waiting_for"].(string), "backup") {
		t.Fatalf("c %v", gotC)
	}
	operator := loginRole(t, e.ts, e.mem, "deployop2", rbac.Admin)
	if code, _ := e.call(t, operator, "POST", "/api/v1/docker/deployments/"+dc["id"].(string)+"/force", ""); code != http.StatusAccepted {
		t.Fatalf("force %d", code)
	}
	if res := e.settle(t, dc["id"].(string)); res["status"] != appdb.DeploySucceeded {
		t.Fatalf("c %v", res)
	}
}

func TestDeployOnePerContainer(t *testing.T) {
	e := newDeployEnv(t)
	ctx := context.Background()
	a := e.project(t, e.ct, "a", t.TempDir(), "sleep 1")
	b := e.project(t, e.ct, "b", t.TempDir(), "true")
	_, da := e.call(t, e.owner, "POST", "/api/v1/docker/deploy/projects/"+a+"/deploy", "")
	_, db := e.call(t, e.owner, "POST", "/api/v1/docker/deploy/projects/"+b+"/deploy", "")
	e.s.tickDeployments(ctx)
	_, gotB := e.call(t, e.owner, "GET", "/api/v1/docker/deployments/"+db["id"].(string), "")
	if gotB["status"] != appdb.DeployWaiting || !strings.Contains(gotB["waiting_for"].(string), "this container") {
		t.Fatalf("b %v", gotB)
	}
	e.settle(t, da["id"].(string))
	e.settle(t, db["id"].(string))
}

func TestDeployCancelQueuedAndRunning(t *testing.T) {
	e := newDeployEnv(t)
	ctx := context.Background()
	operator := loginRole(t, e.ts, e.mem, "deployop3", rbac.Admin)
	a := e.project(t, e.ct, "a", t.TempDir(), "sleep 300")
	_, da := e.call(t, e.owner, "POST", "/api/v1/docker/deploy/projects/"+a+"/deploy", "")
	if code, _ := e.call(t, operator, "POST", "/api/v1/docker/deployments/"+da["id"].(string)+"/cancel", ""); code != http.StatusAccepted {
		t.Fatalf("cancel queued %d", code)
	}
	_, got := e.call(t, e.owner, "GET", "/api/v1/docker/deployments/"+da["id"].(string), "")
	if got["status"] != appdb.DeployCancelled {
		t.Fatalf("queued cancel %v", got)
	}
	_, db := e.call(t, e.owner, "POST", "/api/v1/docker/deploy/projects/"+a+"/deploy", "")
	e.s.tickDeployments(ctx)
	deadline := time.Now().Add(10 * time.Second)
	for {
		_, got = e.call(t, e.owner, "GET", "/api/v1/docker/deployments/"+db["id"].(string), "")
		if got["current_step"].(float64) == 1 || time.Now().After(deadline) {
			break
		}
		e.s.tickDeployments(ctx)
		time.Sleep(50 * time.Millisecond)
	}
	if code, _ := e.call(t, operator, "POST", "/api/v1/docker/deployments/"+db["id"].(string)+"/cancel", ""); code != http.StatusAccepted {
		t.Fatalf("cancel running %d", code)
	}
	res := e.settle(t, db["id"].(string))
	if res["status"] != appdb.DeployCancelled || !strings.Contains(res["error"].(string), "deployop3") {
		t.Fatalf("running cancel %v", res)
	}
}

func TestDeployRefusesStoppedContainersAndHost(t *testing.T) {
	e := newDeployEnv(t)
	ctx := context.Background()
	stopped := uuid.NewString()
	_ = e.mem.CreateWorkload(ctx, appdb.Workload{ID: stopped, ClusterID: e.cid, Name: "off", Kind: "system-container", Status: "stopped"})
	vm := uuid.NewString()
	_ = e.mem.CreateWorkload(ctx, appdb.Workload{ID: vm, ClusterID: e.cid, Name: "vm", Kind: "vm"})
	if code, _ := e.call(t, e.owner, "POST", "/api/v1/docker/deploy/projects", `{"workload_id":"`+vm+`","working_dir":"/srv"}`); code != http.StatusBadRequest {
		t.Fatalf("vm project %d", code)
	}
	id := e.project(t, stopped, "x", "/srv/x", "true")
	_, d := e.call(t, e.owner, "POST", "/api/v1/docker/deploy/projects/"+id+"/deploy", "")
	res := e.settle(t, d["id"].(string))
	if res["status"] != appdb.DeployFailed || !strings.Contains(res["error"].(string), "stopped") {
		t.Fatalf("stopped %v", res)
	}
}

func TestDeployRetentionKeepsNewestAndActive(t *testing.T) {
	now := time.Now()
	mk := func(project, status string, age time.Duration, size int64) appdb.Deployment {
		return appdb.Deployment{ID: uuid.NewString(), ProjectID: project, Status: status, QueuedAt: now.Add(-age), Detail: appdb.DeploymentDetail{LogBytes: size}}
	}
	deps := []appdb.Deployment{ // newest first
		mk("a", appdb.DeployRunning, time.Minute, 10),
		mk("a", appdb.DeployFailed, time.Hour, 10),
		mk("a", appdb.DeploySucceeded, 2*time.Hour, 10),
		mk("a", appdb.DeploySucceeded, 40*24*time.Hour, 10),
		mk("b", appdb.DeployFailed, 50*24*time.Hour, 10),
	}
	st := appdb.DeploySettings{RetentionDays: 30, KeepPerProject: 50}
	drop := expiredDeployments(deps, st, now)
	if len(drop) != 1 || drop[0].ID != deps[3].ID {
		t.Fatalf("age: %+v", drop)
	}
	st = appdb.DeploySettings{KeepPerProject: 1}
	if drop = expiredDeployments(deps, st, now); len(drop) != 2 {
		t.Fatalf("count: %d", len(drop))
	}
	st = appdb.DeploySettings{MaxLogGB: 15.0 / (1 << 30)}
	if drop = expiredDeployments(deps, st, now); len(drop) != 2 {
		t.Fatalf("size: %d", len(drop))
	}
}

func TestDeploymentLogCutKeepsCharactersWhole(t *testing.T) {
	b := []byte("ok é")
	if got := string(utf8Prefix(b[:len(b)-1])); got != "ok " {
		t.Fatalf("%q", got)
	}
	if got := string(utf8Prefix(b)); got != "ok é" {
		t.Fatalf("%q", got)
	}
}

// gitRepo makes a bare remote and a clone of it to deploy from.
func gitRepo(t *testing.T) (remote, work string, commit func(msg string) string) {
	t.Helper()
	base := t.TempDir()
	remote, work = filepath.Join(base, "remote.git"), filepath.Join(base, "work")
	seed := filepath.Join(base, "seed")
	run := func(dir string, args ...string) string {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=Dev", "GIT_AUTHOR_EMAIL=dev@example.com", "GIT_COMMITTER_NAME=Dev", "GIT_COMMITTER_EMAIL=dev@example.com")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	run(base, "init", "-q", "--bare", "-b", "main", remote)
	run(base, "init", "-q", "-b", "main", seed)
	run(seed, "remote", "add", "origin", remote)
	n := 0
	commit = func(msg string) string {
		n++
		_ = os.WriteFile(filepath.Join(seed, "file.txt"), []byte(fmt.Sprint(n)), 0o644)
		run(seed, "add", ".")
		run(seed, "commit", "-q", "-m", msg)
		run(seed, "push", "-q", "origin", "main")
		return run(seed, "rev-parse", "HEAD")
	}
	commit("first")
	run(base, "clone", "-q", "-b", "main", remote, work)
	return remote, work, commit
}

func (e *deployEnv) gitProject(t *testing.T, work, provider string) string {
	t.Helper()
	body, _ := json.Marshal(map[string]any{"workload_id": e.ct, "working_dir": work, "name": "shop", "config": map[string]any{
		"commands": []map[string]any{{"run": "git pull -q origin main"}}, "detect": true, "provider": provider,
	}})
	code, res := e.call(t, e.owner, "POST", "/api/v1/docker/deploy/projects", string(body))
	if code != http.StatusCreated {
		t.Fatalf("create %d %v", code, res)
	}
	return res["id"].(string)
}

func TestDeployDetectsNewCommitsAndClearsAfterDeploy(t *testing.T) {
	e := newDeployEnv(t)
	_, work, commit := gitRepo(t)
	id := e.gitProject(t, work, "git")
	code, p := e.call(t, e.owner, "POST", "/api/v1/docker/deploy/projects/"+id+"/check", "")
	st := p["state"].(map[string]any)
	if code != http.StatusOK || st["due"] == true || st["behind"].(float64) != 0 || st["check_error"] != nil || st["provider"] != "git" || st["branch"] != "main" {
		t.Fatalf("up to date: %d %v", code, st)
	}
	commit("second")
	head := commit("Fix the cart")
	_, p = e.call(t, e.owner, "POST", "/api/v1/docker/deploy/projects/"+id+"/check", "")
	st = p["state"].(map[string]any)
	if st["due"] != true || st["behind"].(float64) != 2 || st["remote_commit"] != head || st["remote_subject"] != "Fix the cart" || st["remote_author"] != "Dev" {
		t.Fatalf("behind: %v", st)
	}
	// Detection fetched but did not move the folder.
	out, _ := exec.Command("git", "-C", work, "rev-parse", "HEAD").Output()
	if strings.TrimSpace(string(out)) == head {
		t.Fatal("the check changed the working tree")
	}
	_, d := e.call(t, e.owner, "POST", "/api/v1/docker/deploy/projects/"+id+"/deploy", "")
	done := e.settle(t, d["id"].(string))
	if done["status"] != appdb.DeploySucceeded || done["commit_after"] != head || done["commit_before"] == head {
		t.Fatalf("deploy %v", done)
	}
	_, p = e.call(t, e.owner, "GET", "/api/v1/docker/deploy/projects/"+id, "")
	st = p["state"].(map[string]any)
	if st["due"] == true || st["local_commit"] != head {
		t.Fatalf("after deploy %v", st)
	}
}

func TestDeployDetectUsesFallbackRemoteAndReportsErrors(t *testing.T) {
	e := newDeployEnv(t)
	remote, work, commit := gitRepo(t)
	_ = exec.Command("git", "-C", work, "remote", "add", "mirror", remote).Run()
	_ = exec.Command("git", "-C", work, "remote", "set-url", "origin", filepath.Join(t.TempDir(), "missing.git")).Run()
	id := e.gitProject(t, work, "auto")
	_, p := e.call(t, e.owner, "POST", "/api/v1/docker/deploy/projects/"+id+"/check", "")
	if st := p["state"].(map[string]any); st["check_error"] == nil || !strings.Contains(st["check_error"].(string), "could not reach origin") {
		t.Fatalf("no fallback: %v", st)
	}
	body := `{"config":{"commands":[{"run":"true"}],"detect":true,"provider":"auto","fallback_remote":"mirror"}}`
	if code, res := e.call(t, e.owner, "PUT", "/api/v1/docker/deploy/projects/"+id, body); code != http.StatusOK {
		t.Fatalf("put %d %v", code, res)
	}
	head := commit("via mirror")
	_, p = e.call(t, e.owner, "POST", "/api/v1/docker/deploy/projects/"+id+"/check", "")
	st := p["state"].(map[string]any)
	if st["check_error"] != nil || st["remote"] != "mirror" || st["remote_commit"] != head || st["behind"].(float64) != 1 || !strings.Contains(fmt.Sprint(st["check_note"]), "used mirror") {
		t.Fatalf("fallback: %v", st)
	}

	plain := t.TempDir()
	bad := e.project(t, e.ct, "plain", plain, "true")
	if code, _ := e.call(t, e.owner, "PUT", "/api/v1/docker/deploy/projects/"+bad, `{"config":{"commands":[{"run":"true"}],"detect":true}}`); code != http.StatusOK {
		t.Fatal("enable detect")
	}
	_, p = e.call(t, e.owner, "POST", "/api/v1/docker/deploy/projects/"+bad+"/check", "")
	if st := p["state"].(map[string]any); !strings.Contains(fmt.Sprint(st["check_error"]), "not a Git repository") {
		t.Fatalf("plain folder: %v", st)
	}
}

func TestDeployDetectRoundSkipsUntilDue(t *testing.T) {
	e := newDeployEnv(t)
	_, work, commit := gitRepo(t)
	id := e.gitProject(t, work, "git")
	ctx := context.Background()
	if n := e.s.detectDue(ctx, e.cid, false); n != 1 {
		t.Fatalf("first round checked %d", n)
	}
	commit("later")
	if n := e.s.detectDue(ctx, e.cid, false); n != 0 {
		t.Fatalf("checked again before the interval: %d", n)
	}
	if n := e.s.detectDue(ctx, e.cid, true); n != 1 {
		t.Fatalf("check all: %d", n)
	}
	p, _ := e.mem.GetDeployProject(ctx, e.cid, id)
	if !p.State.Due || p.State.Behind != 1 || p.State.NextCheckAt == nil || p.State.NextCheckAt.Sub(time.Now()) < 9*time.Minute {
		t.Fatalf("state %+v", p.State)
	}
}

func TestBulkDeployDueAndChosen(t *testing.T) {
	e := newDeployEnv(t)
	ctx := context.Background()
	a := e.project(t, e.ct, "a", t.TempDir(), "true")
	b := e.project(t, e.ct2, "b", t.TempDir(), "true")
	c := e.project(t, e.ct2, "c", t.TempDir(), "sleep 300")
	for _, id := range []string{a, b} {
		p, _ := e.mem.GetDeployProject(ctx, e.cid, id)
		p.State.Due = true
		_ = e.mem.UpdateDeployProjectState(ctx, e.cid, id, p.State)
	}
	operator := loginRole(t, e.ts, e.mem, "bulkop", rbac.Admin)
	code, res := e.call(t, operator, "POST", "/api/v1/docker/deploy/bulk", `{"due":true}`)
	if code != http.StatusAccepted || len(res["queued"].([]any)) != 2 || res["batch_id"] == "" {
		t.Fatalf("due %d %v", code, res)
	}
	_, res2 := e.call(t, operator, "POST", "/api/v1/docker/deploy/bulk", `{"project_ids":["`+a+`","`+c+`","nope"]}`)
	skipped := res2["skipped"].([]any)
	if len(res2["queued"].([]any)) != 1 || len(skipped) != 2 {
		t.Fatalf("chosen %v", res2)
	}
	for _, q := range res["queued"].([]any) {
		e.settle(t, q.(map[string]any)["id"].(string))
	}
	cd := res2["queued"].([]any)[0].(map[string]any)
	if cd["project_id"] != c {
		t.Fatalf("queued %v", cd)
	}
	_, _ = e.call(t, operator, "POST", "/api/v1/docker/deployments/"+cd["id"].(string)+"/cancel", "")
	e.settle(t, cd["id"].(string))
	if code, _ := e.call(t, operator, "POST", "/api/v1/docker/deploy/bulk", `{}`); code != http.StatusBadRequest {
		t.Fatalf("empty bulk %d", code)
	}
}

type hookSink struct {
	mu   sync.Mutex
	got  map[string][]map[string]any
	srv  *httptest.Server
	path string
}

// newHookSink receives notification webhooks for hooks.example. The name
// resolves to a public address; the client dials the test server.
func newHookSink(t *testing.T, e *deployEnv) *hookSink {
	t.Helper()
	h := &hookSink{got: map[string][]map[string]any{}}
	h.srv = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var m map[string]any
		_ = json.NewDecoder(r.Body).Decode(&m)
		h.mu.Lock()
		h.got[r.URL.Path] = append(h.got[r.URL.Path], m)
		h.mu.Unlock()
	}))
	t.Cleanup(h.srv.Close)
	lookupWebhookIPs = func(string) ([]net.IP, error) { return []net.IP{net.ParseIP("203.0.113.9")}, nil }
	t.Cleanup(func() { lookupWebhookIPs = net.LookupIP })
	tr := h.srv.Client().Transport.(*http.Transport).Clone()
	tr.DialContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, h.srv.Listener.Addr().String())
	}
	tr.TLSClientConfig.InsecureSkipVerify = true
	e.s.HTTPClient = &http.Client{Transport: tr}
	return h
}

func (h *hookSink) channel(t *testing.T, e *deployEnv, name string) string {
	t.Helper()
	id := uuid.NewString()
	if err := e.mem.CreateNotificationChannel(context.Background(), appdb.NotificationChannel{
		ID: id, ClusterID: e.cid, Name: name, Kind: appdb.NotifyWebhook, Status: appdb.NotifyConfigured, CreatedAt: time.Now(),
	}, "https://hooks.example/"+name, ""); err != nil {
		t.Fatal(err)
	}
	return id
}

func (h *hookSink) wait(t *testing.T, path string, n int) []map[string]any {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		h.mu.Lock()
		got := append([]map[string]any(nil), h.got[path]...)
		h.mu.Unlock()
		if len(got) >= n {
			return got
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("%s: wanted %d messages", path, n)
	return nil
}

func TestDeployNotifiesTheProjectsChannels(t *testing.T) {
	e := newDeployEnv(t)
	sink := newHookSink(t, e)
	sink.channel(t, e, "all")
	ops := sink.channel(t, e, "ops")
	id := e.project(t, e.ct, "api", t.TempDir(), "echo compiling; echo 'error: missing semicolon' >&2; exit 2")
	_, d := e.call(t, e.owner, "POST", "/api/v1/docker/deploy/projects/"+id+"/deploy", "")
	e.settle(t, d["id"].(string))
	msgs := sink.wait(t, "/all", 1)
	m := msgs[0]
	if m["event"] != "deploy.failed" || m["exit_code"].(float64) != 2 || !strings.Contains(m["failed_command"].(string), "missing semicolon") ||
		!strings.Contains(m["log_tail"].(string), "error: missing semicolon") || !strings.Contains(m["text"].(string), "api in apps") || m["content"] != m["text"] {
		t.Fatalf("message %v", m)
	}
	// Only the chosen channel, and success too.
	body := fmt.Sprintf(`{"config":{"commands":[{"run":"true"}],"notify_channels":[%q],"notify_events":["deploy.succeeded"]}}`, ops)
	if code, res := e.call(t, e.owner, "PUT", "/api/v1/docker/deploy/projects/"+id, body); code != http.StatusOK {
		t.Fatalf("put %d %v", code, res)
	}
	_, d = e.call(t, e.owner, "POST", "/api/v1/docker/deploy/projects/"+id+"/deploy", "")
	e.settle(t, d["id"].(string))
	// With no channels chosen the failure went to both; the success only to ops.
	if got := sink.wait(t, "/ops", 2); got[1]["event"] != "deploy.succeeded" {
		t.Fatalf("ops %v", got)
	}
	time.Sleep(200 * time.Millisecond)
	if n := len(sink.wait(t, "/all", 1)); n != 1 {
		t.Fatalf("the other channel got %d messages", n)
	}
	if code, _ := e.call(t, e.owner, "PUT", "/api/v1/docker/deploy/projects/"+id, `{"config":{"commands":[{"run":"true"}],"notify_channels":["missing"]}}`); code != http.StatusBadRequest {
		t.Fatalf("unknown channel accepted: %d", code)
	}
}

func TestBulkDeployReportsOnceWhenAllFinish(t *testing.T) {
	e := newDeployEnv(t)
	sink := newHookSink(t, e)
	sink.channel(t, e, "all")
	a := e.project(t, e.ct, "a", t.TempDir(), "true")
	b := e.project(t, e.ct2, "b", t.TempDir(), "exit 1")
	_, res := e.call(t, e.owner, "POST", "/api/v1/docker/deploy/bulk", `{"project_ids":["`+a+`","`+b+`"]}`)
	for _, q := range res["queued"].([]any) {
		e.settle(t, q.(map[string]any)["id"].(string))
	}
	var bulk []map[string]any
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && len(bulk) == 0 {
		for _, m := range sink.wait(t, "/all", 1) {
			if m["event"] == "bulk.finished" {
				bulk = append(bulk, m)
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	if len(bulk) != 1 || !strings.Contains(bulk[0]["text"].(string), "1 succeeded, 1 failed (b)") {
		t.Fatalf("bulk %v", bulk)
	}
}

func hubSign(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

func TestDeployWebhookChecksAndDeploysOnlyWhenAutomatic(t *testing.T) {
	e := newDeployEnv(t)
	_, work, commit := gitRepo(t)
	id := e.gitProject(t, work, "git")
	operator := loginRole(t, e.ts, e.mem, "hookop", rbac.Admin)
	if code, _ := e.call(t, operator, "POST", "/api/v1/docker/deploy/projects/"+id+"/webhook", `{"enabled":true}`); code != http.StatusForbidden {
		t.Fatalf("operator made a webhook: %d", code)
	}
	post := func(body []byte, headers map[string]string) int {
		req, _ := http.NewRequest("POST", e.ts.URL+"/api/v1/hooks/deploy/"+id, bytes.NewReader(body))
		for k, v := range headers {
			req.Header.Set(k, v)
		}
		res, err := e.ts.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		return res.StatusCode
	}
	push := []byte(`{"ref":"refs/heads/main","after":"x"}`)
	if code := post(push, nil); code != http.StatusNotFound {
		t.Fatalf("webhook off: %d", code)
	}
	_, hook := e.call(t, e.owner, "POST", "/api/v1/docker/deploy/projects/"+id+"/webhook", `{"enabled":true}`)
	secret := hook["secret"].(string)
	if len(secret) != 64 || hook["path"] != "/api/v1/hooks/deploy/"+id {
		t.Fatalf("hook %v", hook)
	}
	_, p := e.call(t, e.owner, "GET", "/api/v1/docker/deploy/projects/"+id, "")
	if strings.Contains(fmt.Sprint(p), secret) {
		t.Fatal("the secret is shown again")
	}
	if code := post(push, map[string]string{"X-Hub-Signature-256": hubSign("wrong", push)}); code != http.StatusUnauthorized {
		t.Fatalf("bad signature: %d", code)
	}
	if code := post([]byte(`{}`), map[string]string{"X-GitHub-Event": "ping", "X-Hub-Signature-256": hubSign(secret, []byte(`{}`))}); code != http.StatusOK {
		t.Fatalf("ping: %d", code)
	}
	head := commit("pushed")
	other := []byte(`{"ref":"refs/heads/dev"}`)
	if code := post(other, map[string]string{"X-Gitea-Signature": strings.TrimPrefix(hubSign(secret, other), "sha256=")}); code != http.StatusAccepted {
		t.Fatalf("other branch: %d", code)
	}
	if code := post(push, map[string]string{"X-Hub-Signature-256": hubSign(secret, push)}); code != http.StatusAccepted {
		t.Fatalf("push: %d", code)
	}
	waitState := func(cond func(appdb.DeployProjectState) bool) appdb.DeployProjectState {
		deadline := time.Now().Add(15 * time.Second)
		for time.Now().Before(deadline) {
			p, _ := e.mem.GetDeployProject(context.Background(), e.cid, id)
			if cond(p.State) {
				return p.State
			}
			time.Sleep(50 * time.Millisecond)
		}
		t.Fatal("the webhook did not check the project")
		return appdb.DeployProjectState{}
	}
	waitState(func(st appdb.DeployProjectState) bool { return st.Due && st.RemoteCommit == head })
	deps, _ := e.mem.ListDeployments(context.Background(), e.cid, appdb.DeploymentFilter{ProjectID: id})
	if len(deps) != 0 {
		t.Fatal("a push deployed a project that does not deploy automatically")
	}

	if code, res := e.call(t, e.owner, "PUT", "/api/v1/docker/deploy/projects/"+id, `{"config":{"commands":[{"run":"git pull -q origin main"}],"detect":true,"provider":"git","auto_deploy":true}}`); code != http.StatusOK {
		t.Fatalf("auto %d %v", code, res)
	}
	head2 := commit("pushed again")
	if code := post(push, map[string]string{"Authorization": "Bearer " + secret}); code != http.StatusAccepted {
		t.Fatalf("bearer push: %d", code)
	}
	waitState(func(st appdb.DeployProjectState) bool { return st.AutoCommit == head2 })
	deps, _ = e.mem.ListDeployments(context.Background(), e.cid, appdb.DeploymentFilter{ProjectID: id})
	if len(deps) != 1 || deps[0].Trigger != "auto" {
		t.Fatalf("auto deployments %+v", deps)
	}
	done := e.settle(t, deps[0].ID)
	if done["status"] != appdb.DeploySucceeded || done["commit_after"] != head2 {
		t.Fatalf("auto deploy %v", done)
	}
	// The same commit is not deployed twice.
	p2, _ := e.mem.GetDeployProject(context.Background(), e.cid, id)
	e.s.maybeAutoDeploy(context.Background(), *p2)
	if deps, _ = e.mem.ListDeployments(context.Background(), e.cid, appdb.DeploymentFilter{ProjectID: id}); len(deps) != 1 {
		t.Fatalf("deployed again: %d", len(deps))
	}
	if code, _ := e.call(t, e.owner, "POST", "/api/v1/docker/deploy/projects/"+id+"/webhook", `{"enabled":false}`); code != http.StatusOK {
		t.Fatal("disable")
	}
	if code := post(push, map[string]string{"Authorization": "Bearer " + secret}); code != http.StatusNotFound {
		t.Fatalf("disabled hook: %d", code)
	}
}

func TestAutoDeployWindow(t *testing.T) {
	at := func(h, m int) time.Time { return time.Date(2026, 10, 18, h, m, 0, 0, time.Local) }
	cases := []struct {
		w    string
		t    time.Time
		want bool
	}{
		{"", at(13, 0), true},
		{"02:00-05:00", at(3, 0), true},
		{"02:00-05:00", at(5, 0), false},
		{"22:00-04:00", at(23, 30), true},
		{"22:00-04:00", at(1, 0), true},
		{"22:00-04:00", at(12, 0), false},
	}
	for _, c := range cases {
		if got := inAutoWindow(c.w, c.t); got != c.want {
			t.Errorf("%q at %s: %v", c.w, c.t.Format("15:04"), got)
		}
	}
}

func TestUpdateAvailableNotifiesOncePerCommit(t *testing.T) {
	e := newDeployEnv(t)
	sink := newHookSink(t, e)
	sink.channel(t, e, "all")
	_, work, commit := gitRepo(t)
	id := e.gitProject(t, work, "git")
	if code, _ := e.call(t, e.owner, "PUT", "/api/v1/docker/deploy/projects/"+id, `{"config":{"commands":[{"run":"true"}],"detect":true,"provider":"git","notify_events":["update.available"]}}`); code != http.StatusOK {
		t.Fatal("put")
	}
	commit("new feature")
	for i := 0; i < 2; i++ {
		e.call(t, e.owner, "POST", "/api/v1/docker/deploy/projects/"+id+"/check", "")
	}
	got := sink.wait(t, "/all", 1)
	time.Sleep(200 * time.Millisecond)
	if got = sink.wait(t, "/all", 1); len(got) != 1 || got[0]["event"] != "update.available" || !strings.Contains(got[0]["text"].(string), "1 new commit(s): new feature") {
		t.Fatalf("messages %v", got)
	}
}
