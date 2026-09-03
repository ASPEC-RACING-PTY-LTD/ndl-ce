package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
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

func TestCheckWebhookDestinationResolvesHostnames(t *testing.T) {
	lookupWebhookIPs = func(host string) ([]net.IP, error) {
		if host != "evil.example" {
			t.Fatalf("host %s", host)
		}
		return []net.IP{net.ParseIP("169.254.169.254")}, nil
	}
	t.Cleanup(func() { lookupWebhookIPs = net.LookupIP })
	if err := checkWebhookDestination("https://evil.example/hook"); err == nil {
		t.Fatal("hostname that resolves to link-local must be denied")
	}
	lookupWebhookIPs = func(string) ([]net.IP, error) {
		return []net.IP{net.ParseIP("10.1.2.3"), net.ParseIP("1.2.3.4")}, nil
	}
	if err := checkWebhookDestination("https://mixed.example/hook"); err == nil {
		t.Fatal("mixed private record must fail closed")
	}
	lookupWebhookIPs = func(string) ([]net.IP, error) {
		return []net.IP{net.ParseIP("1.2.3.4")}, nil
	}
	if err := checkWebhookDestination("https://ok.example/hook"); err != nil {
		t.Fatal(err)
	}
	if err := checkWebhookDestination("https://8.8.8.8/hook"); err != nil {
		t.Fatal(err)
	}
}

func TestNotifySkipsWebhookResolvedToPrivateIP(t *testing.T) {
	lookupWebhookIPs = func(string) ([]net.IP, error) {
		return []net.IP{net.ParseIP("169.254.169.254")}, nil
	}
	t.Cleanup(func() { lookupWebhookIPs = net.LookupIP })
	s, mem, _ := testServer(t)
	cluster, _ := mem.GetCluster(context.Background())
	var hits int
	hook := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		hits++
	}))
	defer hook.Close()
	s.HTTPClient = hook.Client()
	if err := mem.CreateNotificationChannel(context.Background(), appdb.NotificationChannel{
		ID: uuid.NewString(), ClusterID: cluster.ID, Name: "evil", Kind: appdb.NotifyWebhook,
		Status: appdb.NotifyConfigured, CreatedAt: time.Now().UTC(),
	}, "https://evil.example/hook", ""); err != nil {
		t.Fatal(err)
	}
	s.notify(context.Background(), cluster.ID, []byte(`{"metric":"cpu.busy_ratio"}`))
	if hits != 0 {
		t.Fatalf("private resolution must not POST: %d", hits)
	}
}

func TestWebhookClientDoesNotFollowRedirect(t *testing.T) {
	allowWebhookLoopbackForTest = true
	t.Cleanup(func() { allowWebhookLoopbackForTest = false })
	var privateHits int
	private := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		privateHits++
	}))
	defer private.Close()
	public := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, private.URL+"/secret", http.StatusFound)
	}))
	defer public.Close()
	req, err := http.NewRequest(http.MethodPost, public.URL, strings.NewReader(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	res, err := webhookHTTPClient().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if privateHits != 0 {
		t.Fatal("redirect to another origin must not be followed")
	}
	if res.StatusCode != http.StatusFound {
		t.Fatalf("status %d", res.StatusCode)
	}
}

type failInsertEventStore struct {
	appdb.Store
}

func (f failInsertEventStore) InsertEvent(context.Context, appdb.Event) error {
	return errors.New("persist failed")
}

type failUpdateAlertRuleFiredStore struct {
	appdb.Store
}

func (f failUpdateAlertRuleFiredStore) UpdateAlertRuleFired(context.Context, string, string, time.Time) error {
	return errors.New("persist failed")
}

func seedFiringAlert(t *testing.T, s *Server, mem *appdb.Memory, token string) (clusterID string, gotBody *[]byte, hook *httptest.Server) {
	t.Helper()
	allowWebhookLoopbackForTest = true
	t.Cleanup(func() { allowWebhookLoopbackForTest = false })
	cluster, _ := mem.GetCluster(context.Background())
	seedNode(t, mem, cluster.ID, debianInv(), false)
	body := []byte{}
	gotBody = &body
	hook = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*gotBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(204)
	}))
	t.Cleanup(hook.Close)
	s.HTTPClient = hook.Client()
	s.Observer = fakeObserver{res: metrics.QueryResult{
		Status: metrics.StatusAvailable,
		Series: []metrics.Series{{
			Name: metrics.MetricCPUBusyRatio, Status: metrics.StatusAvailable,
			Points: []metrics.Point{{Time: time.Now().UTC(), Value: 0.95}},
		}},
	}}
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close)
	admin := claimAdmin(t, ts, token)
	res := doCookie(t, ts, admin, "POST", "/api/v1/alerts/channels", `{"name":"hook","kind":"webhook","url":"`+hook.URL+`"}`)
	if res.StatusCode != http.StatusCreated {
		b, _ := io.ReadAll(res.Body)
		t.Fatalf("create channel %d %s", res.StatusCode, b)
	}
	_ = res.Body.Close()
	res = doCookie(t, ts, admin, "POST", "/api/v1/alerts", `{"name":"cpu","metric":"cpu.busy_ratio","op":"gt","threshold":0.8}`)
	if res.StatusCode != http.StatusCreated {
		b, _ := io.ReadAll(res.Body)
		t.Fatalf("create alert %d %s", res.StatusCode, b)
	}
	_ = res.Body.Close()
	return cluster.ID, gotBody, hook
}

func TestTickAlertsFailsClosedWhenEventPersistFails(t *testing.T) {
	s, mem, token := testServer(t)
	clusterID, gotBody, _ := seedFiringAlert(t, s, mem, token)
	s.Store = failInsertEventStore{Store: mem}
	s.TickAlerts(context.Background())
	if len(*gotBody) != 0 {
		t.Fatalf("webhook must not fire when event persist fails: %s", *gotBody)
	}
	ev, _ := mem.ListEvents(context.Background(), clusterID, 50)
	for _, e := range ev {
		if e.Type == "alert.firing" {
			t.Fatalf("event persist fail must not record firing: %+v", e)
		}
	}
	rules, _ := mem.ListAlertRules(context.Background(), clusterID)
	if len(rules) != 1 || rules[0].LastFiredAt != nil {
		t.Fatalf("last_fired_at must stay unset: %+v", rules)
	}
}

func TestTickAlertsFailsClosedWhenFiredPersistFails(t *testing.T) {
	s, mem, token := testServer(t)
	clusterID, gotBody, _ := seedFiringAlert(t, s, mem, token)
	s.Store = failUpdateAlertRuleFiredStore{Store: mem}
	s.TickAlerts(context.Background())
	if len(*gotBody) != 0 {
		t.Fatalf("webhook must not fire when last_fired_at persist fails: %s", *gotBody)
	}
	rules, _ := mem.ListAlertRules(context.Background(), clusterID)
	if len(rules) != 1 || rules[0].LastFiredAt != nil {
		t.Fatalf("GET /alerts must not show last_fired_at: %+v", rules)
	}
}
