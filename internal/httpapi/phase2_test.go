package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/no-dal/ndl-ce/internal/appdb"
	"github.com/no-dal/ndl-ce/internal/auth"
	"github.com/no-dal/ndl-ce/internal/inventory"
	"github.com/no-dal/ndl-ce/internal/metrics"
	"github.com/no-dal/ndl-ce/internal/rbac"
)

type fakeObserver struct {
	res metrics.QueryResult
	err error
}

func (f fakeObserver) GetMetrics(context.Context, time.Time, time.Time) (metrics.QueryResult, error) {
	return f.res, f.err
}

func seedNode(t *testing.T, mem *appdb.Memory, clusterID string, inv inventory.Inventory, stale bool) appdb.Node {
	t.Helper()
	node := appdb.Node{ID: uuid.NewString(), ClusterID: clusterID, Name: "local", HostPlatform: json.RawMessage(`{}`)}
	if err := mem.UpsertNode(context.Background(), node); err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(inv)
	if err != nil {
		t.Fatal(err)
	}
	if err := mem.UpsertInventory(context.Background(), appdb.HardwareInventory{
		NodeID: node.ID, ClusterID: clusterID, Payload: body, ObservedAt: time.Now().UTC(), Stale: stale,
	}); err != nil {
		t.Fatal(err)
	}
	return node
}

func loginAs(t *testing.T, ts *httptest.Server, user, pass string) string {
	t.Helper()
	res, err := ts.Client().Post(ts.URL+"/api/v1/auth/login", "application/json", strings.NewReader(
		`{"username":"`+user+`","password":"`+pass+`"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatalf("login=%d", res.StatusCode)
	}
	for _, c := range res.Cookies() {
		if c.Name == sessionCookie {
			return c.Value
		}
	}
	t.Fatal("no cookie")
	return ""
}

func TestPhase2InventoryAndRBAC(t *testing.T) {
	s, mem, token := testServer(t)
	cluster, _ := mem.GetCluster(context.Background())
	inv := inventory.Inventory{
		SchemaVersion: inventory.SchemaVersion,
		ObservedAt:    time.Now().UTC(),
		Host:          inventory.Host{Status: inventory.StatusAvailable, ID: "debian", VersionID: "13", PrettyName: "Debian GNU/Linux 13"},
		CPU:           inventory.CPU{Status: inventory.StatusAvailable, Model: "Test CPU", Cores: 4, Threads: 8},
		Memory:        inventory.Memory{Status: inventory.StatusAvailable, TotalBytes: 8 << 30, DIMMStatus: inventory.StatusNotReported},
		BlockDevices:  []inventory.BlockDevice{{Name: "sda", Serial: "SECRETDISK", SMARTStatus: inventory.StatusNotReported}},
		Firmware:      inventory.Firmware{Status: inventory.StatusAvailable, ProductSerial: "BOARDSERIAL"},
		Capabilities:  []inventory.Capability{{ID: "kvm", Status: inventory.StatusUnavailable}},
	}
	node := seedNode(t, mem, cluster.ID, inv, false)
	s.Observer = fakeObserver{res: metrics.QueryResult{Status: metrics.StatusCollecting, Series: []metrics.Series{}}}

	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	claim, err := ts.Client().Post(ts.URL+"/api/v1/setup/claim", "application/json", strings.NewReader(
		`{"token":"`+token+`","username":"admin","password":"correct-horse"}`))
	if err != nil {
		t.Fatal(err)
	}
	if claim.StatusCode != 200 {
		t.Fatalf("claim=%d", claim.StatusCode)
	}
	var adminCookie string
	for _, c := range claim.Cookies() {
		if c.Name == sessionCookie {
			adminCookie = c.Value
		}
	}
	_ = claim.Body.Close()

	req, _ := http.NewRequest("GET", ts.URL+"/api/v1/nodes/"+node.ID+"/hardware", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: adminCookie})
	res, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != 200 {
		t.Fatalf("hardware=%d", res.StatusCode)
	}
	var hw map[string]any
	if err := json.NewDecoder(res.Body).Decode(&hw); err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	raw, _ := json.Marshal(hw["inventory"])
	if !strings.Contains(string(raw), "SECRETDISK") {
		t.Fatal("admin should see disk serial")
	}

	hash, err := auth.HashPassword("password1")
	if err != nil {
		t.Fatal(err)
	}
	viewer := appdb.User{ID: uuid.NewString(), ClusterID: cluster.ID, Username: "view", PasswordHash: hash}
	if err := mem.CreateUser(context.Background(), viewer); err != nil {
		t.Fatal(err)
	}
	if err := mem.BindRole(context.Background(), cluster.ID, viewer.ID, rbac.Viewer); err != nil {
		t.Fatal(err)
	}
	viewCookie := loginAs(t, ts, "view", "password1")
	vreq, _ := http.NewRequest("GET", ts.URL+"/api/v1/nodes/"+node.ID+"/hardware", nil)
	vreq.AddCookie(&http.Cookie{Name: sessionCookie, Value: viewCookie})
	vres, err := ts.Client().Do(vreq)
	if err != nil {
		t.Fatal(err)
	}
	if vres.StatusCode != 200 {
		t.Fatalf("viewer hardware=%d", vres.StatusCode)
	}
	var vhw map[string]any
	if err := json.NewDecoder(vres.Body).Decode(&vhw); err != nil {
		t.Fatal(err)
	}
	_ = vres.Body.Close()
	vraw, _ := json.Marshal(vhw["inventory"])
	if strings.Contains(string(vraw), "SECRETDISK") || strings.Contains(string(vraw), "BOARDSERIAL") {
		t.Fatal("viewer must not receive serials")
	}

	mreq, _ := http.NewRequest("GET", ts.URL+"/api/v1/nodes/"+node.ID+"/metrics", nil)
	mreq.AddCookie(&http.Cookie{Name: sessionCookie, Value: viewCookie})
	mres, err := ts.Client().Do(mreq)
	if err != nil {
		t.Fatal(err)
	}
	if mres.StatusCode != 200 {
		t.Fatalf("viewer metrics=%d", mres.StatusCode)
	}
	_ = mres.Body.Close()
}

func TestStaleMetricsAreNotZeros(t *testing.T) {
	s, mem, token := testServer(t)
	cluster, _ := mem.GetCluster(context.Background())
	inv := inventory.Inventory{SchemaVersion: inventory.SchemaVersion, Host: inventory.Host{Status: inventory.StatusAvailable, ID: "debian"}}
	node := seedNode(t, mem, cluster.ID, inv, true)
	s.Observer = fakeObserver{res: metrics.QueryResult{
		Status: metrics.StatusAvailable,
		Series: []metrics.Series{{Name: "cpu.busy_ratio", Status: metrics.StatusAvailable, Points: []metrics.Point{{Time: time.Now(), Value: 0}}}},
	}}
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	claim, _ := ts.Client().Post(ts.URL+"/api/v1/setup/claim", "application/json", strings.NewReader(
		`{"token":"`+token+`","username":"admin","password":"correct-horse"}`))
	var cookie string
	for _, c := range claim.Cookies() {
		if c.Name == sessionCookie {
			cookie = c.Value
		}
	}
	_ = claim.Body.Close()

	req, _ := http.NewRequest("GET", ts.URL+"/api/v1/nodes/"+node.ID+"/metrics", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var body metrics.QueryResult
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Status != metrics.StatusStale {
		t.Fatalf("status=%s", body.Status)
	}
	if len(body.Series) != 0 {
		t.Fatal("stale metrics must not return invented series")
	}
}

func TestTasksAndEvents(t *testing.T) {
	s, mem, token := testServer(t)
	cluster, _ := mem.GetCluster(context.Background())
	done := 100
	_ = mem.UpsertOperation(context.Background(), appdb.Operation{
		ID: uuid.NewString(), ClusterID: cluster.ID, Kind: "inventory.refresh", State: "succeeded",
		IdempotencyKey: "inventory.refresh", Progress: &done, Stage: "collected",
	})
	_ = mem.InsertEvent(context.Background(), appdb.Event{
		ID: uuid.NewString(), ClusterID: cluster.ID, Type: "inventory.updated", Payload: json.RawMessage(`{}`),
	})
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	claim, _ := ts.Client().Post(ts.URL+"/api/v1/setup/claim", "application/json", strings.NewReader(
		`{"token":"`+token+`","username":"admin","password":"correct-horse"}`))
	var cookie string
	for _, c := range claim.Cookies() {
		if c.Name == sessionCookie {
			cookie = c.Value
		}
	}
	_ = claim.Body.Close()

	for _, path := range []string{"/api/v1/tasks", "/api/v1/events"} {
		req, _ := http.NewRequest("GET", ts.URL+path, nil)
		req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
		res, err := ts.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		if res.StatusCode != 200 {
			t.Fatalf("%s=%d", path, res.StatusCode)
		}
		_ = res.Body.Close()
	}
}

func TestPhase2RequiresAuth(t *testing.T) {
	s, _, _ := testServer(t)
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	res, err := ts.Client().Get(ts.URL + "/api/v1/nodes")
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauth nodes=%d", res.StatusCode)
	}
	_ = res.Body.Close()
}
