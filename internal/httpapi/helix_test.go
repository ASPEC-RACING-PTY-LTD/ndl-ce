package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/no-dal/ndl-ce/internal/agentrpc"
	"github.com/no-dal/ndl-ce/internal/appdb"
	"github.com/no-dal/ndl-ce/internal/auth"
	"github.com/no-dal/ndl-ce/internal/deploy"
	"github.com/no-dal/ndl-ce/internal/lxc"
	"github.com/no-dal/ndl-ce/internal/rbac"
)

// helixProbes records deploy-probe calls; block makes them wait for ctx.
type helixProbes struct {
	mu    sync.Mutex
	calls []agentrpc.DeployRequest
	block chan struct{}
}

func (f *helixProbes) Deploy(ctx context.Context, action string, req agentrpc.DeployRequest, out any) error {
	if action != "deploy-probe" {
		return nil
	}
	f.mu.Lock()
	f.calls = append(f.calls, req)
	block := f.block
	f.mu.Unlock()
	if block != nil {
		close(block)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(10 * time.Second):
			return context.DeadlineExceeded
		}
	}
	raw, _ := json.Marshal(deploy.ProbeResult{Output: "ran: " + req.Script, ExitCode: 0})
	return json.Unmarshal(raw, out)
}

func helixReq(t *testing.T, ts *httptest.Server, token, method, path, body string) (int, map[string]any) {
	t.Helper()
	req, _ := http.NewRequest(method, ts.URL+path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	res, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	out := map[string]any{}
	_ = json.Unmarshal(raw, &out)
	return res.StatusCode, out
}

func TestHelixIntegration(t *testing.T) {
	s, mem, token := testServer(t)
	probes := &helixProbes{}
	s.Deployer = probes
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)
	ctx := context.Background()
	cluster, _ := mem.GetCluster(ctx)
	app := appdb.Workload{ID: uuid.NewString(), ClusterID: cluster.ID, Name: "app", Kind: lxc.KindSystemContainer, Status: lxc.StatusRunning, DesiredPower: "running"}
	db := appdb.Workload{ID: uuid.NewString(), ClusterID: cluster.ID, Name: "db", Kind: lxc.KindSystemContainer, Status: lxc.StatusRunning, DesiredPower: "running"}
	secret := appdb.Workload{ID: uuid.NewString(), ClusterID: cluster.ID, Name: "secret", Kind: lxc.KindSystemContainer, Status: lxc.StatusRunning}
	vm := appdb.Workload{ID: uuid.NewString(), ClusterID: cluster.ID, Name: "win", Kind: "vm", Status: lxc.StatusRunning}
	for _, w := range []appdb.Workload{app, db, secret, vm} {
		if err := mem.CreateWorkload(ctx, w); err != nil {
			t.Fatal(err)
		}
	}

	// Configure as SuperAdmin.
	code, got := apiJSON(t, ts, cookie, "POST", "/api/v1/helix/credentials", `{"name":"helix-lxc"}`)
	if code != http.StatusCreated {
		t.Fatalf("credential %d %v", code, got)
	}
	hlx := got["token"].(string)
	if !strings.HasPrefix(hlx, "hlx_") || len(hlx) != 68 {
		t.Fatalf("token %q", hlx)
	}
	if code, got := apiJSON(t, ts, cookie, "PUT", "/api/v1/helix/machines", `{"grants":[{"machine_id":"`+vm.ID+`","caps":["execute"]}]}`); code != http.StatusUnprocessableEntity {
		t.Fatalf("VMs cannot get execute: %d %v", code, got)
	}
	grants := `{"grants":[
		{"machine_id":"` + app.ID + `","caps":["read","logs","write","execute","deploy","power"]},
		{"machine_id":"` + db.ID + `","caps":["execute"],"protected":true},
		{"machine_id":"` + vm.ID + `","caps":["power"]}]}`
	if code, got := apiJSON(t, ts, cookie, "PUT", "/api/v1/helix/machines", grants); code != http.StatusOK {
		t.Fatalf("grants %d %v", code, got)
	}

	// Off by default.
	if code, _ := helixReq(t, ts, hlx, "GET", "/api/v1/helix/v1/machines", ""); code != http.StatusForbidden {
		t.Fatalf("integration off: %d", code)
	}
	apiJSON(t, ts, cookie, "PUT", "/api/v1/helix/settings", `{"enabled":true}`)

	// The credential works only on Helix routes, and never on settings.
	if code, _ := helixReq(t, ts, hlx, "GET", "/api/v1/workloads", ""); code != http.StatusUnauthorized {
		t.Fatalf("hlx on the normal API: %d", code)
	}
	for _, p := range []string{"/api/v1/helix/machines", "/api/v1/helix/kill/release", "/api/v1/helix/credentials"} {
		if code, _ := helixReq(t, ts, hlx, "PUT", p, `{"grants":[]}`); code != http.StatusForbidden && code != http.StatusUnauthorized && code != http.StatusMethodNotAllowed {
			t.Fatalf("hlx on %s: %d", p, code)
		}
		if code, _ := helixReq(t, ts, hlx, "POST", p, `{}`); code != http.StatusForbidden && code != http.StatusUnauthorized && code != http.StatusMethodNotAllowed {
			t.Fatalf("hlx on %s: %d", p, code)
		}
	}
	if code, _ := helixReq(t, ts, "hlx_wrong", "GET", "/api/v1/helix/v1/machines", ""); code != http.StatusUnauthorized {
		t.Fatal("a wrong credential")
	}

	// Only granted machines exist for Helix.
	code, got = helixReq(t, ts, hlx, "GET", "/api/v1/helix/v1/machines", "")
	if code != http.StatusOK || len(got["machines"].([]any)) != 3 {
		t.Fatalf("machines %d %v", code, got)
	}
	if code, _ := helixReq(t, ts, hlx, "GET", "/api/v1/helix/v1/machines/"+secret.ID, ""); code != http.StatusNotFound {
		t.Fatal("an ungranted machine must look like a missing one")
	}
	if code, _ := helixReq(t, ts, hlx, "POST", "/api/v1/helix/v1/machines/"+secret.ID+"/exec", `{"command":"id"}`); code != http.StatusNotFound {
		t.Fatal("exec on an ungranted machine")
	}
	if code, _ := helixReq(t, ts, hlx, "POST", "/api/v1/helix/v1/machines/"+vm.ID+"/exec", `{"command":"id"}`); code != http.StatusForbidden && code != http.StatusUnprocessableEntity {
		t.Fatalf("exec on a VM: %d", code)
	}

	// A plain command runs unattended.
	code, got = helixReq(t, ts, hlx, "POST", "/api/v1/helix/v1/machines/"+app.ID+"/exec", `{"command":"docker compose ps"}`)
	if code != http.StatusOK || got["exit_code"].(float64) != 0 || !strings.Contains(got["output"].(string), "docker compose ps") {
		t.Fatalf("exec %d %v", code, got)
	}
	if c := probes.calls[len(probes.calls)-1]; c.MachineID != app.ID || c.RunAs != "root" || c.Dir != "/tmp" {
		t.Fatalf("probe %+v", c)
	}
	if code, _ := helixReq(t, ts, hlx, "POST", "/api/v1/helix/v1/machines/"+app.ID+"/exec", `{"command":"id","run_as":"postgres"}`); code != http.StatusForbidden {
		t.Fatal("run_as outside the grant")
	}

	// A destructive command waits for approval, bound to that exact request.
	calls := len(probes.calls)
	code, got = helixReq(t, ts, hlx, "POST", "/api/v1/helix/v1/machines/"+app.ID+"/exec", `{"command":"docker system prune -af"}`)
	if code != http.StatusAccepted || got["status"] != "approval_required" || len(probes.calls) != calls {
		t.Fatalf("destructive %d %v", code, got)
	}
	approval := got["approval"].(map[string]any)["id"].(string)
	if code, _ := helixReq(t, ts, hlx, "POST", "/api/v1/helix/v1/machines/"+app.ID+"/exec", `{"command":"docker system prune -af","approval_id":"`+approval+`"}`); code != http.StatusConflict {
		t.Fatal("a pending approval cannot be used")
	}
	if code, _ := apiJSON(t, ts, cookie, "POST", "/api/v1/helix/approvals/"+approval+"/approve", `{"note":"ok"}`); code != http.StatusOK {
		t.Fatal("approve")
	}
	if code, _ := helixReq(t, ts, hlx, "POST", "/api/v1/helix/v1/machines/"+app.ID+"/exec", `{"command":"rm -rf /","approval_id":"`+approval+`"}`); code != http.StatusConflict {
		t.Fatal("an approval is for one exact request")
	}
	if code, got := helixReq(t, ts, hlx, "POST", "/api/v1/helix/v1/machines/"+app.ID+"/exec", `{"command":"docker system prune -af","approval_id":"`+approval+`"}`); code != http.StatusOK {
		t.Fatalf("approved run %d %v", code, got)
	}
	if code, _ := helixReq(t, ts, hlx, "POST", "/api/v1/helix/v1/machines/"+app.ID+"/exec", `{"command":"docker system prune -af","approval_id":"`+approval+`"}`); code != http.StatusConflict {
		t.Fatal("an approval is used once")
	}
	// Protected: even a plain command waits.
	if code, _ := helixReq(t, ts, hlx, "POST", "/api/v1/helix/v1/machines/"+db.ID+"/exec", `{"command":"uptime"}`); code != http.StatusAccepted {
		t.Fatal("protected machine")
	}

	// Power goes through the normal lifecycle handler as svc-helix with only
	// the lifecycle grants: it must get past permission checks (and then
	// fail here only because there is no node agent in tests).
	code, got = helixReq(t, ts, hlx, "POST", "/api/v1/helix/v1/machines/"+vm.ID+"/power/start", `{}`)
	if code == http.StatusForbidden || code == http.StatusUnauthorized || strings.Contains(strings.ToLower(asString(got["error"])), "permission") {
		t.Fatalf("power start was refused for permissions: %d %v", code, got)
	}
	t.Logf("power start without a node agent: %d %v", code, got)
	if code, _ := helixReq(t, ts, hlx, "POST", "/api/v1/helix/v1/machines/"+vm.ID+"/power/stop", `{}`); code != http.StatusAccepted {
		t.Fatal("power stop waits for approval")
	}
	if code, _ := helixReq(t, ts, hlx, "POST", "/api/v1/helix/v1/machines/"+db.ID+"/power/start", `{}`); code != http.StatusForbidden {
		t.Fatal("power is not granted on db")
	}

	// Events: machine events only for readable machines.
	_, first := helixReq(t, ts, hlx, "GET", "/api/v1/helix/v1/events", "")
	cursor := int(first["next"].(float64))
	s.automationEmit(ctx, cluster.ID, "workload.stopped", secret.ID, "", map[string]any{"workload_id": secret.ID})
	s.automationEmit(ctx, cluster.ID, "workload.stopped", app.ID, "", map[string]any{"workload_id": app.ID})
	code, got = helixReq(t, ts, hlx, "GET", "/api/v1/helix/v1/events?after="+itoa(cursor), "")
	evs := got["events"].([]any)
	if code != http.StatusOK || len(evs) != 1 || evs[0].(map[string]any)["machine_id"] != app.ID || evs[0].(map[string]any)["type"] != "machine.stopped" {
		t.Fatalf("events %d %v", code, got)
	}
	// Long poll wakes on a new event.
	go func() {
		time.Sleep(200 * time.Millisecond)
		s.automationEmit(ctx, cluster.ID, "uptime.monitor_down", "mon", "", map[string]any{"name": "site"})
	}()
	code, got = helixReq(t, ts, hlx, "GET", "/api/v1/helix/v1/events?wait=10&after="+itoa(int(got["next"].(float64))), "")
	if evs := got["events"].([]any); code != http.StatusOK || len(evs) != 1 || evs[0].(map[string]any)["type"] != "uptime.down" {
		t.Fatalf("long poll %d %v", code, got)
	}

	// IP allowlist.
	apiJSON(t, ts, cookie, "PUT", "/api/v1/helix/settings", `{"allowed_ips":["10.9.9.0/24"]}`)
	if code, _ := helixReq(t, ts, hlx, "GET", "/api/v1/helix/v1/whoami", ""); code != http.StatusForbidden {
		t.Fatal("address outside the allowlist")
	}
	apiJSON(t, ts, cookie, "PUT", "/api/v1/helix/settings", `{"allowed_ips":[]}`)

	// An Admin can engage the kill switch but not configure or release it.
	hash, _ := auth.HashPassword("password1")
	op := appdb.User{ID: uuid.NewString(), ClusterID: cluster.ID, Username: "ops", PasswordHash: hash}
	_ = mem.CreateUser(ctx, op)
	_ = mem.BindRole(ctx, cluster.ID, op.ID, rbac.Admin)
	opCookie := loginAs(t, ts, "ops", "password1")
	if code, _ := apiJSON(t, ts, opCookie, "GET", "/api/v1/helix", ""); code != http.StatusForbidden {
		t.Fatal("Admin cannot see the settings")
	}
	if code, _ := apiJSON(t, ts, opCookie, "PUT", "/api/v1/helix/machines", `{"grants":[]}`); code != http.StatusForbidden {
		t.Fatal("Admin cannot grant")
	}

	// The kill switch stops a running command and refuses everything after.
	probes.mu.Lock()
	probes.block = make(chan struct{})
	started := probes.block
	probes.mu.Unlock()
	result := make(chan int, 1)
	go func() {
		code, _ := helixReq(t, ts, hlx, "POST", "/api/v1/helix/v1/machines/"+app.ID+"/exec", `{"command":"sleep 100"}`)
		result <- code
	}()
	<-started
	if code, got := apiJSON(t, ts, opCookie, "POST", "/api/v1/helix/kill", `{"reason":"test","revoke_credentials":false}`); code != http.StatusOK || got["stopped_operations"].(float64) < 1 {
		t.Fatalf("kill %d %v", code, got)
	}
	select {
	case c := <-result:
		if c != http.StatusServiceUnavailable {
			t.Fatalf("stopped command answered %d", c)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the kill switch did not stop the command")
	}
	probes.mu.Lock()
	probes.block = nil
	probes.mu.Unlock()
	if code, _ := helixReq(t, ts, hlx, "GET", "/api/v1/helix/v1/machines", ""); code != http.StatusForbidden {
		t.Fatal("killed")
	}
	if code, _ := apiJSON(t, ts, opCookie, "POST", "/api/v1/helix/kill/release", `{}`); code != http.StatusForbidden {
		t.Fatal("Admin cannot release")
	}
	if code, _ := apiJSON(t, ts, cookie, "POST", "/api/v1/helix/kill/release", `{}`); code != http.StatusOK {
		t.Fatal("SuperAdmin releases")
	}
	if code, _ := helixReq(t, ts, hlx, "GET", "/api/v1/helix/v1/whoami", ""); code != http.StatusOK {
		t.Fatal("released")
	}

	// Revoking the credential ends it.
	_, view := apiJSON(t, ts, cookie, "GET", "/api/v1/helix", "")
	credID := view["credentials"].([]any)[0].(map[string]any)["id"].(string)
	apiJSON(t, ts, cookie, "POST", "/api/v1/helix/credentials/"+credID+"/revoke", `{}`)
	if code, _ := helixReq(t, ts, hlx, "GET", "/api/v1/helix/v1/whoami", ""); code != http.StatusUnauthorized {
		t.Fatal("revoked")
	}

	// Everything Helix did is in the activity log.
	_, act := apiJSON(t, ts, cookie, "GET", "/api/v1/helix/activity", "")
	seen := map[string]bool{}
	for _, it := range act["items"].([]any) {
		m := it.(map[string]any)
		seen[m["action"].(string)+":"+m["result"].(string)] = true
	}
	for _, want := range []string{"helix.exec:ok", "helix.exec:denied", "helix.exec:approved", "helix.approval.request:pending", "helix.exec:stopped", "helix.kill.engage:ok", "helix.credential.revoke:ok", "helix.auth:denied"} {
		if !seen[want] {
			t.Errorf("activity is missing %s (have %v)", want, seen)
		}
	}
}

func asString(v any) string {
	s, _ := v.(string)
	return s
}
