package httpapi

import (
	"bytes"
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
	"github.com/no-dal/ndl-ce/internal/ndnet"
	"github.com/no-dal/ndl-ce/internal/rbac"
	"github.com/no-dal/ndl-ce/internal/secutil"
)

type fakeNet struct {
	preview ndnet.Preview
	apply   ndnet.ApplyResult
	obs     ndnet.Observation
	err     error
}

func (f fakeNet) DryRunNetwork(context.Context, ndnet.Spec) (ndnet.Preview, error) {
	return f.preview, f.err
}
func (f fakeNet) ApplyNetwork(context.Context, ndnet.Spec) (ndnet.ApplyResult, error) {
	return f.apply, f.err
}
func (f fakeNet) GetNetworks(context.Context, []ndnet.Hint) (ndnet.Observation, error) {
	return f.obs, nil
}

func claimAdmin(t *testing.T, ts *httptest.Server, token string) string {
	t.Helper()
	res, err := ts.Client().Post(ts.URL+"/api/v1/setup/claim", "application/json", strings.NewReader(
		`{"token":"`+token+`","username":"admin","password":"correct-horse"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	for _, c := range res.Cookies() {
		if c.Name == sessionCookie {
			return c.Value
		}
	}
	t.Fatal("no session")
	return ""
}

func TestNetworkCreateIsolated(t *testing.T) {
	s, mem, token := testServer(t)
	cluster, _ := mem.GetCluster(context.Background())
	_ = mem.UpsertNode(context.Background(), appdb.Node{ID: uuid.NewString(), ClusterID: cluster.ID, Name: "local"})
	id := uuid.NewString()
	s.Network = fakeNet{
		preview: ndnet.Preview{NetworkID: id, Kind: ndnet.KindIsolated, Danger: ndnet.DangerSafe, DHCP: true, DryRun: true, ManagementIfIndex: 2},
		apply:   ndnet.ApplyResult{NetworkID: id, Kind: ndnet.KindIsolated, Status: ndnet.StatusAvailable, BridgeName: "ndldeadbeef", DHCP: true, ManagementIfIndex: 2},
	}
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)
	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/networks", strings.NewReader(`{"name":"guests","kind":"isolated"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != http.StatusCreated {
		b, _ := io.ReadAll(res.Body)
		t.Fatalf("create %d %s", res.StatusCode, b)
	}
	var created map[string]any
	_ = json.NewDecoder(res.Body).Decode(&created)
	_ = res.Body.Close()
	if created["kind"] != ndnet.KindIsolated || created["dhcp"] != true {
		t.Fatalf("%v", created)
	}
}

func TestNetworkDangerousRequiresConfirmAndBlocksOperator(t *testing.T) {
	s, mem, token := testServer(t)
	cluster, _ := mem.GetCluster(context.Background())
	_ = mem.UpsertNode(context.Background(), appdb.Node{ID: uuid.NewString(), ClusterID: cluster.ID, Name: "local"})
	s.Network = fakeNet{
		preview: ndnet.Preview{
			Kind: ndnet.KindLANBridge, Danger: ndnet.DangerDangerous, RequiresConfirm: true,
			TypedIfName: "eth0", ManagementIfIndex: 2,
		},
	}
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)
	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/networks", strings.NewReader(`{"name":"lan","kind":"lan-bridge","uplink_ifname":"eth0"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != http.StatusConflict {
		b, _ := io.ReadAll(res.Body)
		t.Fatalf("admin without confirm %d %s", res.StatusCode, b)
	}
	var body map[string]any
	_ = json.NewDecoder(res.Body).Decode(&body)
	_ = res.Body.Close()
	if body["code"] != "confirmation_required" || body["typed_ifname"] != "eth0" {
		t.Fatalf("%v", body)
	}

	op := appdb.User{ID: uuid.NewString(), ClusterID: cluster.ID, Username: "op"}
	_ = mem.CreateUser(context.Background(), op)
	_ = mem.BindRole(context.Background(), cluster.ID, op.ID, rbac.Operator)
	plain := "ndl_op_token"
	_ = mem.CreateToken(context.Background(), appdb.APIToken{
		ID: uuid.NewString(), ClusterID: cluster.ID, UserID: op.ID, Name: "o",
		TokenHash: secutil.HashSHA256(plain), Prefix: "ndl_op",
	})
	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/networks", strings.NewReader(`{"name":"lan2","kind":"lan-bridge","uplink_ifname":"eth0"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+plain)
	res, err = ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != http.StatusForbidden {
		b, _ := io.ReadAll(res.Body)
		t.Fatalf("operator enslave %d %s", res.StatusCode, b)
	}
	_ = res.Body.Close()
}

func TestNetworkAdminConfirmAppliesLANBridge(t *testing.T) {
	s, mem, token := testServer(t)
	cluster, _ := mem.GetCluster(context.Background())
	_ = mem.UpsertNode(context.Background(), appdb.Node{ID: uuid.NewString(), ClusterID: cluster.ID, Name: "local"})
	s.Now = func() time.Time { return time.Date(2026, 9, 1, 4, 0, 0, 0, time.UTC) }
	s.Network = fakeNet{
		preview: ndnet.Preview{
			Kind: ndnet.KindLANBridge, Danger: ndnet.DangerDangerous, RequiresConfirm: true,
			TypedIfName: "eth0", ManagementIfIndex: 2,
		},
		apply: ndnet.ApplyResult{Kind: ndnet.KindLANBridge, Status: ndnet.StatusAvailable, BridgeName: "ndlcafe0001", DHCP: false},
	}
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)
	me, _ := mem.GetUserByName(context.Background(), cluster.ID, "admin")
	confirm := ndnet.ConfirmToken(cluster.ID, me.ID, ndnet.KindLANBridge, "eth0", s.Now())
	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/networks", strings.NewReader(`{"name":"lan","kind":"lan-bridge","uplink_ifname":"eth0","confirm_ifname":"eth0"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(confirmHeader, confirm)
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != http.StatusCreated {
		b, _ := io.ReadAll(res.Body)
		t.Fatalf("confirmed create %d %s", res.StatusCode, b)
	}
	var created map[string]any
	_ = json.NewDecoder(res.Body).Decode(&created)
	_ = res.Body.Close()
	if created["dhcp"] != false || created["kind"] != ndnet.KindLANBridge {
		t.Fatalf("%v", created)
	}
}

func TestNetworkViewerCannotMutate(t *testing.T) {
	s, mem, token := testServer(t)
	cluster, _ := mem.GetCluster(context.Background())
	_ = mem.UpsertNode(context.Background(), appdb.Node{ID: uuid.NewString(), ClusterID: cluster.ID, Name: "local"})
	s.Network = fakeNet{preview: ndnet.Preview{Danger: ndnet.DangerSafe}, apply: ndnet.ApplyResult{Status: ndnet.StatusAvailable}}
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	_ = claimAdmin(t, ts, token)
	viewer := appdb.User{ID: uuid.NewString(), ClusterID: cluster.ID, Username: "view"}
	_ = mem.CreateUser(context.Background(), viewer)
	_ = mem.BindRole(context.Background(), cluster.ID, viewer.ID, rbac.Viewer)
	plain := "ndl_viewer_net"
	_ = mem.CreateToken(context.Background(), appdb.APIToken{
		ID: uuid.NewString(), ClusterID: cluster.ID, UserID: viewer.ID, Name: "v",
		TokenHash: secutil.HashSHA256(plain), Prefix: "ndl_view",
	})
	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/networks", bytes.NewReader([]byte(`{"name":"x","kind":"isolated"}`)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+plain)
	res, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("viewer mutate=%d", res.StatusCode)
	}
	_ = res.Body.Close()
	req, _ = http.NewRequest("GET", ts.URL+"/api/v1/networks", nil)
	req.Header.Set("Authorization", "Bearer "+plain)
	res, err = ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != http.StatusOK {
		t.Fatalf("viewer read=%d", res.StatusCode)
	}
	_ = res.Body.Close()
}

func TestNetworkDryRunDoesNotPersist(t *testing.T) {
	s, mem, token := testServer(t)
	cluster, _ := mem.GetCluster(context.Background())
	_ = mem.UpsertNode(context.Background(), appdb.Node{ID: uuid.NewString(), ClusterID: cluster.ID, Name: "local"})
	s.Network = fakeNet{preview: ndnet.Preview{Kind: ndnet.KindIsolated, Danger: ndnet.DangerSafe, DryRun: true, DHCP: true}}
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)
	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/networks", strings.NewReader(`{"name":"iso","kind":"isolated","dry_run":true}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(res.Body)
		t.Fatalf("dry-run %d %s", res.StatusCode, b)
	}
	_ = res.Body.Close()
	items, _ := mem.ListNetworks(context.Background(), cluster.ID)
	if len(items) != 0 {
		t.Fatal("dry-run persisted a network")
	}
}
