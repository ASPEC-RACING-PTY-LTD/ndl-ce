package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/no-dal/ndl-ce/internal/appdb"
	"github.com/no-dal/ndl-ce/internal/features"
	"github.com/no-dal/ndl-ce/internal/shield"
)

// fakeShield is the agent side: it keeps the last applied spec and reports
// counters the test sets.
type fakeShield struct {
	mu      sync.Mutex
	spec    *shield.Spec
	calls   []string
	rules   map[string]shield.RuleStatus
	bans    []shield.Ban
	applied int
	now     time.Time
}

func (f *fakeShield) Shield(_ context.Context, action string, spec *shield.Spec, addr string, until int64) (shield.Status, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, strings.TrimSpace(action+" "+addr))
	switch action {
	case "apply":
		cp := *spec
		if err := cp.Normalize(); err != nil {
			return shield.Status{}, err
		}
		f.spec = &cp
		f.applied++
	case "disable":
		f.spec = nil
	case "ban":
		if f.spec.Bans == nil {
			f.spec.Bans = map[string]int64{}
		}
		f.spec.Bans[addr] = until
	case "unban":
		delete(f.spec.Bans, addr)
	}
	st := shield.Status{SampledAt: f.now, Bans: f.bans}
	if f.spec != nil {
		st.Active, st.SpecHash, st.Mode, st.Rules = true, f.spec.Hash(), f.spec.Mode, f.rules
	}
	return st, nil
}

func (f *fakeShield) rule(id string) *shield.Rule {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.spec == nil {
		return nil
	}
	for _, r := range f.spec.Rules {
		if r.ID == id {
			r := r
			return &r
		}
	}
	return nil
}

func TestDDoSProtectsGameServersWorkloadsAndCustomTargets(t *testing.T) {
	ctx := context.Background()
	s, mem, token := testServer(t)
	fs := &fakeShield{now: time.Now()}
	s.Shield = fs
	cluster, _ := mem.GetCluster(ctx)
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)

	if code, _ := jsonCall(t, ts, cookie, "GET", "/api/v1/ddos", ""); code != http.StatusNotFound {
		t.Fatalf("ddos while off = %d", code)
	}
	_ = mem.UpsertFeature(ctx, appdb.Feature{ClusterID: cluster.ID, ID: features.IDGameServers, Enabled: true, UpdatedAt: time.Now()})
	ports, _ := json.Marshal([]map[string]any{
		{"name": "game", "container_port": 27015, "host_port": 27015, "protocol": "udp", "primary": true},
		{"name": "rcon", "container_port": 27015, "host_port": 27016, "protocol": "tcp"},
	})
	gsID := uuid.NewString()
	if err := mem.CreateGameServer(ctx, appdb.GameServer{ID: gsID, ClusterID: cluster.ID, Name: "JvS", Family: "source", PortsJSON: ports}); err != nil {
		t.Fatal(err)
	}
	if code, out := jsonCall(t, ts, cookie, "POST", "/api/v1/features/ddos/enable", `{}`); code != http.StatusOK {
		t.Fatalf("enable %d %v", code, out)
	}

	// The game server is protected at once with the Source profile.
	auto := ddosAutoPrefix + gsID
	r := fs.rule(auto)
	if r == nil || !r.Host || r.Protocol != shield.ProtoBoth || strings.Join(r.Ports, ",") != "27015,27016" ||
		r.Limits.QueryPerSource != 5 || !r.Limits.DropFragments || r.Action != shield.ActionBan {
		t.Fatalf("auto rule = %+v", r)
	}
	if !fs.spec.TrustLocal || !fs.spec.Hardening || !fs.spec.DropInvalidTCP || fs.spec.Mode != shield.ModeEnforce {
		t.Fatalf("defaults = %+v", fs.spec)
	}

	// Saving the automatic target stores it with the change.
	code, out := jsonCall(t, ts, cookie, "PUT", "/api/v1/ddos/targets/"+auto, `{"name":"JvS","profile":"strict","allow":["198.51.100.7"]}`)
	if code != http.StatusOK {
		t.Fatalf("customize %d %v", code, out)
	}
	var stored ddosTarget
	cfg, _ := s.loadDDoS(ctx, cluster.ID)
	for _, tg := range cfg.Targets {
		if tg.RefID == gsID {
			stored = tg
		}
	}
	if stored.ID == "" || stored.Profile != shield.ProfileStrict {
		t.Fatalf("stored = %+v", cfg.Targets)
	}
	if fs.rule(auto) != nil {
		t.Fatal("the automatic rule stays next to the stored one")
	}
	if r = fs.rule(stored.ID); r == nil || r.Limits.PerSourcePPS != 200 || strings.Join(r.Allow, ",") != "198.51.100.7/32" || strings.Join(r.Ports, ",") != "27015,27016" {
		t.Fatalf("customized rule = %+v", r)
	}

	// A workload is protected by its address.
	wlID := uuid.NewString()
	_ = mem.CreateWorkload(ctx, appdb.Workload{ID: wlID, ClusterID: cluster.ID, Name: "DeceasedCraft", Kind: "system-container"})
	_ = mem.CreateWorkloadNIC(ctx, appdb.WorkloadNIC{ID: uuid.NewString(), ClusterID: cluster.ID, WorkloadID: wlID, IPv4: "192.168.2.31"})
	code, out = jsonCall(t, ts, cookie, "POST", "/api/v1/ddos/targets", `{"kind":"workload","ref_id":"`+wlID+`","name":"DeceasedCraft","profile":"minecraft","protocol":"tcp","ports":["25565"]}`)
	if code != http.StatusOK {
		t.Fatalf("workload %d %v", code, out)
	}
	var wlTarget string
	for _, tg := range out["targets"].([]any) {
		m := tg.(map[string]any)
		if m["ref_id"] == wlID {
			wlTarget = m["id"].(string)
		}
	}
	if r = fs.rule(wlTarget); r == nil || strings.Join(r.Dest, ",") != "192.168.2.31/32" || r.Limits.ConnsPerSource != 6 {
		t.Fatalf("workload rule = %+v", r)
	}
	if code, _ = jsonCall(t, ts, cookie, "POST", "/api/v1/ddos/targets", `{"kind":"workload","ref_id":"`+wlID+`","profile":"web"}`); code != http.StatusBadRequest {
		t.Fatalf("second target for one workload = %d", code)
	}

	// Custom targets are validated before they are stored.
	for _, bad := range []string{
		`{"kind":"custom","name":"x","dest":["10.0.0.0/33"]}`,
		`{"kind":"custom","name":"x","dest":["10.0.0.1"],"protocol":"udp","ports":["70000"]}`,
		`{"kind":"custom","name":"x"}`,
		`{"kind":"host","name":"x"}`,
		`{"kind":"custom","name":"x","dest":["10.0.0.1"],"profile":"nope"}`,
		`{"kind":"custom","name":"x","dest":["10.0.0.1"],"limits":{"per_source_pps":-1}}`,
	} {
		if code, _ := jsonCall(t, ts, cookie, "POST", "/api/v1/ddos/targets", bad); code != http.StatusBadRequest {
			t.Errorf("%s = %d", bad, code)
		}
	}
	code, out = jsonCall(t, ts, cookie, "POST", "/api/v1/ddos/targets", `{"kind":"host","name":"Panel","profile":"web","ports":["8080","443"]}`)
	if code != http.StatusOK {
		t.Fatalf("host %d %v", code, out)
	}

	// Settings: monitor mode, a trusted network and a blocked one.
	code, out = jsonCall(t, ts, cookie, "PUT", "/api/v1/ddos/settings", `{"mode":"monitor","trusted":["203.0.113.4"],"blocked":["198.18.0.0/15"],"attack_pps":500}`)
	if code != http.StatusOK {
		t.Fatalf("settings %d %v", code, out)
	}
	if fs.spec.Mode != shield.ModeMonitor || strings.Join(fs.spec.Trusted, ",") != "203.0.113.4/32" || strings.Join(fs.spec.Blocked, ",") != "198.18.0.0/15" {
		t.Fatalf("settings spec = %+v", fs.spec)
	}
	if code, _ = jsonCall(t, ts, cookie, "PUT", "/api/v1/ddos/settings", `{"mode":"panic"}`); code != http.StatusBadRequest {
		t.Fatalf("bad mode = %d", code)
	}

	// Bans go to the agent at once and stay in the spec for later applies.
	if code, out = jsonCall(t, ts, cookie, "POST", "/api/v1/ddos/bans", `{"addr":"198.51.100.9","minutes":30,"reason":"booter"}`); code != http.StatusOK {
		t.Fatalf("ban %d %v", code, out)
	}
	if !strings.Contains(strings.Join(fs.calls, "\n"), "ban 198.51.100.9") || fs.spec.Bans["198.51.100.9"] == 0 {
		t.Fatalf("ban not sent: %v %+v", fs.calls, fs.spec.Bans)
	}
	if code, _ = jsonCall(t, ts, cookie, "POST", "/api/v1/ddos/bans", `{"addr":"not-an-ip"}`); code != http.StatusBadRequest {
		t.Fatal("bad ban address accepted")
	}
	if code, _ = jsonCall(t, ts, cookie, "DELETE", "/api/v1/ddos/bans/198.51.100.9", ""); code != http.StatusOK {
		t.Fatal("unban failed")
	}
	if !strings.Contains(strings.Join(fs.calls, "\n"), "unban 198.51.100.9") {
		t.Fatalf("unban not sent: %v", fs.calls)
	}

	// Deleting the stored game server target hands it back to the default.
	if code, _ = jsonCall(t, ts, cookie, "DELETE", "/api/v1/ddos/targets/"+stored.ID, ""); code != http.StatusOK {
		t.Fatal("delete failed")
	}
	if fs.rule(auto) == nil {
		t.Fatal("the game server lost its default protection")
	}
	// Turning automatic game server protection off leaves it unprotected.
	jsonCall(t, ts, cookie, "PUT", "/api/v1/ddos/settings", `{"auto_game_servers":false}`)
	if fs.rule(auto) != nil {
		t.Fatal("automatic protection stayed on")
	}

	// Turning the feature off removes the rules.
	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/features/ddos/disable", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK || fs.spec != nil {
		t.Fatalf("disable %d, spec %+v", res.StatusCode, fs.spec)
	}
	if cfg, _ := s.loadDDoS(ctx, cluster.ID); len(cfg.Targets) != 2 {
		t.Fatal("turning it off dropped the settings")
	}
}

func TestDDoSRecordsAndNotifiesAttacks(t *testing.T) {
	ctx := context.Background()
	s, mem, token := testServer(t)
	cluster, _ := mem.GetCluster(ctx)
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	claimAdmin(t, ts, token)
	cfg := defaultDDoSConfig()
	targets := []ddosResolved{{ddosTarget: ddosTarget{ID: "t1", Name: "JvS"}}}
	at := time.Now()
	tick := func(in, drop uint64, bans int) {
		at = at.Add(ddosTick)
		s.sampleDDoS(ctx, cluster.ID, cfg, targets, shield.Status{SampledAt: at, Rules: map[string]shield.RuleStatus{
			"t1": {In: shield.Counters{Packets: in}, Dropped: shield.Counters{Packets: drop, Bytes: drop * 60}, Bans: bans,
				Reasons: map[string]shield.Counters{"query": {Packets: drop}}},
		}})
	}
	tick(0, 0, 0)
	tick(15_000, 0, 0) // 1000 pps in, nothing dropped
	if evs, _ := mem.ListDDoSEvents(ctx, cluster.ID, 10); len(evs) != 0 {
		t.Fatal("quiet traffic was an attack")
	}
	tick(15_000+1_500_000, 1_500_000, 230) // 100k pps dropped
	evs, _ := mem.ListDDoSEvents(ctx, cluster.ID, 10)
	if len(evs) != 1 || evs[0].PeakPPS != 100_000 || evs[0].TopReason != "query" || evs[0].Sources != 230 || evs[0].EndedAt != nil {
		t.Fatalf("attack = %+v", evs)
	}
	for i := 0; i < ddosQuietTicks; i++ {
		tick(15_000+1_500_000+uint64(i+1)*15_000, 1_500_000, 230)
	}
	evs, _ = mem.ListDDoSEvents(ctx, cluster.ID, 10)
	if len(evs) != 1 || evs[0].EndedAt == nil || evs[0].DroppedPackets != 1_500_000 {
		t.Fatalf("attack did not end: %+v", evs)
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		users, _ := mem.ListUsers(ctx, cluster.ID)
		kinds := map[string]bool{}
		for _, u := range users {
			items, _ := mem.ListNotifications(ctx, cluster.ID, u.ID, false, 50)
			for _, n := range items {
				kinds[n.Kind] = true
			}
		}
		if kinds[noteDDoSAttack] && kinds[noteDDoSAttackEnded] {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("notifications = %v", kinds)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
