package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
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
	"github.com/no-dal/ndl-ce/internal/cloudflare/cftest"
	"github.com/no-dal/ndl-ce/internal/docker"
	"github.com/no-dal/ndl-ce/internal/lxc"
	"github.com/no-dal/ndl-ce/internal/rbac"
)

const (
	cfAcctA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	cfAcctB = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	cfTokA  = "tokenAAAA-user-scoped-0001"
	cfTokB  = "tokenBBBB-account-owned-0002"
)

// cfDocker is a fake agent that runs connectors and reports local cloudflared services.
type cfDocker struct {
	dockerUnavailable
	mu        sync.Mutex
	requests  []docker.ConnectorRequest
	local     []docker.Tunnel
	reachable bool
}

func (d *cfDocker) DockerTunnels(context.Context, []docker.MachineHint) ([]docker.Tunnel, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]docker.Tunnel(nil), d.local...), nil
}

func (d *cfDocker) DockerTunnelUnit(context.Context, string, string, string, bool) error { return nil }

func (d *cfDocker) DockerConnector(_ context.Context, req docker.ConnectorRequest) (docker.ConnectorResult, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.requests = append(d.requests, req)
	switch req.Action {
	case docker.ConnectorProbe:
		if d.reachable {
			return docker.ConnectorResult{Reachable: true}, nil
		}
		return docker.ConnectorResult{Message: "nothing is listening on that address and port"}, nil
	case docker.ConnectorInstall:
		return docker.ConnectorResult{Unit: docker.ConnectorUnit(req.TunnelID), Version: "2026.10.1"}, nil
	}
	return docker.ConnectorResult{Unit: docker.ConnectorUnit(req.TunnelID)}, nil
}

type cfEnv struct {
	t       *testing.T
	s       *Server
	mem     *appdb.Memory
	fake    *cftest.Fake
	ts      *httptest.Server
	admin   string
	cluster string
	dock    *cfDocker
	public  map[string]uptimeResult
	pubMu   sync.Mutex
}

func newCFEnv(t *testing.T) *cfEnv {
	t.Helper()
	s, mem, token := testServer(t)
	f := cftest.New()
	t.Cleanup(f.Server.Close)
	f.Accounts[cfAcctA], f.Accounts[cfAcctB] = "Racing", "Hosting"
	all := []string{cftest.TunnelRead, cftest.TunnelWrite, cftest.ZoneRead, cftest.DNSRead, cftest.DNSWrite, cftest.AccountList}
	f.Tokens[cfTokA] = cftest.Token{Status: "active", Accounts: []string{cfAcctA}, Perms: all}
	f.Tokens[cfTokB] = cftest.Token{Status: "active", AccountOwned: true, Accounts: []string{cfAcctB}, Perms: all}
	s.CFBaseURL = f.URL()
	s.CFKeyPath = filepath.Join(t.TempDir(), "cloudflare.key")
	e := &cfEnv{t: t, s: s, mem: mem, fake: f, dock: &cfDocker{reachable: true}, public: map[string]uptimeResult{}}
	s.Docker = e.dock
	s.CFPublicCheck = func(_ context.Context, url string) uptimeResult {
		e.pubMu.Lock()
		defer e.pubMu.Unlock()
		if r, ok := e.public[url]; ok {
			return r
		}
		return uptimeResult{OK: true, Status: 200, LatencyMS: 12}
	}
	cfMoveCheckVerify = []time.Duration{time.Millisecond}
	e.ts = httptest.NewServer(s.Handler())
	t.Cleanup(e.ts.Close)
	e.admin = claimAdmin(t, e.ts, token)
	cl, _ := mem.GetCluster(context.Background())
	e.cluster = cl.ID
	return e
}

func (e *cfEnv) call(cookie, method, path, body string) (int, map[string]any, string) {
	e.t.Helper()
	res := authedJSON(e.t, e.ts, method, path, cookie, body, nil)
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	var out map[string]any
	_ = json.Unmarshal(raw, &out)
	return res.StatusCode, out, string(raw)
}

func (e *cfEnv) addCredentials() {
	e.t.Helper()
	if code, _, raw := e.call(e.admin, "POST", "/api/v1/cloudflare/credentials", `{"name":"racing","token":"`+cfTokA+`"}`); code != 201 {
		e.t.Fatalf("add A %d %s", code, raw)
	}
	if code, _, raw := e.call(e.admin, "POST", "/api/v1/cloudflare/credentials", `{"name":"hosting","token":"`+cfTokB+`","account_id":"`+cfAcctB+`"}`); code != 201 {
		e.t.Fatalf("add B %d %s", code, raw)
	}
}

func (e *cfEnv) inventory() cfInventory {
	e.t.Helper()
	code, _, raw := e.call(e.admin, "GET", "/api/v1/cloudflare/inventory?refresh=1", "")
	if code != 200 {
		e.t.Fatalf("inventory %d %s", code, raw)
	}
	var inv cfInventory
	if err := json.Unmarshal([]byte(raw), &inv); err != nil {
		e.t.Fatal(err)
	}
	return inv
}

func TestCloudflareCredentialsAreSealedAndNeverReturned(t *testing.T) {
	e := newCFEnv(t)
	e.addCredentials()
	code, _, raw := e.call(e.admin, "GET", "/api/v1/cloudflare/credentials", "")
	if code != 200 || strings.Contains(raw, cfTokA) || strings.Contains(raw, cfTokB) || !strings.Contains(raw, `"token_last4":"0001"`) {
		t.Fatalf("list must not reveal tokens: %s", raw)
	}
	if !strings.Contains(raw, `"account_owned":true`) || !strings.Contains(raw, `"name":"Hosting"`) {
		t.Fatalf("account token must be recognised: %s", raw)
	}
	creds, _ := e.mem.ListCloudflareCredentials(context.Background(), e.cluster)
	for _, c := range creds {
		sealed, _ := e.mem.GetCloudflareToken(context.Background(), e.cluster, c.ID)
		if strings.Contains(sealed, "token") || !strings.HasPrefix(sealed, "ndlcf1:") {
			t.Fatalf("token stored in the clear: %q", sealed)
		}
	}
	events := e.mem.Audits()
	for _, ev := range events {
		if strings.Contains(string(ev.Detail), "token") && strings.Contains(string(ev.Detail), "0001") {
			t.Fatalf("audit leaked a token: %s", ev.Detail)
		}
	}
	// A bad token is not saved.
	if code, _, raw := e.call(e.admin, "POST", "/api/v1/cloudflare/credentials", `{"name":"bad","token":"nope"}`); code != 424 || !strings.Contains(raw, "not saved") {
		t.Fatalf("invalid token: %d %s", code, raw)
	}
	if n := len(creds); n != 2 {
		t.Fatalf("credentials %d", n)
	}
}

func TestCloudflareRotateValidateAndRemove(t *testing.T) {
	e := newCFEnv(t)
	e.addCredentials()
	creds, _ := e.mem.ListCloudflareCredentials(context.Background(), e.cluster)
	var racing appdb.CloudflareCredential
	for _, c := range creds {
		if c.Name == "racing" {
			racing = c
		}
	}
	// A narrower token is refused unless confirmed.
	e.fake.Tokens["narrow"] = cftest.Token{Status: "active", Accounts: []string{cfAcctB}, Perms: []string{cftest.TunnelRead, cftest.AccountList}}
	if code, _, raw := e.call(e.admin, "PUT", "/api/v1/cloudflare/credentials/"+racing.ID, `{"token":"narrow"}`); code != 409 || !strings.Contains(raw, "Racing") {
		t.Fatalf("narrower rotation must ask: %d %s", code, raw)
	}
	e.fake.Tokens["tokenAAAA-rotated-9999"] = e.fake.Tokens[cfTokA]
	if code, out, raw := e.call(e.admin, "PUT", "/api/v1/cloudflare/credentials/"+racing.ID, `{"token":"tokenAAAA-rotated-9999"}`); code != 200 || out["token_last4"] != "9999" {
		t.Fatalf("rotate %d %s", code, raw)
	}
	// An expired token is marked by validation.
	e.fake.Mu.Lock()
	tok := e.fake.Tokens["tokenAAAA-rotated-9999"]
	tok.Status = "expired"
	e.fake.Tokens["tokenAAAA-rotated-9999"] = tok
	e.fake.Mu.Unlock()
	if code, out, raw := e.call(e.admin, "POST", "/api/v1/cloudflare/credentials/"+racing.ID+"/validate", ""); code != 200 || out["status"] != cfStatusInvalid {
		t.Fatalf("validate expired %d %s", code, raw)
	}
	tun := e.fake.AddTunnel(cfAcctA, "keepme")
	if code, out, _ := e.call(e.admin, "DELETE", "/api/v1/cloudflare/credentials/"+racing.ID, ""); code != 200 || !strings.Contains(out["note"].(string), "unchanged") {
		t.Fatal("delete")
	}
	if e.fake.Tunnels[tun.ID].Deleted {
		t.Fatal("removing a credential must not touch Cloudflare")
	}
}

func TestCloudflareUnifiedInventoryKeepsAccountsApart(t *testing.T) {
	e := newCFEnv(t)
	zA := e.fake.AddZone(cfAcctA, "racing.example")
	e.fake.AddZone(cfAcctB, "hosting.example")
	tA := e.fake.AddTunnel(cfAcctA, "racing-shared", map[string]any{"hostname": "shop.racing.example", "service": "http://192.168.2.38:3000"})
	tB := e.fake.AddTunnel(cfAcctB, "hosting-shared", map[string]any{"hostname": "app.hosting.example", "service": "http://localhost:8080"})
	e.fake.AddRecord(zA, "shop.racing.example", "CNAME", tA.ID+".cfargotunnel.com", true)
	e.fake.AddRecord(zA, "old.racing.example", "CNAME", "deadbeef-0000-0000-0000-000000000000.cfargotunnel.com", true)
	e.dock.local = []docker.Tunnel{{MachineID: "ct-1", MachineName: "apps", Unit: "cloudflared.service", Active: true, TunnelID: tB.ID}}
	e.addCredentials()
	inv := e.inventory()
	if len(inv.Accounts) != 2 || len(inv.Tunnels) != 2 || len(inv.Routes) != 2 {
		t.Fatalf("inventory %+v", inv)
	}
	for _, r := range inv.Routes {
		switch r.Hostname {
		case "shop.racing.example":
			if r.AccountName != "Racing" || r.DNS == nil || r.DNS.Action != "ok" || !r.Editable {
				t.Fatalf("shop %+v", r)
			}
		case "app.hosting.example":
			if r.AccountName != "Hosting" || r.DNS == nil || r.DNS.Action != "create" {
				t.Fatalf("app %+v", r)
			}
		}
	}
	for _, tv := range inv.Tunnels {
		if tv.ID == tB.ID && (len(tv.Local) != 1 || tv.Local[0].MachineName != "apps") {
			t.Fatalf("local connector must attach by tunnel id: %+v", tv.Local)
		}
	}
	unknown := false
	for _, d := range inv.DNS {
		if d.Record.Name == "old.racing.example" && d.Status == "unknown-tunnel" {
			unknown = true
		}
	}
	if !unknown {
		t.Fatalf("a CNAME to a deleted tunnel must be flagged: %+v", inv.DNS)
	}
	// Each account was only ever called with its own token.
	e.fake.Mu.Lock()
	defer e.fake.Mu.Unlock()
	if len(e.fake.Refused) > 0 {
		t.Fatalf("cross-account calls %v", e.fake.Refused)
	}
}

func TestCloudflareRouteCreateChecksDNSAndRollsBack(t *testing.T) {
	e := newCFEnv(t)
	zA := e.fake.AddZone(cfAcctA, "racing.example")
	e.fake.AddZone(cfAcctB, "hosting.example")
	tA := e.fake.AddTunnel(cfAcctA, "shared")
	other := e.fake.AddTunnel(cfAcctA, "other", map[string]any{"hostname": "taken.racing.example", "service": "http://10.0.0.9:80"})
	e.fake.AddRecord(zA, "mail.racing.example", "A", "203.0.113.5", false)
	e.fake.AddRecord(zA, "moved.racing.example", "CNAME", other.ID+".cfargotunnel.com", true)
	e.addCredentials()
	route := func(host, extra string) (int, map[string]any, string) {
		return e.call(e.admin, "POST", "/api/v1/cloudflare/routes",
			`{"account_id":"`+cfAcctA+`","tunnel_id":"`+tA.ID+`","hostname":"`+host+`","protocol":"http","host":"192.168.2.38","port":3000`+extra+`}`)
	}
	code, out, raw := route("Shop.Racing.Example", `,"options":{"no_tls_verify":true,"http_host_header":"shop.internal"}`)
	if code != 200 || out["dns_action"] != "create" {
		t.Fatalf("create %d %s", code, raw)
	}
	recs := e.fake.RecordsNamed("shop.racing.example")
	if len(recs) != 1 || recs[0].Content != tA.ID+".cfargotunnel.com" || !recs[0].Proxied || !strings.HasPrefix(recs[0].Comment, "Managed by No-dal") {
		t.Fatalf("dns %+v", recs)
	}
	ing := e.fake.Ingress(tA.ID)
	if ing[0]["service"] != "http://192.168.2.38:3000" || ing[0]["originRequest"].(map[string]any)["httpHostHeader"] != "shop.internal" {
		t.Fatalf("ingress %v", ing)
	}
	// An unrelated record is never overwritten and nothing is written.
	if code, _, raw = route("mail.racing.example", ""); code != 409 || !strings.Contains(raw, "Nothing was changed") {
		t.Fatalf("conflict %d %s", code, raw)
	}
	if len(e.fake.Ingress(tA.ID)) != 2 || e.fake.RecordsNamed("mail.racing.example")[0].Content != "203.0.113.5" {
		t.Fatal("a conflicting route must leave config and DNS untouched")
	}
	// A hostname on another tunnel needs Move or explicit replacement.
	if code, _, raw = route("moved.racing.example", ""); code != 409 || !strings.Contains(raw, "other") {
		t.Fatalf("retarget %d %s", code, raw)
	}
	if code, _, raw = route("taken.racing.example", ""); code != 409 || !strings.Contains(raw, "Move") {
		t.Fatalf("hostname routed elsewhere %d %s", code, raw)
	}
	// Cross-account: the zone is in Hosting, the tunnel in Racing.
	if code, _, raw = route("x.hosting.example", ""); code != 422 || !strings.Contains(raw, "1014") {
		t.Fatalf("cross account %d %s", code, raw)
	}
	// A DNS failure undoes the new route.
	before := len(e.fake.Ingress(tA.ID))
	e.fake.Mu.Lock()
	tok := e.fake.Tokens[cfTokA]
	tok.Perms = []string{cftest.TunnelRead, cftest.TunnelWrite, cftest.ZoneRead, cftest.DNSRead, cftest.AccountList}
	e.fake.Tokens[cfTokA] = tok
	e.fake.Mu.Unlock()
	if code, _, raw = route("new.racing.example", ""); code != 424 || !strings.Contains(raw, "removed again") {
		t.Fatalf("dns failure %d %s", code, raw)
	}
	if len(e.fake.Ingress(tA.ID)) != before {
		t.Fatal("the route must be rolled back when DNS fails")
	}
	// A token that cannot read DNS (tunnel permissions only) still edits
	// routes; DNS is left alone and the reply says why.
	e.fake.Mu.Lock()
	tok = e.fake.Tokens[cfTokA]
	tok.Perms = []string{cftest.TunnelRead, cftest.TunnelWrite, cftest.ZoneRead, cftest.AccountList}
	e.fake.Tokens[cfTokA] = tok
	e.fake.Mu.Unlock()
	if code, _, raw := e.call(e.admin, "POST", "/api/v1/cloudflare/routes/plan", `{"account_id":"`+cfAcctA+`","tunnel_id":"`+tA.ID+`","hostname":"tunnel-only.racing.example"}`); code != 200 || !strings.Contains(raw, `"unavailable"`) {
		t.Fatalf("plan without DNS read %d %s", code, raw)
	}
	code, out, raw = route("tunnel-only.racing.example", "")
	if code != 200 || out["dns_action"] != "unavailable" || !strings.Contains(raw, "DNS was not changed") {
		t.Fatalf("route without DNS read %d %s", code, raw)
	}
	if len(e.fake.RecordsNamed("tunnel-only.racing.example")) != 0 {
		t.Fatal("DNS must not be touched without DNS permission")
	}
	// Unsafe origins are refused.
	code, _, _ = e.call(e.admin, "POST", "/api/v1/cloudflare/routes", `{"account_id":"`+cfAcctA+`","tunnel_id":"`+tA.ID+`","hostname":"meta.racing.example","protocol":"http","host":"169.254.169.254","port":80}`)
	if code != 400 {
		t.Fatalf("metadata origin must be refused: %d", code)
	}
}

func TestCloudflareDisableEnableDelete(t *testing.T) {
	e := newCFEnv(t)
	zA := e.fake.AddZone(cfAcctA, "racing.example")
	tA := e.fake.AddTunnel(cfAcctA, "shared", map[string]any{"hostname": "shop.racing.example", "service": "http://10.0.0.2:80", "originRequest": map[string]any{"noTLSVerify": true}})
	e.fake.AddRecord(zA, "shop.racing.example", "CNAME", tA.ID+".cfargotunnel.com", true)
	e.addCredentials()
	ref := `"account_id":"` + cfAcctA + `","tunnel_id":"` + tA.ID + `","hostname":"shop.racing.example"`
	if code, _, raw := e.call(e.admin, "POST", "/api/v1/cloudflare/routes/enabled", `{`+ref+`,"enabled":false}`); code != 200 {
		t.Fatalf("disable %d %s", code, raw)
	}
	if len(e.fake.Ingress(tA.ID)) != 1 {
		t.Fatal("a disabled route is removed from the tunnel")
	}
	inv := e.inventory()
	if len(inv.Routes) != 1 || inv.Routes[0].Enabled {
		t.Fatalf("a disabled route stays listed: %+v", inv.Routes)
	}
	if code, _, raw := e.call(e.admin, "POST", "/api/v1/cloudflare/routes/enabled", `{`+ref+`,"enabled":true}`); code != 200 {
		t.Fatalf("enable %d %s", code, raw)
	}
	ing := e.fake.Ingress(tA.ID)
	if len(ing) != 2 || ing[0]["originRequest"].(map[string]any)["noTLSVerify"] != true {
		t.Fatalf("re-enabled route must be restored exactly: %v", ing)
	}
	if code, _, _ := e.call(e.admin, "POST", "/api/v1/cloudflare/routes/delete", `{`+ref+`,"delete_dns":true,"confirm":"wrong"}`); code != 400 {
		t.Fatal("delete needs the hostname typed")
	}
	if code, out, raw := e.call(e.admin, "POST", "/api/v1/cloudflare/routes/delete", `{`+ref+`,"delete_dns":true,"confirm":"shop.racing.example"}`); code != 200 || out["note"] != "DNS record removed" {
		t.Fatalf("delete %d %s", code, raw)
	}
	if len(e.fake.RecordsNamed("shop.racing.example")) != 0 || len(e.fake.Ingress(tA.ID)) != 1 {
		t.Fatal("route and its own CNAME must be gone")
	}
}

func TestCloudflareMoveVerifiesThenRemovesAndRollsBack(t *testing.T) {
	e := newCFEnv(t)
	zA := e.fake.AddZone(cfAcctA, "racing.example")
	src := e.fake.AddTunnel(cfAcctA, "per-site", map[string]any{"hostname": "a.racing.example", "service": "http://192.168.2.10:80"},
		map[string]any{"hostname": "b.racing.example", "service": "http://192.168.2.11:80"}, map[string]any{"hostname": "c.racing.example", "service": "http://localhost:80"})
	dest := e.fake.AddTunnel(cfAcctA, "shared")
	for _, h := range []string{"a", "b", "c"} {
		e.fake.AddRecord(zA, h+".racing.example", "CNAME", src.ID+".cfargotunnel.com", true)
	}
	e.dock.local = []docker.Tunnel{{MachineID: "ct-src", MachineName: "old", Unit: "cloudflared.service", Active: true, TunnelID: src.ID},
		{MachineID: docker.HostMachineID, MachineName: "Host", Unit: docker.ConnectorUnit(dest.ID), Active: true, TunnelID: dest.ID}}
	e.addCredentials()
	e.public["https://b.racing.example"] = uptimeResult{Status: 502}
	refs := func(hosts ...string) string {
		var parts []string
		for _, h := range hosts {
			parts = append(parts, `{"account_id":"`+cfAcctA+`","tunnel_id":"`+src.ID+`","hostname":"`+h+`.racing.example"}`)
		}
		return `{"routes":[` + strings.Join(parts, ",") + `],"dest_account_id":"` + cfAcctA + `","dest_tunnel_id":"` + dest.ID + `"`
	}
	code, out, raw := e.call(e.admin, "POST", "/api/v1/cloudflare/move/preview", refs("a", "b", "c")+`}`)
	if code != 200 {
		t.Fatalf("preview %d %s", code, raw)
	}
	items := out["items"].([]any)
	if items[2].(map[string]any)["ok"] != false || !strings.Contains(raw, "localhost on the old connector") {
		t.Fatalf("a localhost origin on another machine must block: %s", raw)
	}
	if code, _, _ = e.call(e.admin, "POST", "/api/v1/cloudflare/move", refs("a", "b")+`}`); code != 400 {
		t.Fatal("moving needs confirmation")
	}
	code, out, raw = e.call(e.admin, "POST", "/api/v1/cloudflare/move", refs("a", "b")+`,"confirm":true}`)
	if code != 202 {
		t.Fatalf("move %d %s", code, raw)
	}
	job := e.s.opsJobs.get(out["id"].(string))
	for i := 0; i < 400; i++ {
		job.mu.Lock()
		done := !job.FinishedAt.IsZero()
		job.mu.Unlock()
		if done {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	logText := fmt.Sprint(job.json()["lines"])
	hosts := func(id string) map[string]bool {
		m := map[string]bool{}
		for _, r := range e.fake.Ingress(id) {
			if h, _ := r["hostname"].(string); h != "" {
				m[h] = true
			}
		}
		return m
	}
	srcHosts, destHosts := hosts(src.ID), hosts(dest.ID)
	if srcHosts["a.racing.example"] || !destHosts["a.racing.example"] {
		t.Fatalf("a must move: src=%v dest=%v\n%s", srcHosts, destHosts, logText)
	}
	if rec := e.fake.RecordsNamed("a.racing.example")[0]; rec.Content != dest.ID+".cfargotunnel.com" {
		t.Fatalf("a DNS %+v", rec)
	}
	if !srcHosts["b.racing.example"] || destHosts["b.racing.example"] || e.fake.RecordsNamed("b.racing.example")[0].Content != src.ID+".cfargotunnel.com" {
		t.Fatalf("b failed its public check and must be rolled back: src=%v dest=%v\n%s", srcHosts, destHosts, logText)
	}
	if e.fake.Tunnels[src.ID].Deleted || !strings.Contains(logText, "old tunnels were kept") {
		t.Fatal("the source tunnel must never be deleted")
	}
}

func TestCloudflarePublishWorkloadAndConnector(t *testing.T) {
	e := newCFEnv(t)
	e.fake.AddZone(cfAcctA, "racing.example")
	tA := e.fake.AddTunnel(cfAcctA, "shared")
	e.addCredentials()
	ctx := context.Background()
	wid := uuid.NewString()
	_ = e.mem.CreateWorkload(ctx, appdb.Workload{ID: wid, ClusterID: e.cluster, Name: "vault", Kind: lxc.KindSystemContainer, Status: lxc.StatusRunning})
	_ = e.mem.CreateWorkloadNIC(ctx, appdb.WorkloadNIC{ID: uuid.NewString(), ClusterID: e.cluster, WorkloadID: wid, IPv4: "192.168.2.117"})
	code, out, raw := e.call(e.admin, "GET", "/api/v1/workloads/"+wid+"/cloudflare", "")
	if code != 200 || out["current_ip"] != "192.168.2.117" || len(out["routes"].([]any)) != 0 {
		t.Fatalf("nothing is published by default: %d %s", code, raw)
	}
	code, _, raw = e.call(e.admin, "POST", "/api/v1/workloads/"+wid+"/cloudflare/publish",
		`{"account_id":"`+cfAcctA+`","tunnel_id":"`+tA.ID+`","hostname":"vault.racing.example","protocol":"http","port":8080}`)
	if code != 200 {
		t.Fatalf("publish %d %s", code, raw)
	}
	if ing := e.fake.Ingress(tA.ID); ing[0]["service"] != "http://192.168.2.117:8080" {
		t.Fatalf("origin must be the workload address: %v", ing)
	}
	code, out, _ = e.call(e.admin, "GET", "/api/v1/workloads/"+wid+"/cloudflare", "")
	if len(out["routes"].([]any)) != 1 || out["preferred"].(map[string]any)["tunnel_id"] != tA.ID {
		t.Fatalf("published route and preference must show: %v", out)
	}
	// The workload moves to a new address: the route is flagged and can be updated.
	nics, _ := e.mem.ListWorkloadNICs(ctx, e.cluster, wid)
	nics[0].IPv4 = "192.168.2.200"
	_ = e.mem.UpdateWorkloadNIC(ctx, nics[0])
	e.s.cfInvalidate(e.cluster)
	inv := e.inventory()
	if !inv.Routes[0].IPChanged {
		t.Fatalf("an IP change must be detected: %+v", inv.Routes[0])
	}
	if code, _, raw = e.call(e.admin, "POST", "/api/v1/cloudflare/routes/sync-ip", `{"account_id":"`+cfAcctA+`","tunnel_id":"`+tA.ID+`","hostname":"vault.racing.example"}`); code != 200 {
		t.Fatalf("sync %d %s", code, raw)
	}
	if ing := e.fake.Ingress(tA.ID); ing[0]["service"] != "http://192.168.2.200:8080" {
		t.Fatalf("route must follow the new address: %v", ing)
	}
	// Deploy a connector: the token goes only to the agent.
	code, _, raw = e.call(e.admin, "POST", "/api/v1/cloudflare/tunnels/"+tA.ID+"/connectors", `{"account_id":"`+cfAcctA+`","machine_id":"`+wid+`","action":"install"}`)
	if code != 200 || strings.Contains(raw, "eyFAKE") {
		t.Fatalf("install %d %s", code, raw)
	}
	e.dock.mu.Lock()
	sent := e.dock.requests[len(e.dock.requests)-1]
	e.dock.mu.Unlock()
	if sent.Token != e.fake.Tunnels[tA.ID].Token || sent.MachineID != wid {
		t.Fatalf("agent request %+v", sent)
	}
	conns, _ := e.mem.ListCloudflareConnectors(ctx, e.cluster)
	if len(conns) != 1 || conns[0].Unit != docker.ConnectorUnit(tA.ID) {
		t.Fatalf("connector record %+v", conns)
	}
	if code, _, _ = e.call(e.admin, "POST", "/api/v1/cloudflare/tunnels/"+tA.ID+"/connectors", `{"account_id":"`+cfAcctA+`","machine_id":"`+wid+`","action":"remove"}`); code != 400 {
		t.Fatal("removing a connector needs confirmation")
	}
	// The host's own tunnel is never touched without an Owner's approval.
	e.dock.mu.Lock()
	before := len(e.dock.requests)
	e.dock.mu.Unlock()
	for _, body := range []string{
		`{"account_id":"` + cfAcctA + `","machine_id":"host","action":"install"}`,
		`{"account_id":"` + cfAcctA + `","action":"update"}`,
		`{"account_id":"` + cfAcctA + `","machine_id":"host","action":"restart","unit":"cloudflared.service"}`,
	} {
		code, _, raw = e.call(e.admin, "POST", "/api/v1/cloudflare/tunnels/"+tA.ID+"/connectors", body)
		if code != 409 || !strings.Contains(raw, hostTunnelCode) || !strings.Contains(raw, "systemctl status") {
			t.Fatalf("host change without approval: %d %s", code, raw)
		}
	}
	e.dock.mu.Lock()
	after := len(e.dock.requests)
	e.dock.mu.Unlock()
	if after != before {
		t.Fatal("the agent was asked to change the host's tunnel without approval")
	}
	code, _, raw = e.call(e.admin, "POST", "/api/v1/cloudflare/tunnels/"+tA.ID+"/connectors", `{"account_id":"`+cfAcctA+`","machine_id":"host","action":"install","approve_host":true}`)
	if code != 200 {
		t.Fatalf("an Owner's approved host install: %d %s", code, raw)
	}
	e.dock.mu.Lock()
	sent = e.dock.requests[len(e.dock.requests)-1]
	e.dock.mu.Unlock()
	if !sent.HostApproved {
		t.Fatal("the approval must reach the agent")
	}
}

func TestCloudflareTunnelLifecycleAndDiagnostics(t *testing.T) {
	e := newCFEnv(t)
	zA := e.fake.AddZone(cfAcctA, "racing.example")
	e.addCredentials()
	code, out, raw := e.call(e.admin, "POST", "/api/v1/cloudflare/tunnels", `{"account_id":"`+cfAcctA+`","name":"new-shared"}`)
	if code != 201 {
		t.Fatalf("create %d %s", code, raw)
	}
	id := out["id"].(string)
	if code, _, _ = e.call(e.admin, "PATCH", "/api/v1/cloudflare/tunnels/"+id, `{"account_id":"`+cfAcctA+`","name":"renamed"}`); code != 200 || e.fake.Tunnels[id].Name != "renamed" {
		t.Fatal("rename")
	}
	busy := e.fake.AddTunnel(cfAcctA, "busy", map[string]any{"hostname": "x.racing.example", "service": "http://10.0.0.1:80"})
	e.fake.AddRecord(zA, "x.racing.example", "CNAME", busy.ID+".cfargotunnel.com", true)
	if code, _, raw = e.call(e.admin, "POST", "/api/v1/cloudflare/tunnels/"+busy.ID+"/delete", `{"account_id":"`+cfAcctA+`","confirm":"busy"}`); code != 409 || !strings.Contains(raw, "route") {
		t.Fatalf("a tunnel in use must not be deleted: %d %s", code, raw)
	}
	if code, _, _ = e.call(e.admin, "POST", "/api/v1/cloudflare/tunnels/"+id+"/delete", `{"account_id":"`+cfAcctA+`","confirm":"nope"}`); code != 400 {
		t.Fatal("delete needs the name typed")
	}
	if code, _, raw = e.call(e.admin, "POST", "/api/v1/cloudflare/tunnels/"+id+"/delete", `{"account_id":"`+cfAcctA+`","confirm":"renamed"}`); code != 200 || !e.fake.Tunnels[id].Deleted {
		t.Fatalf("delete %d %s", code, raw)
	}
	e.dock.reachable = false
	e.public["https://x.racing.example"] = uptimeResult{Status: 502}
	code, out, raw = e.call(e.admin, "POST", "/api/v1/cloudflare/routes/diagnose", `{"account_id":"`+cfAcctA+`","tunnel_id":"`+busy.ID+`","hostname":"x.racing.example"}`)
	if code != 200 || !strings.Contains(out["summary"].(string), "Internal service") {
		t.Fatalf("diagnose %d %s", code, raw)
	}
	e.dock.mu.Lock()
	probe := e.dock.requests[len(e.dock.requests)-1]
	e.dock.mu.Unlock()
	if probe.Action != docker.ConnectorProbe || probe.Host != "10.0.0.1" || probe.Port != 80 {
		t.Fatalf("only the route's own origin is probed: %+v", probe)
	}
}

func TestCloudflarePermissionsAndRateLimits(t *testing.T) {
	e := newCFEnv(t)
	e.fake.AddZone(cfAcctA, "racing.example")
	tA := e.fake.AddTunnel(cfAcctA, "shared")
	e.addCredentials()
	viewer := loginRole(t, e.ts, e.mem, "cfviewer", rbac.Viewer)
	operator := loginRole(t, e.ts, e.mem, "cfoperator", rbac.Operator)
	if code, _, _ := e.call(viewer, "GET", "/api/v1/cloudflare/inventory", ""); code != 200 {
		t.Fatal("viewers can see the inventory")
	}
	if code, _, _ := e.call(viewer, "POST", "/api/v1/cloudflare/tunnels", `{"account_id":"`+cfAcctA+`","name":"x"}`); code != 403 {
		t.Fatal("viewers cannot create tunnels")
	}
	if code, _, _ := e.call(operator, "POST", "/api/v1/cloudflare/credentials", `{"name":"x","token":"`+cfTokA+`"}`); code != 403 {
		t.Fatal("operators cannot add tokens by default")
	}
	if code, _, _ := e.call(operator, "POST", "/api/v1/cloudflare/tunnels", `{"account_id":"`+cfAcctA+`","name":"op-tunnel"}`); code != 201 {
		t.Fatal("operators manage tunnels")
	}
	e.inventory()
	e.fake.Mu.Lock()
	e.fake.FailNext, e.fake.FailStatus = 1, 429
	e.fake.Mu.Unlock()
	code, _, raw := e.call(e.admin, "PATCH", "/api/v1/cloudflare/tunnels/"+tA.ID, `{"account_id":"`+cfAcctA+`","name":"renamed"}`)
	if code != 429 || !strings.Contains(raw, "retry in 42s") {
		t.Fatalf("rate limits must surface: %d %s", code, raw)
	}
}

func TestCloudflarePermissionsGuideFallsBackAndCaches(t *testing.T) {
	e := newCFEnv(t)
	hits := 0
	docs := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		http.Error(w, "down", http.StatusServiceUnavailable)
	}))
	defer docs.Close()
	e.s.CFDocsURL = docs.URL
	viewer := loginRole(t, e.ts, e.mem, "guideviewer", rbac.Viewer)
	code, out, raw := e.call(viewer, "GET", "/api/v1/cloudflare/permissions", "")
	if code != 200 || out["source"] != "built-in" || !strings.Contains(raw, "dns_edit") {
		t.Fatalf("guide %d %s", code, raw)
	}
	before := hits
	if code, _, _ := e.call(viewer, "GET", "/api/v1/cloudflare/permissions", ""); code != 200 || hits != before {
		t.Fatalf("a failed read must be cached for a while: %d hits", hits-before)
	}
}

func TestCloudflareInventoryAnswersNotModified(t *testing.T) {
	e := newCFEnv(t)
	e.fake.AddZone(cfAcctA, "racing.example")
	e.fake.AddTunnel(cfAcctA, "shared", map[string]any{"hostname": "shop.racing.example", "service": "http://10.0.0.2:80"})
	e.addCredentials()
	res := authedJSON(t, e.ts, "GET", "/api/v1/cloudflare/inventory", e.admin, "", nil)
	_ = res.Body.Close()
	etag := res.Header.Get("ETag")
	if res.StatusCode != 200 || etag == "" {
		t.Fatalf("first read %d %q", res.StatusCode, etag)
	}
	res = authedJSON(t, e.ts, "GET", "/api/v1/cloudflare/inventory", e.admin, "", map[string]string{"If-None-Match": etag})
	_ = res.Body.Close()
	if res.StatusCode != http.StatusNotModified {
		t.Fatalf("unchanged inventory must be 304, got %d", res.StatusCode)
	}
}

func TestCloudflareDNSAccessIsTriedEveryRefreshAndReported(t *testing.T) {
	e := newCFEnv(t)
	var zones []string
	for _, n := range []string{"a.example", "b.example", "c.example", "d.example", "e.example"} {
		zones = append(zones, e.fake.AddZone(cfAcctA, n))
	}
	tA := e.fake.AddTunnel(cfAcctA, "shared", map[string]any{"hostname": "shop.a.example", "service": "http://10.0.0.2:80"})
	e.fake.AddRecord(zones[0], "shop.a.example", "CNAME", tA.ID+".cfargotunnel.com", true)
	// A tunnel-only token, like one made with Zero Trust permissions.
	e.fake.Mu.Lock()
	tok := e.fake.Tokens[cfTokA]
	tok.Perms = []string{cftest.TunnelRead, cftest.TunnelWrite, cftest.ZoneRead, cftest.AccountList}
	e.fake.Tokens[cfTokA] = tok
	e.fake.Mu.Unlock()
	if code, _, raw := e.call(e.admin, "POST", "/api/v1/cloudflare/credentials", `{"name":"racing","token":"`+cfTokA+`"}`); code != 201 {
		t.Fatalf("add %d %s", code, raw)
	}
	dnsCalls := func() int {
		e.fake.Mu.Lock()
		defer e.fake.Mu.Unlock()
		n := 0
		for k, v := range e.fake.Calls {
			if strings.HasPrefix(k, "GET /zones/") && strings.HasSuffix(k, "/dns_records") {
				n += v
			}
		}
		return n
	}
	before := dnsCalls()
	inv := e.inventory()
	a := inv.Accounts[0]
	if a.DNSAccess != "denied" || !strings.Contains(a.DNSReason, "DNS Read") || a.DNSUnreadable != 5 {
		t.Fatalf("denied DNS must be reported: %+v", a)
	}
	// A few zones are tried, not all of them.
	if n := dnsCalls() - before; n == 0 || n > cfDNSProbes {
		t.Fatalf("dns calls %d", n)
	}
	// The permission is added in Cloudflare; the next refresh uses it without
	// checking the token again, and the token's card catches up.
	e.fake.Mu.Lock()
	tok.Perms = append(tok.Perms, cftest.DNSRead, cftest.DNSWrite)
	e.fake.Tokens[cfTokA] = tok
	e.fake.Mu.Unlock()
	inv = e.inventory()
	a = inv.Accounts[0]
	if a.DNSAccess != "ok" || a.DNSUnreadable != 0 {
		t.Fatalf("DNS should be read now: %+v", a)
	}
	var shop *cfRouteView
	for i := range inv.Routes {
		if inv.Routes[i].Hostname == "shop.a.example" {
			shop = &inv.Routes[i]
		}
	}
	if shop == nil || shop.DNS == nil || shop.DNS.Action != "ok" {
		t.Fatalf("route DNS %+v", shop)
	}
	_, _, raw := e.call(e.admin, "GET", "/api/v1/cloudflare/credentials", "")
	if !strings.Contains(raw, `"dns":true`) || !strings.Contains(raw, `"status":"active"`) {
		t.Fatalf("credential scope not updated: %s", raw)
	}
}
