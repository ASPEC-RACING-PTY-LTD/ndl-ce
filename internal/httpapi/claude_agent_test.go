package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/no-dal/ndl-ce/internal/appdb"
	"github.com/no-dal/ndl-ce/internal/claudeagent"
	"github.com/no-dal/ndl-ce/internal/features"
	"github.com/no-dal/ndl-ce/internal/gameserver"
	"github.com/no-dal/ndl-ce/internal/lxc"
)

// agentWorkloads is the local agent for Claude Agent and leftover tests.
type agentWorkloads struct {
	*fakeWorkloads
	calls     []string
	reqs      []claudeagent.Request
	mounts    []lxc.Mount
	leftovers []lxc.Leftover
	removed   []string
	// down is set when the agent's container was restarted with a folder
	// it cannot mount.
	down bool
}

func (a *agentWorkloads) GetWorkloads(context.Context, []lxc.Hint) (lxc.Observation, error) {
	st := lxc.StatusRunning
	if a.down {
		st = lxc.StatusStopped
	}
	return lxc.Observation{Workloads: []lxc.Observed{{WorkloadID: "wl-agent", Kind: lxc.KindSystemContainer, Status: st, UnitActive: !a.down}}}, nil
}

func (a *agentWorkloads) LifecycleCT(ctx context.Context, req lxc.LifecycleRequest) (lxc.Result, error) {
	if req.Action == lxc.ActionMountsSet {
		changed := len(a.mounts) != len(req.Mounts)
		a.mounts = req.Mounts
		return lxc.Result{WorkloadID: req.WorkloadID, Mounts: req.Mounts, RestartRequired: changed}, nil
	}
	if req.Action == "restart" {
		a.down = false
		for _, m := range a.mounts {
			if strings.Contains(m.Source, "broken") {
				a.down = true
			}
		}
		return lxc.Result{WorkloadID: req.WorkloadID}, nil
	}
	return a.fakeWorkloads.LifecycleCT(ctx, req)
}

func (a *agentWorkloads) ClaudeAgent(_ context.Context, action string, req claudeagent.Request, out any) error {
	a.calls = append(a.calls, action)
	a.reqs = append(a.reqs, req)
	var res any = claudeagent.Status{Installed: true}
	switch action {
	case "grant":
		var g claudeagent.GrantResult
		for _, acl := range req.ACLs {
			g.Mounts = append(g.Mounts, claudeagent.MountSource{ServerID: acl.ServerID, Path: acl.Path, Write: acl.Write, Source: "/var/lib/ndl/gameservers/" + acl.ServerID + "/" + acl.Path})
		}
		res = g
	case "login-start":
		res = claudeagent.Status{Installed: true, LoginURL: "https://claude.com/cai/oauth/authorize?x=1"}
	case "login-code":
		res = claudeagent.Status{Installed: true, LoggedIn: true, Account: "op@example.com"}
	}
	if out != nil {
		raw, _ := json.Marshal(res)
		return json.Unmarshal(raw, out)
	}
	return nil
}

func (a *agentWorkloads) CtLeftovers(_ context.Context, action string, ids []string, out any) error {
	var res any = a.leftovers
	if action == "remove" {
		a.removed = append(a.removed, ids...)
		var done []lxc.LeftoverRemoved
		for _, id := range ids {
			for _, l := range a.leftovers {
				if l.ID == id {
					done = append(done, lxc.LeftoverRemoved{ID: id, Rootfs: l.Rootfs, RootfsKept: l.RootfsMount, RemovedData: !l.RootfsMount})
				}
			}
		}
		res = done
	}
	raw, _ := json.Marshal(res)
	return json.Unmarshal(raw, out)
}

func confirmCall(t *testing.T, ts *httptest.Server, cookie, method, path, body, confirm string) (int, string) {
	t.Helper()
	req, _ := http.NewRequest(method, ts.URL+path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	if confirm != "" {
		req.Header.Set(confirmHeader, confirm)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return res.StatusCode, string(b)
}

func bearerCall(t *testing.T, ts *httptest.Server, token, method, path, body string) (int, map[string]any) {
	t.Helper()
	req, _ := http.NewRequest(method, ts.URL+path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(res.Body).Decode(&out)
	return res.StatusCode, out
}

func TestClaudeAgent(t *testing.T) {
	s, mem, token := testServer(t)
	s.Game = gameserver.NewRuntime(t.TempDir())
	host := &playersHost{Host: &gameserver.LocalHost{RT: s.Game}}
	s.GameHost = host
	wl := &agentWorkloads{fakeWorkloads: &fakeWorkloads{}}
	s.Workloads = wl
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)
	ctx := context.Background()
	cluster, _ := mem.GetCluster(ctx)
	enableGameServers(t, mem, cluster.ID)
	_ = mem.CreateGameServer(ctx, appdb.GameServer{
		ID: "gs-jvs", ClusterID: cluster.ID, Name: "JvS", Game: "gmod", Family: "source", TemplateID: "ndl-gmod", Status: "ready",
		EnvJSON: []byte(`{}`), PortsJSON: []byte(`[]`), Capabilities: []byte(`["files","console"]`), CreatedAt: time.Now().UTC(),
	})

	// Off until turned on in Add Features.
	if code, _ := jsonCall(t, ts, cookie, "POST", "/api/v1/claude-agent/setup", `{}`); code != http.StatusConflict {
		t.Fatalf("setup while off = %d", code)
	}
	_ = mem.UpsertFeature(ctx, appdb.Feature{ClusterID: cluster.ID, ID: features.IDClaudeAgent, Enabled: true, UpdatedAt: time.Now().UTC()})

	code, out := jsonCall(t, ts, cookie, "GET", "/api/v1/claude-agent", "")
	servers, _ := out["servers"].([]any)
	if code != 200 || len(servers) != 1 || len(servers[0].(map[string]any)["default_folders"].([]any)) != 2 {
		t.Fatalf("status %d %v", code, out)
	}

	// Grants saved before setup are kept and applied once it is ready.
	grants := `{"grants":[{"kind":"gameserver","id":"gs-jvs","folders":[{"path":"garrysmod/gamemodes","write":true},{"path":"garrysmod/addons","write":true}],"log":true,"console":true}]}`
	if code, out := jsonCall(t, ts, cookie, "PUT", "/api/v1/claude-agent/grants", grants); code != 200 || out["applied"] != false {
		t.Fatalf("early grants %d %v", code, out)
	}
	caPollEvery, caRestartWait = 10*time.Millisecond, 300*time.Millisecond
	t.Cleanup(func() { caPollEvery, caRestartWait = 5*time.Second, 60*time.Second })
	_ = mem.CreateWorkload(ctx, appdb.Workload{ID: "wl-agent", ClusterID: cluster.ID, Name: caWorkloadName, Kind: lxc.KindSystemContainer, Status: lxc.StatusRunning, DesiredPower: "running"})
	_, _ = s.caUpdate(ctx, cluster.ID, "test", func(c *caConfig) error {
		c.WorkloadID, c.State = "wl-agent", caStateReady
		return nil
	})
	if code, out := jsonCall(t, ts, cookie, "PUT", "/api/v1/claude-agent/grants", grants); code != 200 || out["applied"] != true {
		t.Fatalf("grants %d %v", code, out)
	}
	if len(wl.mounts) != 2 || wl.mounts[0].Target != "/srv/jvs/garrysmod/addons" || wl.mounts[0].ReadOnly {
		t.Fatalf("mounts %+v", wl.mounts)
	}
	last := wl.reqs[len(wl.reqs)-1]
	if wl.calls[len(wl.calls)-1] != "configure" || !strings.HasPrefix(last.APIToken, caTokenPrefix) || len(last.Grants) != 1 {
		t.Fatalf("configure %v %+v", wl.calls, last)
	}
	agentToken := last.APIToken

	// Narrowing a grant takes the removed folder's access away.
	narrow := `{"grants":[{"kind":"gameserver","id":"gs-jvs","folders":[{"path":"garrysmod/addons"}],"log":true}]}`
	if code, _ := jsonCall(t, ts, cookie, "PUT", "/api/v1/claude-agent/grants", narrow); code != 200 {
		t.Fatalf("narrow %d", code)
	}
	var revoked []string
	for i, c := range wl.calls {
		if c == "revoke" {
			for _, a := range wl.reqs[i].ACLs {
				revoked = append(revoked, a.Path)
			}
		}
	}
	if strings.Join(revoked, ",") != "garrysmod/addons,garrysmod/gamemodes" || len(wl.mounts) != 1 || !wl.mounts[0].ReadOnly {
		t.Fatalf("revoked %v mounts %+v", revoked, wl.mounts)
	}
	// The token rotates on every change; the old one stops working.
	if code, _ := bearerCall(t, ts, agentToken, "GET", "/api/v1/claude-agent/api/servers", ""); code != http.StatusUnauthorized {
		t.Fatalf("old token = %d", code)
	}
	agentToken = wl.reqs[len(wl.reqs)-1].APIToken

	// ndl's API: only what is granted.
	code, out = bearerCall(t, ts, agentToken, "GET", "/api/v1/claude-agent/api/servers", "")
	if code != 200 || len(out["servers"].([]any)) != 1 {
		t.Fatalf("servers %d %v", code, out)
	}
	if code, _ := bearerCall(t, ts, agentToken, "POST", "/api/v1/claude-agent/api/servers/jvs/console", `{"command":"lua_run print(1)"}`); code != http.StatusForbidden {
		t.Fatalf("ungranted console = %d", code)
	}
	if code, _ := bearerCall(t, ts, agentToken, "POST", "/api/v1/claude-agent/api/servers/jvs/restart", ``); code != http.StatusForbidden {
		t.Fatalf("ungranted restart = %d", code)
	}
	if code, _ := bearerCall(t, ts, agentToken, "GET", "/api/v1/claude-agent/api/servers/other/log", ""); code != http.StatusNotFound {
		t.Fatalf("other server = %d", code)
	}
	for _, tok := range []string{"", "ndlca_wrong", cookie} {
		if code, _ := bearerCall(t, ts, tok, "GET", "/api/v1/claude-agent/api/servers", ""); code != http.StatusUnauthorized {
			t.Fatalf("token %q = %d", tok, code)
		}
	}
	if code, _ := jsonCall(t, ts, cookie, "PUT", "/api/v1/claude-agent/grants", grants); code != 200 {
		t.Fatal("re-grant")
	}
	agentToken = wl.reqs[len(wl.reqs)-1].APIToken
	if code, out := bearerCall(t, ts, agentToken, "POST", "/api/v1/claude-agent/api/servers/jvs/console", `{"command":"lua_run print(1)"}`); code != 200 || len(host.sent) != 1 || host.sent[0] != "lua_run print(1)" {
		t.Fatalf("console %d %v %v", code, out, host.sent)
	}
	if code, _ := bearerCall(t, ts, agentToken, "POST", "/api/v1/claude-agent/api/servers/jvs/console", `{"command":"say hi\nquit"}`); code != http.StatusBadRequest {
		t.Fatalf("two lines = %d", code)
	}

	// A folder the container cannot mount puts the previous folders back.
	_ = mem.CreateGameServer(ctx, appdb.GameServer{
		ID: "gs-broken", ClusterID: cluster.ID, Name: "Broken", Game: "gmod", Family: "source", TemplateID: "ndl-gmod", Status: "ready",
		EnvJSON: []byte(`{}`), PortsJSON: []byte(`[]`), Capabilities: []byte(`["files"]`), CreatedAt: time.Now().UTC(),
	})
	before := len(wl.mounts)
	bad := `{"grants":[{"kind":"gameserver","id":"gs-jvs","folders":[{"path":"garrysmod/gamemodes","write":true},{"path":"garrysmod/addons","write":true}],"log":true,"console":true},{"kind":"gameserver","id":"gs-broken","folders":[{"path":"x"}]}]}`
	if code, out := jsonCall(t, ts, cookie, "PUT", "/api/v1/claude-agent/grants", bad); code != http.StatusUnprocessableEntity || !strings.Contains(out["error"].(string), "previous folders are being put back") {
		t.Fatalf("broken folder %d %v", code, out)
	}
	// The rollback runs in the background; its last step is the log line.
	for i := 0; i < 300; i++ {
		_, ov := jsonCall(t, ts, cookie, "GET", "/api/v1/claude-agent", "")
		if strings.Contains(fmt.Sprint(ov["log"]), "previous folders were put back") {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if len(wl.mounts) != before || wl.down {
		t.Fatalf("not rolled back: %d mounts, down=%v", len(wl.mounts), wl.down)
	}
	if code, out := jsonCall(t, ts, cookie, "GET", "/api/v1/claude-agent", ""); code != 200 || len(out["grants"].([]any)) != 1 || out["container_running"] != true {
		t.Fatalf("after rollback %d %v", code, out)
	}

	// A stopped container is said plainly, and Start brings it back.
	wl.down = true
	if code, out := jsonCall(t, ts, cookie, "POST", "/api/v1/claude-agent/login", `{"action":"start"}`); code != http.StatusConflict || !strings.Contains(out["error"].(string), "not running") {
		t.Fatalf("login while stopped %d %v", code, out)
	}
	if code, out := jsonCall(t, ts, cookie, "GET", "/api/v1/claude-agent", ""); code != 200 || out["container_running"] != false || out["status"] != nil {
		t.Fatalf("overview while stopped %d %v", code, out)
	}
	if code, out := jsonCall(t, ts, cookie, "POST", "/api/v1/claude-agent/start", ``); code != 200 || wl.down {
		t.Fatalf("start %d %v", code, out)
	}

	// Sign-in.
	if code, out := jsonCall(t, ts, cookie, "POST", "/api/v1/claude-agent/login", `{"action":"start"}`); code != 200 || !strings.HasPrefix(out["login_url"].(string), "https://") {
		t.Fatalf("login start %d %v", code, out)
	}
	if code, _ := jsonCall(t, ts, cookie, "POST", "/api/v1/claude-agent/login", `{"action":"code","code":"x; rm -rf /"}`); code != http.StatusBadRequest {
		t.Fatalf("bad code = %d", code)
	}
	if code, out := jsonCall(t, ts, cookie, "POST", "/api/v1/claude-agent/login", `{"action":"code","code":"AbCdEf123456#xyz"}`); code != 200 || out["logged_in"] != true {
		t.Fatalf("login code %d %v", code, out)
	}

	// The feature cannot be turned off while the container exists, and
	// removing it takes every folder's access away first.
	if code, body := confirmCall(t, ts, cookie, "POST", "/api/v1/features/"+features.IDClaudeAgent+"/disable", ``, ""); code != http.StatusConflict || !strings.Contains(body, "claude_agent_container") {
		t.Fatalf("disable %d %s", code, body)
	}
	if code, _ := confirmCall(t, ts, cookie, "POST", "/api/v1/claude-agent/remove", ``, ""); code != http.StatusPreconditionRequired {
		t.Fatalf("remove without confirm = %d", code)
	}
	if code, body := confirmCall(t, ts, cookie, "POST", "/api/v1/claude-agent/remove", ``, caRemoveConfirm); code != 200 {
		t.Fatalf("remove %d %s", code, body)
	}
	if wl.calls[len(wl.calls)-1] != "revoke" || len(wl.reqs[len(wl.reqs)-1].ACLs) != 2 {
		t.Fatalf("remove did not revoke: %v", wl.calls)
	}
	if code, _ := bearerCall(t, ts, agentToken, "GET", "/api/v1/claude-agent/api/servers", ""); code != http.StatusUnauthorized {
		t.Fatalf("token after remove = %d", code)
	}
}

func TestCTLeftovers(t *testing.T) {
	s, mem, token := testServer(t)
	old := time.Now().Add(-48 * time.Hour)
	wl := &agentWorkloads{fakeWorkloads: &fakeWorkloads{}, leftovers: []lxc.Leftover{
		{ID: "2bfff17a-0f19-4f3b-96a7-ba94fad9f0d1", UnitEnabled: true, HasConfig: true, Rootfs: "/pool/volumes/container-root/f67be82e-c14b-4f43-97e5-cf48cdf8d9ab", RootfsMount: true, Modified: old},
		{ID: "3193d754-6f83-4c13-806e-70a3fb0f0249", UnitEnabled: true, HasConfig: true, Rootfs: "/pool/volumes/container-root/5ebee57c-8477-4367-afc0-189de751b0ef", Modified: old},
		{ID: "9de638b0-74d4-4202-9e9b-1cf89b9c0f2f", UnitEnabled: true, HasConfig: true, Modified: old},
		{ID: "0609eec1-801e-4ef1-96a3-1b3e1990fe79", HasConfig: true, Modified: time.Now()},
	}}
	s.Workloads = wl
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)
	ctx := context.Background()
	cluster, _ := mem.GetCluster(ctx)
	// A live workload and a volume row for one leftover's mounted root.
	_ = mem.CreateWorkload(ctx, appdb.Workload{ID: "9de638b0-74d4-4202-9e9b-1cf89b9c0f2f", ClusterID: cluster.ID, Name: "AspecRacing", Kind: lxc.KindSystemContainer})
	_ = mem.CreateVolume(ctx, appdb.Volume{ID: "f67be82e-c14b-4f43-97e5-cf48cdf8d9ab", ClusterID: cluster.ID, Class: "container-root"})

	code, out := jsonCall(t, ts, cookie, "GET", "/api/v1/workloads/leftovers?sizes=0", "")
	items, _ := out["items"].([]any)
	if code != 200 || len(items) != 2 {
		t.Fatalf("list %d %v", code, out)
	}
	if code, out := jsonCall(t, ts, cookie, "GET", "/api/v1/workloads/leftovers?sizes=0&recent=1", ""); code != 200 || len(out["items"].([]any)) != 3 {
		t.Fatalf("recent list %d %v", code, out)
	}
	ids := `{"ids":["2bfff17a-0f19-4f3b-96a7-ba94fad9f0d1","3193d754-6f83-4c13-806e-70a3fb0f0249"]}`
	if code, _ := confirmCall(t, ts, cookie, "POST", "/api/v1/workloads/leftovers/remove", ids, ""); code != http.StatusPreconditionRequired {
		t.Fatalf("remove without confirm = %d", code)
	}
	// A live workload or a just-written container is never removed.
	for _, id := range []string{"9de638b0-74d4-4202-9e9b-1cf89b9c0f2f", "0609eec1-801e-4ef1-96a3-1b3e1990fe79"} {
		if code, _ := confirmCall(t, ts, cookie, "POST", "/api/v1/workloads/leftovers/remove", `{"ids":["`+id+`"]}`, removeLeftoversConfirm); code != http.StatusConflict {
			t.Fatalf("remove %s = %d", id, code)
		}
	}
	if len(wl.removed) != 0 {
		t.Fatalf("refused ids reached the node: %v", wl.removed)
	}
	code, body := confirmCall(t, ts, cookie, "POST", "/api/v1/workloads/leftovers/remove", ids, removeLeftoversConfirm)
	if code != 200 || len(wl.removed) != 2 {
		t.Fatalf("remove %d %s", code, body)
	}
	if v, _ := mem.GetVolume(ctx, cluster.ID, "f67be82e-c14b-4f43-97e5-cf48cdf8d9ab"); v != nil {
		t.Fatal("the mounted root's volume row was kept")
	}
	if !strings.Contains(body, `"volume_id":"f67be82e-c14b-4f43-97e5-cf48cdf8d9ab"`) {
		t.Fatalf("body %s", body)
	}
}
