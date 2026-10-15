package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/no-dal/ndl-ce/internal/appdb"
)

func TestUptimeTargetsAndStatusRanges(t *testing.T) {
	cases := map[string]string{
		"https://shop.example.com/health": "http",
		"shop.example.com/health":         "http",
		"10.0.0.5:8080":                   "tcp",
		"tcp://db.example.com:5432":       "tcp",
	}
	for target, want := range cases {
		if kind, _, err := uptimeKind(target); err != nil || kind != want {
			t.Fatalf("%s: %s %v", target, kind, err)
		}
	}
	if _, _, err := uptimeKind("ftp://x"); err == nil {
		t.Fatal("ftp must be refused")
	}
	if !statusOK(204, "") || statusOK(502, "") || !statusOK(301, "200,301") || statusOK(404, "200-399") || !statusOK(404, "404") {
		t.Fatal("status ranges")
	}
	if validExpect("abc") || validExpect("700") || !validExpect("200-299,301") {
		t.Fatal("expect validation")
	}
}

func TestUptimeRestartsContainersUntilTheSiteWorks(t *testing.T) {
	mem, ts, cookie, clusterID, fd := opsReady(t)
	var healthy atomic.Bool
	site := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !healthy.Load() {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		_, _ = w.Write([]byte("welcome to the shop"))
	}))
	defer site.Close()

	code, out := ociCall(t, ts, cookie, "POST", "/uptime/monitors",
		`{"name":"shop","target":"`+site.URL+`/","keyword":"shop","containers":["apps/shop-web-1",{"machine":"apps","name":"shop-db-1"}]}`)
	if code != http.StatusCreated {
		t.Fatalf("create %d %v", code, out["_raw"])
	}
	id := out["id"].(string)

	code, out = ociCall(t, ts, cookie, "POST", "/uptime/monitors/"+id+"/check", "")
	if code != http.StatusOK || out["state"] != "down" {
		t.Fatalf("first check %d %v", code, out["_raw"])
	}
	got := strings.Join(fd.done(), ",")
	if !strings.Contains(got, "start shop-web-1") || !strings.Contains(got, "restart shop-db-1") {
		t.Fatalf("a down site must start or restart its containers: %s", got)
	}
	if len(fd.tunnelOp) != 1 {
		t.Fatalf("the stopped tunnel on the same machine must be started: %v", fd.tunnelOp)
	}
	u, _ := mem.GetUptimeMonitor(context.Background(), clusterID, id)
	if u.NextCheckAt == nil || u.NextCheckAt.Sub(*u.LastCheckAt) != uptimeFailingEvery {
		t.Fatalf("a down site is rechecked every minute: %v", u.NextCheckAt)
	}

	// Still down a minute later: no second restart inside the restart gap.
	before := len(fd.done())
	ociCall(t, ts, cookie, "POST", "/uptime/monitors/"+id+"/check", "")
	if len(fd.done()) != before {
		t.Fatal("containers must not be restarted again within five minutes")
	}

	healthy.Store(true)
	code, out = ociCall(t, ts, cookie, "POST", "/uptime/monitors/"+id+"/check", "")
	if code != http.StatusOK || out["state"] != "up" {
		t.Fatalf("recovery %d %v", code, out["_raw"])
	}
	u, _ = mem.GetUptimeMonitor(context.Background(), clusterID, id)
	if u.NextCheckAt.Sub(*u.LastCheckAt) != uptimeHealthyEvery {
		t.Fatal("a working site is checked every five minutes")
	}
	events := out["events"].([]any)
	if events[0].(map[string]any)["action"] != "recovered" {
		t.Fatalf("recovery must be recorded: %v", events[0])
	}
}

func TestUptimeSweepChecksDueMonitorsOnly(t *testing.T) {
	s, mem, _ := testServer(t)
	cluster, _ := mem.GetCluster(context.Background())
	var hits atomic.Int32
	site := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
	}))
	defer site.Close()
	later := time.Now().Add(time.Hour)
	for _, u := range []appdb.UptimeMonitor{
		{ID: uuid.NewString(), ClusterID: cluster.ID, Name: "due", Target: site.URL, Enabled: true},
		{ID: uuid.NewString(), ClusterID: cluster.ID, Name: "off", Target: site.URL, Enabled: false},
		{ID: uuid.NewString(), ClusterID: cluster.ID, Name: "later", Target: site.URL, Enabled: true, NextCheckAt: &later},
	} {
		if err := mem.PutUptimeMonitor(context.Background(), u); err != nil {
			t.Fatal(err)
		}
	}
	s.uptimeSweep(context.Background())
	if hits.Load() != 1 {
		t.Fatalf("only the due, enabled monitor is checked: %d", hits.Load())
	}
	list, _ := mem.ListUptimeMonitors(context.Background(), cluster.ID)
	for _, u := range list {
		if u.Name == "due" && (u.State != "up" || u.NextCheckAt == nil) {
			t.Fatalf("due monitor %+v", u)
		}
	}
}

func TestUptimeImportTestsThenSaves(t *testing.T) {
	s, mem, token := testServer(t)
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)
	site := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer site.Close()
	body := `{"monitors":[{"name":"one","target":"` + site.URL + `"},{"name":"two","target":"127.0.0.1:1","auto_restart":false}]}`
	code, out := ociCall(t, ts, cookie, "POST", "/uptime/import", `{"dry_run":true,"script":`+body+`}`)
	if code != http.StatusOK || out["valid"] != true {
		t.Fatalf("test %d %v", code, out["_raw"])
	}
	results := out["results"].([]any)
	one := results[0].(map[string]any)["check"].(map[string]any)
	two := results[1].(map[string]any)["check"].(map[string]any)
	if one["ok"] != true || two["ok"] != false {
		t.Fatalf("checks %v %v", one, two)
	}
	cluster, _ := mem.GetCluster(context.Background())
	if list, _ := mem.ListUptimeMonitors(context.Background(), cluster.ID); len(list) != 0 {
		t.Fatal("Test must not save")
	}
	code, out = ociCall(t, ts, cookie, "POST", "/uptime/import", `{"script":`+body+`}`)
	if code != http.StatusOK {
		t.Fatalf("import %d %v", code, out["_raw"])
	}
	list, _ := mem.ListUptimeMonitors(context.Background(), cluster.ID)
	if len(list) != 2 || list[1].AutoRestart {
		t.Fatalf("saved %+v", list)
	}
	// Importing again updates by name instead of duplicating.
	ociCall(t, ts, cookie, "POST", "/uptime/import", `{"script":{"monitors":[{"name":"one","target":"`+site.URL+`/v2"}],"delete":["two"]}}`)
	list, _ = mem.ListUptimeMonitors(context.Background(), cluster.ID)
	if len(list) != 1 || !strings.HasSuffix(list[0].Target, "/v2") {
		t.Fatalf("update by name %+v", list)
	}
	if code, _ := ociCall(t, ts, cookie, "POST", "/uptime/import", `{"script":{"monitors":[{"name":"bad","target":"ftp://x"}]}}`); code != http.StatusUnprocessableEntity {
		t.Fatalf("bad target %d", code)
	}
}

func TestSavedScriptsAndAutomationPolicies(t *testing.T) {
	_, ts, cookie, _, fd := opsReady(t)
	code, out := ociCall(t, ts, cookie, "POST", "/scripts", `{"kind":"docker","name":"nightly fix","body":"{\"steps\":[{\"action\":\"fix\"}]}"}`)
	if code != http.StatusOK {
		t.Fatalf("save %d %v", code, out["_raw"])
	}
	id := out["id"].(string)
	if code, _ := ociCall(t, ts, cookie, "POST", "/scripts", `{"kind":"docker","name":"nightly fix","body":"{}"}`); code != http.StatusConflict {
		t.Fatalf("duplicate name %d", code)
	}
	if code, _ := ociCall(t, ts, cookie, "POST", "/scripts", `{"kind":"docker","name":"x","body":"not json"}`); code != http.StatusBadRequest {
		t.Fatalf("invalid json %d", code)
	}
	_, list := ociCall(t, ts, cookie, "GET", "/scripts?kind=docker", "")
	if items := list["items"].([]any); len(items) != 1 {
		t.Fatalf("list %v", list["_raw"])
	}

	code, out = ociCall(t, ts, cookie, "POST", "/policies", `{"name":"recover shop","kind":"docker_script","target":"nightly fix"}`)
	if code != http.StatusCreated || out["target"] != "nightly fix" {
		t.Fatalf("policy %d %v", code, out["_raw"])
	}
	code, run := ociCall(t, ts, cookie, "POST", "/policies/"+out["id"].(string)+"/apply", "")
	if code != http.StatusOK || run["status"] != "succeeded" {
		t.Fatalf("apply %d %v", code, run["_raw"])
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && !strings.Contains(strings.Join(fd.done(), ","), "shop-web-1") {
		time.Sleep(20 * time.Millisecond)
	}
	if !strings.Contains(strings.Join(fd.done(), ","), "start shop-web-1") {
		t.Fatalf("the saved script must run from the policy: %v", fd.done())
	}
	if code, _ := ociCall(t, ts, cookie, "POST", "/policies", `{"name":"x","kind":"docker_script"}`); code != http.StatusUnprocessableEntity {
		t.Fatalf("docker_script without target %d", code)
	}
	code, out = ociCall(t, ts, cookie, "POST", "/policies", `{"name":"uptime","kind":"uptime_check"}`)
	if code != http.StatusCreated {
		t.Fatalf("uptime policy %d %v", code, out["_raw"])
	}
	_, run = ociCall(t, ts, cookie, "POST", "/policies/"+out["id"].(string)+"/apply", "")
	if run["status"] != "failed" || !strings.Contains(run["reason"].(string), "no enabled uptime monitors") {
		t.Fatalf("uptime policy with no monitors: %v", run["_raw"])
	}
	ociCall(t, ts, cookie, "DELETE", "/scripts/"+id, "")
	_, list = ociCall(t, ts, cookie, "GET", "/scripts?kind=docker", "")
	if items := list["items"].([]any); len(items) != 0 {
		t.Fatal("delete")
	}
}
