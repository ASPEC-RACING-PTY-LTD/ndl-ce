package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/no-dal/ndl-ce/internal/appdb"
	"github.com/no-dal/ndl-ce/internal/codehold"
	"github.com/no-dal/ndl-ce/internal/docker"
	"github.com/no-dal/ndl-ce/internal/lxc"
	"github.com/no-dal/ndl-ce/internal/storage"
)

// fakeCodehold is a CodeHold server speaking the host controller protocol.
type fakeCodehold struct {
	mu       sync.Mutex
	srv      *httptest.Server
	token    string
	runners  []codehold.DesiredRunner
	reports  []codehold.SyncRequest
	offer    *codehold.CredentialOffer
	reject   bool
	enrolled int
}

func newFakeCodehold(t *testing.T) *fakeCodehold {
	f := &fakeCodehold{token: "chc_first"}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		body, _ := io.ReadAll(r.Body)
		switch r.URL.Path {
		case "/api/v1/actions/controller/enroll":
			var in map[string]string
			_ = json.Unmarshal(body, &in)
			if in["token"] != "chce_valid" {
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = w.Write([]byte(`{"error":"enrollment token is invalid","code":"enrollment_invalid"}`))
				return
			}
			f.enrolled++
			_, _ = w.Write([]byte(`{"controller_id":"ctl-1","controller_token":"` + f.token + `","name":"` + in["name"] + `"}`))
		case "/api/v1/actions/controller/sync":
			if f.reject || r.Header.Get("Authorization") != "Bearer "+f.token {
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = w.Write([]byte(`{"error":"credential revoked","code":"credential_revoked"}`))
				return
			}
			var req codehold.SyncRequest
			_ = json.Unmarshal(body, &req)
			f.reports = append(f.reports, req)
			out := codehold.SyncReply{Runners: f.runners, Credential: f.offer}
			if f.offer != nil {
				f.token, f.offer = f.offer.Token, nil
			}
			_ = json.NewEncoder(w).Encode(out)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(f.srv.Close)
	return f
}

// chDocker is the agent: it records CodeHold actions.
type chDocker struct {
	dockerUnavailable
	mu        sync.Mutex
	reqs      []docker.CodeholdRequest
	installed map[string]bool
	authFail  bool
}

func (d *chDocker) DockerCodehold(_ context.Context, req docker.CodeholdRequest) (docker.CodeholdResult, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.reqs = append(d.reqs, req)
	if d.installed == nil {
		d.installed = map[string]bool{}
	}
	switch req.Action {
	case docker.CodeholdDetect:
		return docker.CodeholdResult{Instances: []docker.CodeholdInstance{{MachineID: "host", MachineName: "Host", ContainerID: "srv", ContainerName: "codehold-codehold-1", HTTPPort: 8085, HasRunner: true}}}, nil
	case docker.CodeholdInstall:
		d.installed[req.MachineID] = true
		return docker.CodeholdResult{Installed: true, Active: true, Version: "codehold-runner 1.0"}, nil
	case docker.CodeholdStatus:
		return docker.CodeholdResult{Installed: d.installed[req.MachineID], Active: d.installed[req.MachineID], State: "active", AuthFailed: d.authFail}, nil
	}
	return docker.CodeholdResult{}, nil
}

func (d *chDocker) actions(kind string) []docker.CodeholdRequest {
	d.mu.Lock()
	defer d.mu.Unlock()
	var out []docker.CodeholdRequest
	for _, r := range d.reqs {
		if r.Action == kind {
			out = append(out, r)
		}
	}
	return out
}

type chEnv struct {
	t       *testing.T
	s       *Server
	mem     *appdb.Memory
	ts      *httptest.Server
	admin   string
	cluster string
	fake    *fakeCodehold
	dock    *chDocker
	wl      *fakeWorkloads
}

func newCHEnv(t *testing.T) *chEnv {
	s, mem, token := testServer(t)
	cl, _ := mem.GetCluster(context.Background())
	nodeID := uuid.NewString()
	_ = mem.UpsertNode(context.Background(), appdb.Node{ID: nodeID, ClusterID: cl.ID, Name: "homelab"})
	seedCompute(t, mem, cl.ID, nodeID)
	e := &chEnv{t: t, s: s, mem: mem, cluster: cl.ID, fake: newFakeCodehold(t), dock: &chDocker{}, wl: &fakeWorkloads{life: lxc.Result{Status: lxc.StatusRunning, Extras: []lxc.ExtraStatus{{ID: "docker", Installed: true, Version: "28.5"}}}}}
	s.Workloads = e.wl
	s.Docker = e.dock
	s.Storage = fakeStorage{vol: storage.CreateVolumeResult{Handle: storage.VolumeHandle{
		BackendType: storage.BackendDirectory, BackendRef: "volumes/container-root/x",
		Kind: storage.KindFilesystem, Class: storage.ClassContainerRoot, Format: storage.FormatDirectory,
	}}}
	s.CHKeyPath = filepath.Join(t.TempDir(), "codehold.key")
	chPollEvery = 5 * time.Millisecond
	e.ts = httptest.NewServer(s.Handler())
	t.Cleanup(e.ts.Close)
	e.admin = claimAdmin(t, e.ts, token)
	return e
}

func (e *chEnv) call(method, path, body string) (int, string) {
	e.t.Helper()
	res := authedJSON(e.t, e.ts, method, path, e.admin, body, nil)
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	return res.StatusCode, string(raw)
}

func (e *chEnv) connect() {
	e.t.Helper()
	if code, raw := e.call("POST", "/api/v1/features/codehold/enable", ""); code != 200 {
		e.t.Fatalf("enable %d %s", code, raw)
	}
	if code, raw := e.call("POST", "/api/v1/codehold/connect", `{"url":"`+e.fake.srv.URL+`","runner_url":"http://192.168.2.196:8085","enrollment_token":"chce_valid"}`); code != 200 {
		e.t.Fatalf("connect %d %s", code, raw)
	}
}

func (e *chEnv) syncOnce() codehold.SyncReply {
	e.t.Helper()
	ctx := context.Background()
	ctrl, _ := e.mem.GetCodeholdController(ctx, e.cluster)
	reply, err := e.s.chSync(ctx, ctrl, e.s.chSecret(ctx, e.cluster, chSecretToken), e.s.chObserve(ctx, e.cluster), false)
	if err != nil {
		e.t.Fatal(err)
	}
	e.s.chSynced(ctx, e.cluster, reply.Credential, e.s.chSecret(ctx, e.cluster, chSecretToken))
	e.s.chApply(ctx, e.cluster, reply.Runners)
	return reply
}

func (e *chEnv) waitSetup(id string) {
	e.t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		e.s.ch.mu.Lock()
		busy := e.s.ch.provisioning[id]
		e.s.ch.mu.Unlock()
		if !busy {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	e.t.Fatal("setup did not finish")
}

func (e *chEnv) runner(id string) appdb.CodeholdRunner {
	e.t.Helper()
	r, _ := e.s.chRunner(context.Background(), e.cluster, id)
	if r == nil {
		e.t.Fatalf("runner %s missing", id)
	}
	return *r
}

func TestCodeholdConnectSealsTheCredential(t *testing.T) {
	e := newCHEnv(t)
	if code, _ := e.call("POST", "/api/v1/codehold/connect", `{"url":"`+e.fake.srv.URL+`","enrollment_token":"chce_valid"}`); code != http.StatusNotFound {
		t.Fatalf("a disabled feature must 404: %d", code)
	}
	if code, _ := e.call("POST", "/api/v1/features/codehold/enable", ""); code != 200 {
		t.Fatal("enable")
	}
	if code, raw := e.call("POST", "/api/v1/codehold/connect", `{"url":"`+e.fake.srv.URL+`","enrollment_token":"chce_wrong"}`); code != http.StatusBadGateway || !strings.Contains(raw, "enrollment token is invalid") {
		t.Fatalf("bad token %d %s", code, raw)
	}
	if code, _ := e.call("POST", "/api/v1/codehold/connect", `{"url":"`+e.fake.srv.URL+`","enrollment_token":"nope"}`); code != http.StatusBadRequest {
		t.Fatal("tokens must look like chce_")
	}
	e.connect()
	sealed, _ := e.mem.GetCodeholdSecret(context.Background(), e.cluster, chSecretToken)
	if sealed == "" || strings.Contains(sealed, "chc_first") {
		t.Fatalf("credential not sealed: %q", sealed)
	}
	code, raw := e.call("GET", "/api/v1/codehold", "")
	if code != 200 || strings.Contains(raw, "chc_") || !strings.Contains(raw, `"controller_id":"ctl-1"`) || !strings.Contains(raw, `"cpus":2`) {
		t.Fatalf("status %d %s", code, raw)
	}
	// The local CodeHold is found, with an address on the local network.
	if !strings.Contains(raw, `"url":"http://127.0.0.1:8085"`) {
		t.Fatalf("local instance %s", raw)
	}
}

func TestCodeholdRunnerLifecycle(t *testing.T) {
	e := newCHEnv(t)
	e.connect()
	ctx := context.Background()
	e.fake.mu.Lock()
	e.fake.runners = []codehold.DesiredRunner{{ID: "r1", Name: "Build 1", State: "running", Spec: "s1",
		Limits: codehold.Limits{Concurrency: 1, CPUs: 2, MemoryMB: 2048}, RegistrationToken: "chreg_firsttoken"}}
	e.fake.mu.Unlock()

	e.syncOnce()
	e.waitSetup("r1")
	r := e.runner("r1")
	if r.WorkloadID == "" || r.Error != "" {
		t.Fatalf("runner %+v", r)
	}
	wl, _ := e.mem.GetWorkload(ctx, e.cluster, r.WorkloadID)
	if wl == nil || !strings.HasPrefix(wl.Name, "codehold-build-1-") || wl.CPUs != 2 || wl.MemoryBytes != 2<<30 {
		t.Fatalf("container %+v", wl)
	}
	// Docker runs inside, so the container needs nesting.
	if e.wl.lastSpec.Nesting == nil || !*e.wl.lastSpec.Nesting {
		t.Fatal("runner containers need nesting for Docker")
	}
	inst := e.dock.actions(docker.CodeholdInstall)
	if len(inst) != 1 || inst[0].Source.ContainerID != "srv" || inst[0].RunnerURL != "http://192.168.2.196:8085" {
		t.Fatalf("install %+v", inst)
	}

	// The next sync hands the token over, once.
	e.syncOnce()
	e.syncOnce()
	toks := e.dock.actions(docker.CodeholdToken)
	if len(toks) != 1 || toks[0].Token != "chreg_firsttoken" || toks[0].MachineID != r.WorkloadID {
		t.Fatalf("token hand-over %+v", toks)
	}
	e.fake.mu.Lock()
	last := e.fake.reports[len(e.fake.reports)-1]
	e.fake.mu.Unlock()
	if len(last.Runners) != 1 || last.Runners[0].State != "running" || last.CPUs <= 0 || last.Hostname != "homelab" {
		t.Fatalf("report %+v", last)
	}
	// Rejected agent credentials are reported so CodeHold issues a new token.
	e.dock.mu.Lock()
	e.dock.authFail = true
	e.dock.mu.Unlock()
	e.syncOnce()
	e.fake.mu.Lock()
	last = e.fake.reports[len(e.fake.reports)-1]
	e.fake.mu.Unlock()
	if !last.Runners[0].AuthFailed {
		t.Fatalf("auth failure not reported: %+v", last.Runners[0])
	}
	e.dock.mu.Lock()
	e.dock.authFail = false
	e.dock.mu.Unlock()

	// Credential rotation: the new one is used from the next sync on.
	e.fake.mu.Lock()
	e.fake.offer = &codehold.CredentialOffer{Token: "chc_second", ExpiresAt: time.Now().Add(30 * 24 * time.Hour)}
	e.fake.mu.Unlock()
	e.syncOnce()
	e.syncOnce()
	if got := e.s.chSecret(ctx, e.cluster, chSecretToken); got != "chc_second" {
		t.Fatalf("rotated credential not used: %q", got)
	}

	// Removed in CodeHold: the container is deleted and removal reported.
	e.fake.mu.Lock()
	e.fake.runners[0].State = "removed"
	e.fake.runners[0].RegistrationToken = ""
	e.fake.mu.Unlock()
	e.syncOnce()
	if gone, _ := e.mem.GetWorkload(ctx, e.cluster, r.WorkloadID); gone != nil {
		t.Fatal("container not deleted")
	}
	e.fake.mu.Lock()
	e.fake.runners = nil
	e.fake.mu.Unlock()
	e.syncOnce()
	e.fake.mu.Lock()
	last = e.fake.reports[len(e.fake.reports)-1]
	e.fake.mu.Unlock()
	if len(last.Runners) != 1 || last.Runners[0].State != "removed" {
		t.Fatalf("removal not reported: %+v", last.Runners)
	}
	e.syncOnce()
	if rr, _ := e.s.chRunner(ctx, e.cluster, "r1"); rr != nil {
		t.Fatal("a runner CodeHold forgot must be forgotten here too")
	}
}

func TestCodeholdRejectedCredentialStopsTheLoop(t *testing.T) {
	e := newCHEnv(t)
	e.connect()
	e.fake.mu.Lock()
	e.fake.reject = true
	e.fake.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	e.s.chLoop(ctx, e.cluster)
	ctrl, _ := e.mem.GetCodeholdController(context.Background(), e.cluster)
	if ctrl.Status != chRejected || !strings.Contains(ctrl.StatusReason, "enrollment token") {
		t.Fatalf("%+v", ctrl)
	}
}

func (e *chEnv) addRunnerContainer(id, name string) string {
	e.t.Helper()
	ctx := context.Background()
	node, _ := e.mem.GetNode(ctx, e.cluster)
	wid := uuid.NewString()
	if err := e.mem.CreateWorkload(ctx, appdb.Workload{ID: wid, ClusterID: e.cluster, NodeID: node.ID, OwnerNodeID: node.ID, DesiredNodeID: node.ID,
		Name: "codehold-" + name, Kind: lxc.KindSystemContainer, Status: "running", ImagePin: chDefaultImage}); err != nil {
		e.t.Fatal(err)
	}
	if err := e.mem.PutCodeholdRunner(ctx, appdb.CodeholdRunner{ClusterID: e.cluster, RunnerID: id, Name: name, WorkloadID: wid, Desired: "running", State: "running"}); err != nil {
		e.t.Fatal(err)
	}
	return wid
}

func TestCodeholdDisableAsksWhatHappensToRunners(t *testing.T) {
	e := newCHEnv(t)
	e.connect()
	ctx := context.Background()
	keep := e.addRunnerContainer("r1", "keep-me")
	drop := e.addRunnerContainer("r2", "drop-me")
	code, raw := e.call("POST", "/api/v1/features/codehold/disable", "")
	if code != http.StatusConflict || !strings.Contains(raw, "codehold_runners") || !strings.Contains(raw, "keep-me") {
		t.Fatalf("disable without decisions %d %s", code, raw)
	}
	if code, raw := e.call("POST", "/api/v1/codehold/disable", `{"decisions":{"r1":"stop"}}`); code != http.StatusConflict || !strings.Contains(raw, "drop-me") {
		t.Fatalf("missing decision %d %s", code, raw)
	}
	if code, raw := e.call("POST", "/api/v1/codehold/disable", `{"decisions":{"r1":"stop","r2":"delete"}}`); code != 200 {
		t.Fatalf("disable %d %s", code, raw)
	}
	if e.s.chEnabled(ctx, e.cluster) {
		t.Fatal("feature still on")
	}
	if e.wl.lastLife.Action != "delete" && len(e.wl.deleted) == 0 {
		t.Fatalf("drop-me not deleted: %+v", e.wl)
	}
	if wl, _ := e.mem.GetWorkload(ctx, e.cluster, drop); wl != nil {
		t.Fatal("drop-me container still recorded")
	}
	if wl, _ := e.mem.GetWorkload(ctx, e.cluster, keep); wl == nil {
		t.Fatal("keep-me must be kept")
	}
	r := e.runner("r1")
	if !r.Detached || r.DeleteAfter != nil {
		t.Fatalf("kept runner %+v", r)
	}
	// Turning it on again gives the runner back to CodeHold.
	_, _ = e.call("POST", "/api/v1/features/codehold/enable", "")
	cctx, cancel := context.WithCancel(ctx)
	cancel()
	e.s.chLoop(cctx, e.cluster)
	if r := e.runner("r1"); r.Detached {
		t.Fatal("re-enabling must re-attach kept runners")
	}
}

func TestCodeholdForceDisableKeepsContainersForAWeek(t *testing.T) {
	e := newCHEnv(t)
	e.connect()
	ctx := context.Background()
	wid := e.addRunnerContainer("r1", "build")
	now := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)
	e.s.Now = func() time.Time { return now }
	if code, raw := e.call("POST", "/api/v1/codehold/disable", `{"force":true}`); code != 200 {
		t.Fatalf("force %d %s", code, raw)
	}
	r := e.runner("r1")
	if !r.Detached || r.DeleteAfter == nil || !r.DeleteAfter.Equal(now.Add(7*24*time.Hour)) {
		t.Fatalf("grace %+v", r)
	}
	code, raw := e.call("GET", "/api/v1/codehold/pending", "")
	if code != 200 || !strings.Contains(raw, `"name":"build"`) {
		t.Fatalf("pending %d %s", code, raw)
	}
	// Six days on, nothing is deleted.
	now = now.Add(6 * 24 * time.Hour)
	e.s.chSweep(ctx, e.cluster)
	if wl, _ := e.mem.GetWorkload(ctx, e.cluster, wid); wl == nil {
		t.Fatal("deleted before the grace period ended")
	}
	// After seven days it is.
	now = now.Add(25 * time.Hour)
	e.s.chSweep(ctx, e.cluster)
	if wl, _ := e.mem.GetWorkload(ctx, e.cluster, wid); wl != nil {
		t.Fatal("not deleted after the grace period")
	}
	if rr, _ := e.s.chRunner(ctx, e.cluster, "r1"); rr != nil {
		t.Fatal("record not removed")
	}
}

func TestCodeholdPendingKeep(t *testing.T) {
	e := newCHEnv(t)
	e.connect()
	e.addRunnerContainer("r1", "build")
	if code, _ := e.call("POST", "/api/v1/codehold/disable", `{"force":true}`); code != 200 {
		t.Fatal("force")
	}
	if code, raw := e.call("POST", "/api/v1/codehold/pending", `{"action":"keep"}`); code != 200 {
		t.Fatalf("keep %d %s", code, raw)
	}
	if r := e.runner("r1"); r.DeleteAfter != nil {
		t.Fatal("keep must cancel the deletion")
	}
}

func TestCodeholdSettingsValidate(t *testing.T) {
	e := newCHEnv(t)
	e.connect()
	for _, bad := range []string{
		`{"cpus":0,"memory_mb":100}`,
		`{"source":"url","download_url":"http://x/"}`,
		`{"source":"url","download_url":"https://x/","checksum_required":true,"sha256":"abc"}`,
	} {
		if code, _ := e.call("PUT", "/api/v1/codehold/settings", bad); code != http.StatusBadRequest {
			t.Fatalf("%s accepted: %d", bad, code)
		}
	}
	ok := `{"cpus":4,"memory_mb":4096,"disk_gb":80,"source":"url","download_url":"https://git.example.com/","checksum_required":false}`
	if code, raw := e.call("PUT", "/api/v1/codehold/settings", ok); code != 200 {
		t.Fatalf("%d %s", code, raw)
	}
	st := e.s.chSettingsOf(func() *appdb.CodeholdController {
		c, _ := e.mem.GetCodeholdController(context.Background(), e.cluster)
		return c
	}())
	src, err := e.s.chSource(context.Background(), e.cluster, st)
	if err != nil || src.URL != "https://git.example.com/" || src.SHA256 != "" {
		t.Fatalf("%+v %v", src, err)
	}
}
