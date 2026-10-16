package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/no-dal/ndl-ce/internal/appdb"
	"github.com/no-dal/ndl-ce/internal/guestextras"
	"github.com/no-dal/ndl-ce/internal/lxc"
	"github.com/no-dal/ndl-ce/internal/storage"
)

func chcliDo(t *testing.T, ts *httptest.Server, cookie string) func(method, path, body string) (int, []byte) {
	return func(method, path, body string) (int, []byte) {
		t.Helper()
		req, _ := http.NewRequest(method, ts.URL+path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
		res, err := ts.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		b, _ := io.ReadAll(res.Body)
		return res.StatusCode, b
	}
}

func TestWorkloadCreateGetsCodeholdCLIUnlessTurnedOff(t *testing.T) {
	s, mem, token := testServer(t)
	cluster, _ := mem.GetCluster(context.Background())
	nodeID := uuid.NewString()
	_ = mem.UpsertNode(context.Background(), appdb.Node{ID: nodeID, ClusterID: cluster.ID, Name: "local"})
	poolID, netID := seedCompute(t, mem, cluster.ID, nodeID)
	fw := &fakeWorkloads{}
	s.Workloads = fw
	s.Storage = fakeStorage{vol: storage.CreateVolumeResult{Handle: storage.VolumeHandle{
		BackendType: storage.BackendDirectory, BackendRef: "volumes/container-root/x",
		Kind: storage.KindFilesystem, Class: storage.ClassContainerRoot, Format: storage.FormatDirectory,
	}}}
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	do := chcliDo(t, ts, claimAdmin(t, ts, token))
	create := func(name, extra string) {
		t.Helper()
		code, b := do("POST", "/api/v1/workloads", `{"name":"`+name+`","kind":"system-container","image_pin":"debian/trixie/amd64/default","pool_id":"`+poolID+`","network_id":"`+netID+`"`+extra+`}`)
		if code != http.StatusCreated {
			t.Fatalf("create %d %s", code, b)
		}
		var got map[string]any
		_ = json.Unmarshal(b, &got)
		waitExtras(t, s, got["id"].(string))
	}

	// There is no built-in CodeHold server, so nothing is installed until an
	// address is set.
	create("unset", "")
	if fw.setups != 0 {
		t.Fatal("with no CodeHold address a new container must not get ch")
	}
	if code, b := do("PUT", "/api/v1/codehold-cli/settings", `{"url":"https://codehold.example.com","auto_install":true}`); code != 200 {
		t.Fatalf("settings %d %s", code, b)
	}
	create("default", "")
	if fw.setups != 1 || !slices.Equal(fw.lastLife.Extras, []string{guestextras.CodeholdCLI}) {
		t.Fatalf("a new container must get ch by default: setups=%d extras=%v", fw.setups, fw.lastLife.Extras)
	}
	if fw.lastLife.Codehold == nil || fw.lastLife.Codehold.URL != "https://codehold.example.com" || fw.lastLife.Codehold.AccessClientSecret != "" {
		t.Fatalf("the configured CodeHold address must be passed, without Access headers: %+v", fw.lastLife.Codehold)
	}

	create("opted-out", `,"codehold_cli":false`)
	if fw.setups != 1 {
		t.Fatal("codehold_cli false must keep ch out of that container")
	}

	if code, b := do("PUT", "/api/v1/codehold-cli/settings", `{"url":"https://code.example.com/","auto_install":false}`); code != 200 {
		t.Fatalf("settings %d %s", code, b)
	}
	create("global-off", "")
	if fw.setups != 1 {
		t.Fatal("with auto install off a new container must not get ch")
	}
	create("asked", `,"extras":["codehold-cli"]`)
	if fw.setups != 2 || fw.lastLife.Codehold == nil || fw.lastLife.Codehold.URL != "https://code.example.com" {
		t.Fatalf("an explicit request must still install, from the configured address: %+v", fw.lastLife.Codehold)
	}

	code, b := do("GET", "/api/v1/workload-extras?image_pin=debian/trixie/amd64/default", "")
	if code != 200 || !strings.Contains(string(b), `"id":"codehold-cli"`) || strings.Contains(string(b), `"default":true`) {
		t.Fatalf("catalog must list ch, not as a default while auto install is off: %s", b)
	}
}

func TestCodeholdCLISettingsValidation(t *testing.T) {
	s, _, token := testServer(t)
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	do := chcliDo(t, ts, claimAdmin(t, ts, token))

	code, b := do("GET", "/api/v1/codehold-cli", "")
	if code != 200 || !strings.Contains(string(b), `"url":""`) || !strings.Contains(string(b), `"auto_install":true`) || !strings.Contains(string(b), `"concurrency":5`) {
		t.Fatalf("defaults %d %s", code, b)
	}
	for _, bad := range []string{`{"url":"http://git.example.com","auto_install":true}`, `{"url":"git.example.com","auto_install":true}`, `{"concurrency":99,"auto_install":true}`} {
		if code, b := do("PUT", "/api/v1/codehold-cli/settings", bad); code != http.StatusBadRequest {
			t.Fatalf("%s must be refused: %d %s", bad, code, b)
		}
	}
}

// cliWorkloads fakes the agent for rollouts: per container, whether ch is
// installed and how an install goes.
type cliWorkloads struct {
	fakeWorkloads
	mu        sync.Mutex
	installed map[string]string // workload -> version
	fail      map[string]string // workload -> warning
	hang      map[string]bool
	actions   []string
	inFlight  int
	maxFlight int
}

func (f *cliWorkloads) LifecycleCT(ctx context.Context, req lxc.LifecycleRequest) (lxc.Result, error) {
	f.mu.Lock()
	f.actions = append(f.actions, req.WorkloadID+" "+req.Action)
	f.inFlight++
	if f.inFlight > f.maxFlight {
		f.maxFlight = f.inFlight
	}
	hang := f.hang[req.WorkloadID] && req.Action != "stop"
	f.mu.Unlock()
	time.Sleep(5 * time.Millisecond)
	defer func() { f.mu.Lock(); f.inFlight--; f.mu.Unlock() }()
	if hang {
		<-ctx.Done()
		return lxc.Result{}, ctx.Err()
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	status := func() []lxc.ExtraStatus {
		v, ok := f.installed[req.WorkloadID]
		return []lxc.ExtraStatus{{ID: guestextras.CodeholdCLI, Installed: ok, Version: v}}
	}
	switch req.Action {
	case lxc.ActionGuestExtrasStatus:
		return lxc.Result{WorkloadID: req.WorkloadID, Extras: status()}, nil
	case lxc.ActionGuestSetup, lxc.ActionGuestExtrasUpdate:
		if req.Codehold == nil || req.Codehold.URL == "" {
			return lxc.Result{}, errors.New("no source")
		}
		if msg := f.fail[req.WorkloadID]; msg != "" {
			return lxc.Result{WorkloadID: req.WorkloadID, Extras: status(), SetupWarnings: []lxc.SetupWarning{{Extra: guestextras.CodeholdCLI, Message: msg}}}, nil
		}
		outcome := lxc.ChangeInstalled
		if _, ok := f.installed[req.WorkloadID]; ok {
			outcome = lxc.ChangeCurrent
			if req.Action == lxc.ActionGuestExtrasUpdate {
				outcome = lxc.ChangeUpdated
			}
		}
		if outcome != lxc.ChangeCurrent {
			f.installed[req.WorkloadID] = "ch v2"
		}
		return lxc.Result{WorkloadID: req.WorkloadID, Extras: status(), Changes: []lxc.ExtraChange{{Extra: guestextras.CodeholdCLI, Outcome: outcome, Version: f.installed[req.WorkloadID]}}}, nil
	}
	return lxc.Result{WorkloadID: req.WorkloadID, Status: lxc.StatusRunning}, nil
}

func (f *cliWorkloads) ran(action string) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, a := range f.actions {
		if strings.HasSuffix(a, " "+action) {
			out = append(out, strings.TrimSuffix(a, " "+action))
		}
	}
	return out
}

type cliFixture struct {
	s   *Server
	mem *appdb.Memory
	fw  *cliWorkloads
	do  func(method, path, body string) (int, []byte)
	ids map[string]string
}

func newCLIFixture(t *testing.T) *cliFixture {
	s, mem, token := testServer(t)
	cluster, _ := mem.GetCluster(context.Background())
	ids := map[string]string{}
	add := func(name, pin, status, power string) {
		id := uuid.NewString()
		ids[name] = id
		_ = mem.CreateWorkload(context.Background(), appdb.Workload{
			ID: id, ClusterID: cluster.ID, NodeID: "n1", Name: name, Kind: lxc.KindSystemContainer,
			Status: status, ImagePin: pin, DesiredPower: power,
		})
	}
	add("has-ch", "debian/trixie/amd64/default", lxc.StatusRunning, "running")
	add("fresh", "debian/trixie/amd64/default", lxc.StatusRunning, "running")
	add("offline", "debian/trixie/amd64/default", lxc.StatusRunning, "running")
	add("asleep", "debian/trixie/amd64/default", lxc.StatusStopped, "stopped")
	add("odd", "fedora/41/amd64/default", lxc.StatusRunning, "running")
	fw := &cliWorkloads{
		installed: map[string]string{ids["has-ch"]: "ch v1"},
		fail:      map[string]string{ids["offline"]: "could not download ch from https://codehold.example.com/downloads/ch-linux-amd64: curl: (6) Could not resolve host"},
		hang:      map[string]bool{},
	}
	s.Workloads = fw
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close)
	do := chcliDo(t, ts, claimAdmin(t, ts, token))
	if code, _ := do("POST", "/api/v1/codehold-cli/rollout", `{"dry_run":true}`); code != http.StatusBadRequest {
		t.Fatalf("a rollout with no CodeHold address must be refused: %d", code)
	}
	if code, b := do("PUT", "/api/v1/codehold-cli/settings", `{"url":"https://codehold.example.com","auto_install":true}`); code != 200 {
		t.Fatalf("settings %d %s", code, b)
	}
	return &cliFixture{s: s, mem: mem, fw: fw, do: do, ids: ids}
}

func (f *cliFixture) rollout(t *testing.T, body string) *cliRollout {
	t.Helper()
	code, b := f.do("POST", "/api/v1/codehold-cli/rollout", body)
	if code != http.StatusAccepted {
		t.Fatalf("rollout %d %s", code, b)
	}
	for i := 0; i < 1000; i++ {
		_, b = f.do("GET", "/api/v1/codehold-cli/rollout", "")
		var got struct{ Rollout *cliRollout }
		if err := json.Unmarshal(b, &got); err != nil {
			t.Fatal(err)
		}
		if got.Rollout != nil && !got.Rollout.Running {
			return got.Rollout
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("rollout did not finish")
	return nil
}

func byName(ts []*cliTarget) map[string]*cliTarget {
	out := map[string]*cliTarget{}
	for _, t := range ts {
		out[t.Name] = t
	}
	return out
}

func TestCodeholdCLIDryRunChangesNothing(t *testing.T) {
	f := newCLIFixture(t)
	run := f.rollout(t, `{"dry_run":true}`)
	if len(f.fw.ran(lxc.ActionGuestSetup))+len(f.fw.ran(lxc.ActionGuestExtrasUpdate))+len(f.fw.ran("start")) != 0 {
		t.Fatalf("a dry run must not install or start anything: %v", f.fw.actions)
	}
	got := byName(run.Targets)
	if p := got["has-ch"]; p == nil || p.Plan != "skip" || !p.Installed || p.Version != "ch v1" {
		t.Fatalf("has-ch %+v", p)
	}
	if p := got["fresh"]; p == nil || p.Plan != "install" || p.Installed {
		t.Fatalf("fresh %+v", p)
	}
	skipped := byName(run.Skipped)
	if skipped["asleep"] == nil || skipped["asleep"].Outcome != cliNotRunning || skipped["odd"] == nil || skipped["odd"].Outcome != cliUnsupported {
		t.Fatalf("stopped and unsupported containers must be listed as skipped: %+v", run.Skipped)
	}
	if run.Summary[cliNotRunning] != 1 || run.Summary[cliUnsupported] != 1 {
		t.Fatalf("summary %+v", run.Summary)
	}

	run = f.rollout(t, `{"dry_run":true,"update":true}`)
	if p := byName(run.Targets)["has-ch"]; p.Plan != "update" {
		t.Fatalf("update dry run %+v", p)
	}
}

func TestCodeholdCLIRolloutSummary(t *testing.T) {
	f := newCLIFixture(t)
	run := f.rollout(t, `{}`)
	got := byName(run.Targets)
	if got["fresh"].Outcome != cliInstalled || got["fresh"].Version != "ch v2" {
		t.Fatalf("fresh %+v", got["fresh"])
	}
	if got["has-ch"].Outcome != cliCurrent {
		t.Fatalf("an installed ch must be left alone without update: %+v", got["has-ch"])
	}
	if o := got["offline"]; o.Outcome != cliFailed || !strings.Contains(o.Reason, "Could not resolve host") {
		t.Fatalf("offline %+v", o)
	}
	if run.Summary[cliInstalled] != 1 || run.Summary[cliCurrent] != 1 || run.Summary[cliFailed] != 1 || run.Summary[cliNotRunning] != 1 {
		t.Fatalf("summary %+v", run.Summary)
	}
	if slices.Contains(f.fw.ran(lxc.ActionGuestSetup), f.ids["asleep"]) {
		t.Fatal("a stopped container must be skipped by default")
	}
	cluster, _ := f.mem.GetCluster(context.Background())
	var op *appdb.Operation
	ops, _ := f.mem.ListOperations(context.Background(), cluster.ID, 50)
	for i := range ops {
		if ops[i].ID == run.OperationID {
			op = &ops[i]
		}
	}
	if op == nil || op.Kind != "codehold-cli.rollout" || op.State != "failed" || !strings.Contains(op.Message, "Could not resolve host") {
		t.Fatalf("the rollout must be recorded with its failures: %+v", op)
	}
	exts, _ := f.mem.ListWorkloadExtensions(context.Background(), cluster.ID, f.ids["fresh"])
	if len(exts) != 1 || exts[0].State != appdb.ExtensionInstalled || exts[0].Version != "ch v2" {
		t.Fatalf("the extension must be recorded on the container: %+v", exts)
	}

	run = f.rollout(t, `{"update":true,"include_stopped":true,"workload_ids":["`+f.ids["has-ch"]+`","`+f.ids["asleep"]+`"]}`)
	got = byName(run.Targets)
	if len(run.Targets) != 2 || got["has-ch"].Outcome != cliUpdated || got["asleep"].Outcome != cliInstalled {
		t.Fatalf("update with include_stopped %+v", run.Targets)
	}
	if !slices.Contains(f.fw.ran("start"), f.ids["asleep"]) || !slices.Contains(f.fw.ran("stop"), f.ids["asleep"]) {
		t.Fatalf("a stopped container must be started for the install and stopped again: %v", f.fw.actions)
	}
}

func TestCodeholdCLIRolloutTimeoutAndConcurrency(t *testing.T) {
	f := newCLIFixture(t)
	f.fw.hang[f.ids["fresh"]] = true
	run := f.rollout(t, `{"timeout_seconds":1,"concurrency":2}`)
	if o := byName(run.Targets)["fresh"]; o.Outcome != cliFailed || o.Reason != "timed out after 1s" {
		t.Fatalf("a stuck container must time out on its own: %+v", o)
	}
	if byName(run.Targets)["has-ch"].Outcome != cliCurrent {
		t.Fatal("one stuck container must not hold up the others")
	}
	if f.fw.maxFlight > 2 {
		t.Fatalf("at most 2 containers at a time, saw %d", f.fw.maxFlight)
	}
	if code, _ := f.do("POST", "/api/v1/codehold-cli/rollout", `{"workload_ids":["nope"]}`); code != http.StatusBadRequest {
		t.Fatal("an unknown container must be refused")
	}
}
