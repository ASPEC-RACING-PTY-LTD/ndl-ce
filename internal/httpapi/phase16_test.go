package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/no-dal/ndl-ce/internal/appdb"
	"github.com/no-dal/ndl-ce/internal/inventory"
	"github.com/no-dal/ndl-ce/internal/journald"
	"github.com/no-dal/ndl-ce/internal/metrics"
	"github.com/no-dal/ndl-ce/internal/rbac"
)

type fakeLogs struct {
	res journald.Result
	err error
}

func (f fakeLogs) GetLogs(context.Context, string, int, time.Time) (journald.Result, error) {
	return f.res, f.err
}

func TestStaleMetricsStillNotZeros(t *testing.T) {
	TestStaleMetricsAreNotZeros(t)
}

func TestLogsUnavailableAreEmpty(t *testing.T) {
	s, mem, token := testServer(t)
	cluster, _ := mem.GetCluster(context.Background())
	node := seedNode(t, mem, cluster.ID, debianInv(), false)
	s.Logs = fakeLogs{res: journald.Result{Status: journald.StatusUnavailable, Unit: journald.UnitAgent, Lines: []string{}}}
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)
	res := doCookie(t, ts, cookie, "GET", "/api/v1/nodes/"+node.ID+"/logs?unit=ndl-agent.service", "")
	defer res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatalf("status %d", res.StatusCode)
	}
	var body journald.Result
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Status != journald.StatusUnavailable || len(body.Lines) != 0 {
		t.Fatalf("must not invent logs: %+v", body)
	}
	res = doCookie(t, ts, cookie, "GET", "/api/v1/nodes/"+node.ID+"/logs?unit=syslog.service", "")
	if res.StatusCode != http.StatusUnprocessableEntity {
		b, _ := io.ReadAll(res.Body)
		t.Fatalf("bad unit %d %s", res.StatusCode, b)
	}
	_ = res.Body.Close()
}

func TestSMARTHonestNotReported(t *testing.T) {
	s, mem, token := testServer(t)
	cluster, _ := mem.GetCluster(context.Background())
	inv := debianInv()
	inv.BlockDevices = []inventory.BlockDevice{{Name: "sda", Serial: "SECRETDISK", SMARTStatus: inventory.StatusNotReported}}
	node := seedNode(t, mem, cluster.ID, inv, false)
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)
	res := doCookie(t, ts, cookie, "GET", "/api/v1/nodes/"+node.ID+"/smart", "")
	defer res.Body.Close()
	var body struct {
		Items []map[string]any `json:"items"`
	}
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if len(body.Items) != 1 || body.Items[0]["smart_status"] != string(inventory.StatusNotReported) {
		t.Fatalf("%+v", body.Items)
	}
	raw, _ := json.Marshal(body)
	if strings.Contains(string(raw), "SECRETDISK") {
		t.Fatal("serial leaked")
	}
}

func TestCapacityCollectingWithoutSamples(t *testing.T) {
	s, mem, token := testServer(t)
	cluster, _ := mem.GetCluster(context.Background())
	node := seedNode(t, mem, cluster.ID, debianInv(), false)
	s.Observer = fakeObserver{res: metrics.QueryResult{Status: metrics.StatusCollecting, Series: []metrics.Series{
		{Name: metrics.MetricStorageAvailBytes, Status: metrics.StatusCollecting, Points: []metrics.Point{}},
	}}}
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)
	res := doCookie(t, ts, cookie, "GET", "/api/v1/nodes/"+node.ID+"/capacity", "")
	defer res.Body.Close()
	var body map[string]any
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body["status"] != "collecting" {
		t.Fatalf("%+v", body)
	}
	if _, ok := body["hours_to_zero"]; ok {
		t.Fatal("must not invent a forecast")
	}
}

func TestTimelineIncludesEventsAndAudit(t *testing.T) {
	s, mem, token := testServer(t)
	cluster, _ := mem.GetCluster(context.Background())
	_ = mem.InsertEvent(context.Background(), appdb.Event{
		ID: uuid.NewString(), ClusterID: cluster.ID, Type: "inventory.updated", CreatedAt: time.Now().UTC(),
	})
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)
	res := doCookie(t, ts, cookie, "GET", "/api/v1/timeline", "")
	defer res.Body.Close()
	var body struct {
		Items []map[string]any `json:"items"`
	}
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	var hasEvent, hasAudit bool
	for _, item := range body.Items {
		if item["kind"] == "event" {
			hasEvent = true
		}
		if item["kind"] == "audit" {
			hasAudit = true
		}
	}
	if !hasEvent {
		t.Fatal("timeline missing events")
	}
	if !hasAudit {
		t.Fatal("admin timeline missing audit")
	}
}

func TestAlertWebhookRejectsSSRFTargets(t *testing.T) {
	s, _, token := testServer(t)
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	admin := claimAdmin(t, ts, token)
	denied := []string{
		`{"name":"meta","kind":"webhook","url":"http://169.254.169.254/latest/meta-data"}`,
		`{"name":"loop","kind":"webhook","url":"http://127.0.0.1:9/hook"}`,
		`{"name":"local","kind":"webhook","url":"http://localhost/hook"}`,
		`{"name":"ten","kind":"webhook","url":"http://10.1.2.3/hook"}`,
		`{"name":"priv","kind":"webhook","url":"http://192.168.1.1/hook"}`,
		`{"name":"mid","kind":"webhook","url":"http://172.16.0.8/hook"}`,
		`{"name":"ll","kind":"webhook","url":"http://169.254.1.1/hook"}`,
		`{"name":"v6ll","kind":"webhook","url":"http://[fe80::1]/hook"}`,
		`{"name":"v6ula","kind":"webhook","url":"http://[fd00::1]/hook"}`,
		`{"name":"v6loop","kind":"webhook","url":"http://[::1]/hook"}`,
	}
	for _, body := range denied {
		res := doCookie(t, ts, admin, "POST", "/api/v1/alerts/channels", body)
		if res.StatusCode != http.StatusUnprocessableEntity {
			b, _ := io.ReadAll(res.Body)
			_ = res.Body.Close()
			t.Fatalf("ssrf %s -> %d %s", body, res.StatusCode, b)
		}
		_ = res.Body.Close()
	}
}

func TestAlertWebhookSecretAndViewerDeny(t *testing.T) {
	allowWebhookLoopbackForTest = true
	t.Cleanup(func() { allowWebhookLoopbackForTest = false })
	s, mem, token := testServer(t)
	cluster, _ := mem.GetCluster(context.Background())
	seedNode(t, mem, cluster.ID, debianInv(), false)
	var gotBody []byte
	hook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(204)
	}))
	defer hook.Close()
	s.HTTPClient = hook.Client()
	s.Observer = fakeObserver{res: metrics.QueryResult{
		Status: metrics.StatusAvailable,
		Series: []metrics.Series{{
			Name: metrics.MetricCPUBusyRatio, Status: metrics.StatusAvailable,
			Points: []metrics.Point{{Time: time.Now().UTC(), Value: 0.95}},
		}},
	}}
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	admin := claimAdmin(t, ts, token)
	view := loginRole(t, ts, mem, "view", rbac.Viewer)

	res := doCookie(t, ts, view, "POST", "/api/v1/alerts", `{"name":"cpu","metric":"cpu.busy_ratio","op":"gt","threshold":0.8}`)
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("viewer create %d", res.StatusCode)
	}
	_ = res.Body.Close()

	res = doCookie(t, ts, admin, "POST", "/api/v1/alerts/channels", `{"name":"hook","kind":"webhook","url":"file:///etc/passwd"}`)
	if res.StatusCode != http.StatusUnprocessableEntity {
		b, _ := io.ReadAll(res.Body)
		t.Fatalf("file webhook %d %s", res.StatusCode, b)
	}
	_ = res.Body.Close()

	res = doCookie(t, ts, admin, "POST", "/api/v1/alerts/channels", `{"name":"hook","kind":"webhook","url":"`+hook.URL+`"}`)
	if res.StatusCode != http.StatusCreated {
		b, _ := io.ReadAll(res.Body)
		t.Fatalf("create channel %d %s", res.StatusCode, b)
	}
	listed, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if strings.Contains(string(listed), hook.URL) {
		t.Fatal("webhook url leaked on create")
	}

	res = doCookie(t, ts, admin, "POST", "/api/v1/alerts/channels", `{"name":"mail","kind":"smtp"}`)
	var smtp map[string]any
	if err := json.NewDecoder(res.Body).Decode(&smtp); err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if smtp["status"] != appdb.NotifyNotConfigured {
		t.Fatalf("smtp %+v", smtp)
	}

	res = doCookie(t, ts, admin, "POST", "/api/v1/alerts", `{"name":"cpu","metric":"cpu.busy_ratio","op":"gt","threshold":0.8}`)
	if res.StatusCode != http.StatusCreated {
		b, _ := io.ReadAll(res.Body)
		t.Fatalf("create alert %d %s", res.StatusCode, b)
	}
	_ = res.Body.Close()

	s.TickAlerts(context.Background())
	ev, _ := mem.ListEvents(context.Background(), cluster.ID, 50)
	var fired bool
	for _, e := range ev {
		if e.Type == "alert.firing" {
			fired = true
			if strings.Contains(string(e.Payload), hook.URL) {
				t.Fatal("event leaked webhook url")
			}
		}
	}
	if !fired {
		t.Fatal("alert did not fire")
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && len(gotBody) == 0 {
		time.Sleep(10 * time.Millisecond)
	}
	if !strings.Contains(string(gotBody), `"metric":"cpu.busy_ratio"`) {
		t.Fatalf("webhook body %s", gotBody)
	}
	if strings.Contains(string(gotBody), hook.URL) {
		t.Fatal("webhook body leaked url")
	}

	res = doCookie(t, ts, view, "GET", "/api/v1/alerts", "")
	if res.StatusCode != 200 {
		t.Fatalf("viewer list %d", res.StatusCode)
	}
	_ = res.Body.Close()
	res = doCookie(t, ts, view, "GET", "/api/v1/timeline", "")
	var tl struct {
		Items []map[string]any `json:"items"`
	}
	if err := json.NewDecoder(res.Body).Decode(&tl); err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	for _, item := range tl.Items {
		if item["kind"] == "audit" {
			t.Fatal("viewer must not receive audit on timeline")
		}
	}
}
