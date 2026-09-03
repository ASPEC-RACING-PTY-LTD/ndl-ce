package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/no-dal/ndl-ce/internal/appdb"
	"github.com/no-dal/ndl-ce/internal/ndnet"
)

func TestPhase27VLANBondPolicyOverlay(t *testing.T) {
	s, mem, token := testServer(t)
	s.Network = fakeNet{apply: ndnet.ApplyResult{Status: ndnet.StatusAvailable, BridgeName: "ndlabcd123"}}
	cluster, _ := mem.GetCluster(t.Context())
	node := seedNode(t, mem, cluster.ID, debianInv(), false)
	netID := uuid.NewString()
	_ = mem.CreateNetwork(t.Context(), appdb.Network{
		ID: netID, ClusterID: cluster.ID, NodeID: node.ID, Name: "iso", Kind: ndnet.KindIsolated,
		Status: ndnet.StatusAvailable, BridgeName: "ndlabcd123",
	})
	a := uuid.NewString()
	b := uuid.NewString()
	_ = mem.CreateWorkload(t.Context(), appdb.Workload{ID: a, ClusterID: cluster.ID, Name: "a", Kind: "vm", Status: "stopped"})
	_ = mem.CreateWorkload(t.Context(), appdb.Workload{ID: b, ClusterID: cluster.ID, Name: "b", Kind: "vm", Status: "stopped"})
	_ = mem.CreateWorkloadNIC(t.Context(), appdb.WorkloadNIC{ID: uuid.NewString(), ClusterID: cluster.ID, WorkloadID: a, NetworkID: netID, MAC: "02:00:00:00:00:01"})
	_ = mem.CreateWorkloadNIC(t.Context(), appdb.WorkloadNIC{ID: uuid.NewString(), ClusterID: cluster.ID, WorkloadID: b, NetworkID: netID, MAC: "02:00:00:00:00:02"})

	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)

	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/networks/vlans", strings.NewReader(`{"name":"access20","network_id":"`+netID+`","vlan_id":20,"access_ifname":"eth1"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ := ts.Client().Do(req)
	body, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusCreated || !strings.Contains(string(body), `"vlan_id":20`) {
		t.Fatalf("vlan %d %s", res.StatusCode, body)
	}

	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/networks/bonds", strings.NewReader(`{"name":"uplink","mode":"active-backup","members":["eth1"]}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(req)
	body, _ = io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusCreated || !strings.Contains(string(body), "active-backup") {
		t.Fatalf("bond %d %s", res.StatusCode, body)
	}

	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/networks/policies", strings.NewReader(`{"name":"deny-pair","action":"deny","src_workload_id":"`+a+`","dst_workload_id":"`+b+`"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(req)
	body, _ = io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("policy %d %s", res.StatusCode, body)
	}
	var pol map[string]any
	if err := json.Unmarshal(body, &pol); err != nil {
		t.Fatal(err)
	}
	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/networks/policies/"+pol["id"].(string)+"/apply", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(req)
	body, _ = io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("apply %d %s", res.StatusCode, body)
	}

	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/networks/overlays", strings.NewReader(`{"name":"prep","vni":100}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(req)
	body, _ = io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("overlay %d %s", res.StatusCode, body)
	}
	if strings.Contains(string(body), `"status":"available"`) {
		t.Fatalf("overlay prep must not store available: %s", body)
	}
	if !strings.Contains(string(body), "local prep, mesh not joined") {
		t.Fatalf("overlay %d %s", res.StatusCode, body)
	}

	req, _ = http.NewRequest("GET", ts.URL+"/api/v1/networks", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(req)
	body, _ = io.ReadAll(res.Body)
	_ = res.Body.Close()
	if !strings.Contains(string(body), `"bonds"`) || !strings.Contains(string(body), "uplink") {
		t.Fatalf("list %s", body)
	}
}

func TestPhase27OverlayCreateFailsClosedForInvalidVNI(t *testing.T) {
	s, mem, token := testServer(t)
	s.Network = fakeNet{apply: ndnet.ApplyResult{Status: ndnet.StatusAvailable, BridgeName: "ndlabcd123"}}
	cluster, _ := mem.GetCluster(t.Context())
	_ = seedNode(t, mem, cluster.ID, debianInv(), false)
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)

	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/networks/overlays", strings.NewReader(`{"name":"bad","vni":0}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ := ts.Client().Do(req)
	body, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("invalid vni %d %s", res.StatusCode, body)
	}

	req, _ = http.NewRequest("GET", ts.URL+"/api/v1/networks", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(req)
	listed, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if strings.Contains(string(listed), `"name":"bad"`) {
		t.Fatalf("GET /networks must not list overlay with vni 0: %s", listed)
	}
}

func TestPhase27VLANCreateFailsClosedForInvalidModeAndParent(t *testing.T) {
	s, mem, token := testServer(t)
	s.Network = fakeNet{apply: ndnet.ApplyResult{Status: ndnet.StatusAvailable, BridgeName: "ndlabcd123"}}
	cluster, _ := mem.GetCluster(t.Context())
	_ = seedNode(t, mem, cluster.ID, debianInv(), false)
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)

	cases := []struct {
		body string
		want string
	}{
		{`{"name":"bad-mode","vlan_id":20,"parent_ifname":"eth1","mode":"foo"}`, "vlan mode must be access or trunk"},
		{`{"name":"no-parent","vlan_id":20}`, "vlan parent interface is required"},
		{`{"name":"bad-parent","vlan_id":20,"parent_ifname":"eth1;rm"}`, "vlan parent interface is required"},
		{`{"name":"bad-access","vlan_id":20,"parent_ifname":"eth1","access_ifname":"eth1;rm"}`, "access interface name is not valid"},
	}
	for _, tc := range cases {
		req, _ := http.NewRequest("POST", ts.URL+"/api/v1/networks/vlans", strings.NewReader(tc.body))
		req.Header.Set("Content-Type", "application/json")
		req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
		res, _ := ts.Client().Do(req)
		body, _ := io.ReadAll(res.Body)
		_ = res.Body.Close()
		if res.StatusCode != http.StatusBadRequest || !strings.Contains(string(body), tc.want) {
			t.Fatalf("%s: %d %s", tc.want, res.StatusCode, body)
		}
	}

	req, _ := http.NewRequest("GET", ts.URL+"/api/v1/networks", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ := ts.Client().Do(req)
	listed, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	for _, name := range []string{"bad-mode", "no-parent", "bad-parent", "bad-access"} {
		if strings.Contains(string(listed), `"name":"`+name+`"`) {
			t.Fatalf("GET /networks must not list invalid vlan %s: %s", name, listed)
		}
	}
}

func TestPhase27VLANCreateFailsClosedForUnavailableNetwork(t *testing.T) {
	s, mem, token := testServer(t)
	s.Network = fakeNet{apply: ndnet.ApplyResult{Status: ndnet.StatusAvailable, BridgeName: "ndlabcd123"}}
	cluster, _ := mem.GetCluster(t.Context())
	node := seedNode(t, mem, cluster.ID, debianInv(), false)
	netID := uuid.NewString()
	if err := mem.CreateNetwork(t.Context(), appdb.Network{
		ID: netID, ClusterID: cluster.ID, NodeID: node.ID, Name: "vlan-offline",
		Kind: ndnet.KindIsolated, Status: ndnet.StatusUnavailable, BridgeName: "ndlcafe00cc",
	}); err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)
	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/networks/vlans", strings.NewReader(`{"name":"access20","network_id":"`+netID+`","vlan_id":20,"access_ifname":"eth1"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ := ts.Client().Do(req)
	raw, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusConflict {
		t.Fatalf("unavailable parent network %d %s", res.StatusCode, raw)
	}
	if !strings.Contains(string(raw), "an available network is required") {
		t.Fatalf("unavailable parent network body %s", raw)
	}
	vlans, _ := mem.ListNetworkVLANs(t.Context(), cluster.ID)
	if len(vlans) != 0 {
		t.Fatalf("GET must not list a VLAN whose parent network apply cannot attach: %+v", vlans)
	}
}

func TestPhase27BondCreateFailsClosedForInvalidModeAndMembers(t *testing.T) {
	s, mem, token := testServer(t)
	s.Network = fakeNet{apply: ndnet.ApplyResult{Status: ndnet.StatusAvailable, BridgeName: "ndlabcd123"}}
	cluster, _ := mem.GetCluster(t.Context())
	_ = seedNode(t, mem, cluster.ID, debianInv(), false)
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)

	cases := []struct {
		body string
		want string
	}{
		{`{"name":"bad-mode","mode":"foo","members":["eth1"]}`, "bond mode must be active-backup or 802.3ad"},
		{`{"name":"no-members","mode":"active-backup","members":[]}`, "bond requires at least one member interface"},
		{`{"name":"bad-if","mode":"active-backup","members":["eth1;rm"]}`, "bond member interface name is not valid"},
	}
	for _, tc := range cases {
		req, _ := http.NewRequest("POST", ts.URL+"/api/v1/networks/bonds", strings.NewReader(tc.body))
		req.Header.Set("Content-Type", "application/json")
		req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
		res, _ := ts.Client().Do(req)
		body, _ := io.ReadAll(res.Body)
		_ = res.Body.Close()
		if res.StatusCode != http.StatusBadRequest || !strings.Contains(string(body), tc.want) {
			t.Fatalf("%s: %d %s", tc.want, res.StatusCode, body)
		}
	}

	req, _ := http.NewRequest("GET", ts.URL+"/api/v1/networks", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ := ts.Client().Do(req)
	listed, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	for _, name := range []string{"bad-mode", "no-members", "bad-if"} {
		if strings.Contains(string(listed), `"name":"`+name+`"`) {
			t.Fatalf("GET /networks must not list invalid bond %s: %s", name, listed)
		}
	}
}

func TestPhase27ApplyOnePolicyKeepsOthers(t *testing.T) {
	s, mem, token := testServer(t)
	s.Network = fakeNet{apply: ndnet.ApplyResult{Status: ndnet.StatusAvailable, BridgeName: "ndlabcd123"}}
	cluster, _ := mem.GetCluster(t.Context())
	node := seedNode(t, mem, cluster.ID, debianInv(), false)
	netID := uuid.NewString()
	_ = mem.CreateNetwork(t.Context(), appdb.Network{
		ID: netID, ClusterID: cluster.ID, NodeID: node.ID, Name: "iso", Kind: ndnet.KindIsolated,
		Status: ndnet.StatusAvailable, BridgeName: "ndlabcd123",
	})
	a := uuid.NewString()
	b := uuid.NewString()
	c := uuid.NewString()
	_ = mem.CreateWorkload(t.Context(), appdb.Workload{ID: a, ClusterID: cluster.ID, Name: "a", Kind: "vm", Status: "stopped"})
	_ = mem.CreateWorkload(t.Context(), appdb.Workload{ID: b, ClusterID: cluster.ID, Name: "b", Kind: "vm", Status: "stopped"})
	_ = mem.CreateWorkload(t.Context(), appdb.Workload{ID: c, ClusterID: cluster.ID, Name: "c", Kind: "vm", Status: "stopped"})
	_ = mem.CreateWorkloadNIC(t.Context(), appdb.WorkloadNIC{ID: uuid.NewString(), ClusterID: cluster.ID, WorkloadID: a, NetworkID: netID, MAC: "02:00:00:00:00:01"})
	_ = mem.CreateWorkloadNIC(t.Context(), appdb.WorkloadNIC{ID: uuid.NewString(), ClusterID: cluster.ID, WorkloadID: b, NetworkID: netID, MAC: "02:00:00:00:00:02"})
	_ = mem.CreateWorkloadNIC(t.Context(), appdb.WorkloadNIC{ID: uuid.NewString(), ClusterID: cluster.ID, WorkloadID: c, NetworkID: netID, MAC: "02:00:00:00:00:03"})
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)

	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/networks/policies", strings.NewReader(`{"name":"ab","action":"deny","src_workload_id":"`+a+`","dst_workload_id":"`+b+`"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ := ts.Client().Do(req)
	body, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("policy a %d %s", res.StatusCode, body)
	}
	var first map[string]any
	if err := json.Unmarshal(body, &first); err != nil {
		t.Fatal(err)
	}

	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/networks/policies", strings.NewReader(`{"name":"ac","action":"deny","src_workload_id":"`+a+`","dst_workload_id":"`+c+`"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(req)
	if res.StatusCode != http.StatusCreated {
		b, _ := io.ReadAll(res.Body)
		t.Fatalf("policy b %d %s", res.StatusCode, b)
	}
	_ = res.Body.Close()

	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/networks/policies/"+first["id"].(string)+"/apply", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(req)
	if res.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(res.Body)
		t.Fatalf("apply %d %s", res.StatusCode, b)
	}
	_ = res.Body.Close()

	items, _ := mem.ListNetworkPolicies(t.Context(), cluster.ID)
	if len(items) != 2 {
		t.Fatalf("applying one policy deleted others: %d", len(items))
	}
	var applied appdb.NetworkPolicy
	for _, item := range items {
		if item.ID == first["id"].(string) {
			applied = item
		}
	}
	if applied.Status != ndnet.StatusAvailable {
		t.Fatalf("applied policy status %+v", applied)
	}
	for _, item := range items {
		if item.Status != ndnet.StatusAvailable {
			t.Fatalf("full-set apply left sibling %+v", item)
		}
	}
}

type recordAdvancedNet struct {
	fakeNet
	last  ndnet.AdvancedOp
	calls int
}

func (r *recordAdvancedNet) NetAdvanced(ctx context.Context, op ndnet.AdvancedOp) (ndnet.AdvancedResult, error) {
	r.last = op
	r.calls++
	return r.fakeNet.NetAdvanced(ctx, op)
}

func TestPhase27ApplySendsFullPolicySetOnce(t *testing.T) {
	s, mem, token := testServer(t)
	rec := &recordAdvancedNet{fakeNet: fakeNet{apply: ndnet.ApplyResult{Status: ndnet.StatusAvailable, BridgeName: "ndlabcd123"}}}
	s.Network = rec
	cluster, _ := mem.GetCluster(t.Context())
	node := seedNode(t, mem, cluster.ID, debianInv(), false)
	netID := uuid.NewString()
	_ = mem.CreateNetwork(t.Context(), appdb.Network{
		ID: netID, ClusterID: cluster.ID, NodeID: node.ID, Name: "iso", Kind: ndnet.KindIsolated,
		Status: ndnet.StatusAvailable, BridgeName: "ndlabcd123",
	})
	a := uuid.NewString()
	b := uuid.NewString()
	c := uuid.NewString()
	_ = mem.CreateWorkload(t.Context(), appdb.Workload{ID: a, ClusterID: cluster.ID, Name: "a", Kind: "vm", Status: "stopped"})
	_ = mem.CreateWorkload(t.Context(), appdb.Workload{ID: b, ClusterID: cluster.ID, Name: "b", Kind: "vm", Status: "stopped"})
	_ = mem.CreateWorkload(t.Context(), appdb.Workload{ID: c, ClusterID: cluster.ID, Name: "c", Kind: "vm", Status: "stopped"})
	_ = mem.CreateWorkloadNIC(t.Context(), appdb.WorkloadNIC{ID: uuid.NewString(), ClusterID: cluster.ID, WorkloadID: a, NetworkID: netID, MAC: "02:00:00:00:00:01"})
	_ = mem.CreateWorkloadNIC(t.Context(), appdb.WorkloadNIC{ID: uuid.NewString(), ClusterID: cluster.ID, WorkloadID: b, NetworkID: netID, MAC: "02:00:00:00:00:02"})
	_ = mem.CreateWorkloadNIC(t.Context(), appdb.WorkloadNIC{ID: uuid.NewString(), ClusterID: cluster.ID, WorkloadID: c, NetworkID: netID, MAC: "02:00:00:00:00:03"})
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)

	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/networks/policies", strings.NewReader(`{"name":"ab","action":"deny","src_workload_id":"`+a+`","dst_workload_id":"`+b+`"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ := ts.Client().Do(req)
	body, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("policy a %d %s", res.StatusCode, body)
	}
	var first map[string]any
	if err := json.Unmarshal(body, &first); err != nil {
		t.Fatal(err)
	}

	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/networks/policies", strings.NewReader(`{"name":"ac","action":"deny","src_workload_id":"`+a+`","dst_workload_id":"`+c+`"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(req)
	if res.StatusCode != http.StatusCreated {
		b, _ := io.ReadAll(res.Body)
		t.Fatalf("policy b %d %s", res.StatusCode, b)
	}
	_ = res.Body.Close()

	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/networks/policies/"+first["id"].(string)+"/apply", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(req)
	raw, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("apply %d %s", res.StatusCode, raw)
	}
	if rec.calls != 1 {
		t.Fatalf("apply must load nft once, got %d", rec.calls)
	}
	if len(rec.last.Policies) != 2 {
		t.Fatalf("apply must send the full stored set, got %+v", rec.last.Policies)
	}
}

type failUpdateNetworkPolicyStatusStore struct {
	appdb.Store
}

func (f failUpdateNetworkPolicyStatusStore) UpdateNetworkPolicyStatus(context.Context, string, string, string, string) error {
	return errors.New("persist failed")
}

func TestPhase27ApplyFailsClosedWhenStatusPersistFails(t *testing.T) {
	s, mem, token := testServer(t)
	s.Network = fakeNet{apply: ndnet.ApplyResult{Status: ndnet.StatusAvailable, BridgeName: "ndlabcd123"}}
	cluster, _ := mem.GetCluster(t.Context())
	node := seedNode(t, mem, cluster.ID, debianInv(), false)
	netID := uuid.NewString()
	_ = mem.CreateNetwork(t.Context(), appdb.Network{
		ID: netID, ClusterID: cluster.ID, NodeID: node.ID, Name: "iso", Kind: ndnet.KindIsolated,
		Status: ndnet.StatusAvailable, BridgeName: "ndlabcd123",
	})
	a := uuid.NewString()
	b := uuid.NewString()
	_ = mem.CreateWorkload(t.Context(), appdb.Workload{ID: a, ClusterID: cluster.ID, Name: "a", Kind: "vm", Status: "stopped"})
	_ = mem.CreateWorkload(t.Context(), appdb.Workload{ID: b, ClusterID: cluster.ID, Name: "b", Kind: "vm", Status: "stopped"})
	_ = mem.CreateWorkloadNIC(t.Context(), appdb.WorkloadNIC{ID: uuid.NewString(), ClusterID: cluster.ID, WorkloadID: a, NetworkID: netID, MAC: "02:00:00:00:00:01"})
	_ = mem.CreateWorkloadNIC(t.Context(), appdb.WorkloadNIC{ID: uuid.NewString(), ClusterID: cluster.ID, WorkloadID: b, NetworkID: netID, MAC: "02:00:00:00:00:02"})
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)

	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/networks/policies", strings.NewReader(`{"name":"ab","action":"deny","src_workload_id":"`+a+`","dst_workload_id":"`+b+`"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ := ts.Client().Do(req)
	body, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("policy %d %s", res.StatusCode, body)
	}
	var created map[string]any
	if err := json.Unmarshal(body, &created); err != nil {
		t.Fatal(err)
	}
	s.Store = failUpdateNetworkPolicyStatusStore{Store: mem}

	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/networks/policies/"+created["id"].(string)+"/apply", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(req)
	raw, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusInternalServerError {
		t.Fatalf("policy persist %d %s", res.StatusCode, raw)
	}
	if !strings.Contains(string(raw), "could not record network policy") {
		t.Fatalf("policy persist body %s", raw)
	}
}

func TestPhase27PolicyCreateFailsClosedForMissingAndEmptyWorkload(t *testing.T) {
	s, mem, token := testServer(t)
	cluster, _ := mem.GetCluster(t.Context())
	node := seedNode(t, mem, cluster.ID, debianInv(), false)
	netID := uuid.NewString()
	_ = mem.CreateNetwork(t.Context(), appdb.Network{
		ID: netID, ClusterID: cluster.ID, NodeID: node.ID, Name: "iso", Kind: ndnet.KindIsolated,
		Status: ndnet.StatusAvailable, BridgeName: "ndlabcd123",
	})
	a := uuid.NewString()
	b := uuid.NewString()
	_ = mem.CreateWorkload(t.Context(), appdb.Workload{ID: a, ClusterID: cluster.ID, Name: "a", Kind: "vm", Status: "stopped"})
	_ = mem.CreateWorkload(t.Context(), appdb.Workload{ID: b, ClusterID: cluster.ID, Name: "b", Kind: "vm", Status: "stopped"})
	_ = mem.CreateWorkloadNIC(t.Context(), appdb.WorkloadNIC{ID: uuid.NewString(), ClusterID: cluster.ID, WorkloadID: a, NetworkID: netID, MAC: "02:00:00:00:00:01"})
	_ = mem.CreateWorkloadNIC(t.Context(), appdb.WorkloadNIC{ID: uuid.NewString(), ClusterID: cluster.ID, WorkloadID: b, NetworkID: netID, MAC: "02:00:00:00:00:02"})
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)

	empty, _ := http.NewRequest("POST", ts.URL+"/api/v1/networks/policies", strings.NewReader(`{"name":"stolen","action":"deny","src_workload_id":"","dst_workload_id":"`+b+`"}`))
	empty.Header.Set("Content-Type", "application/json")
	empty.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	emptyRes, _ := ts.Client().Do(empty)
	emptyRaw, _ := io.ReadAll(emptyRes.Body)
	_ = emptyRes.Body.Close()
	if emptyRes.StatusCode != http.StatusBadRequest {
		t.Fatalf("empty src %d %s", emptyRes.StatusCode, emptyRaw)
	}
	if !strings.Contains(string(emptyRaw), "src_workload_id and dst_workload_id are required") {
		t.Fatalf("empty src body %s", emptyRaw)
	}

	missing := uuid.NewString()
	miss, _ := http.NewRequest("POST", ts.URL+"/api/v1/networks/policies", strings.NewReader(`{"name":"ghost","action":"deny","src_workload_id":"`+missing+`","dst_workload_id":"`+b+`"}`))
	miss.Header.Set("Content-Type", "application/json")
	miss.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	missRes, _ := ts.Client().Do(miss)
	missRaw, _ := io.ReadAll(missRes.Body)
	_ = missRes.Body.Close()
	if missRes.StatusCode != http.StatusNotFound {
		t.Fatalf("missing src %d %s", missRes.StatusCode, missRaw)
	}
	if !strings.Contains(string(missRaw), "workload not found") {
		t.Fatalf("missing src body %s", missRaw)
	}

	get, _ := http.NewRequest("GET", ts.URL+"/api/v1/networks", nil)
	get.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	got, _ := ts.Client().Do(get)
	listRaw, _ := io.ReadAll(got.Body)
	_ = got.Body.Close()
	var listed map[string]any
	if err := json.Unmarshal(listRaw, &listed); err != nil {
		t.Fatal(err)
	}
	pols, _ := listed["policies"].([]any)
	if len(pols) != 0 {
		t.Fatalf("GET must not invent a policy for a missing or empty workload: %s", listRaw)
	}
}

func TestPhase27PolicyCreateFailsClosedForInvalidActionAndSameWorkload(t *testing.T) {
	s, mem, token := testServer(t)
	cluster, _ := mem.GetCluster(t.Context())
	node := seedNode(t, mem, cluster.ID, debianInv(), false)
	netID := uuid.NewString()
	_ = mem.CreateNetwork(t.Context(), appdb.Network{
		ID: netID, ClusterID: cluster.ID, NodeID: node.ID, Name: "iso", Kind: ndnet.KindIsolated,
		Status: ndnet.StatusAvailable, BridgeName: "ndlabcd123",
	})
	a := uuid.NewString()
	b := uuid.NewString()
	_ = mem.CreateWorkload(t.Context(), appdb.Workload{ID: a, ClusterID: cluster.ID, Name: "a", Kind: "vm", Status: "stopped"})
	_ = mem.CreateWorkload(t.Context(), appdb.Workload{ID: b, ClusterID: cluster.ID, Name: "b", Kind: "vm", Status: "stopped"})
	_ = mem.CreateWorkloadNIC(t.Context(), appdb.WorkloadNIC{ID: uuid.NewString(), ClusterID: cluster.ID, WorkloadID: a, NetworkID: netID, MAC: "02:00:00:00:00:01"})
	_ = mem.CreateWorkloadNIC(t.Context(), appdb.WorkloadNIC{ID: uuid.NewString(), ClusterID: cluster.ID, WorkloadID: b, NetworkID: netID, MAC: "02:00:00:00:00:02"})
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)

	bad, _ := http.NewRequest("POST", ts.URL+"/api/v1/networks/policies", strings.NewReader(`{"name":"bad-action","action":"foo","src_workload_id":"`+a+`","dst_workload_id":"`+b+`"}`))
	bad.Header.Set("Content-Type", "application/json")
	bad.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	badRes, _ := ts.Client().Do(bad)
	badRaw, _ := io.ReadAll(badRes.Body)
	_ = badRes.Body.Close()
	if badRes.StatusCode != http.StatusBadRequest || !strings.Contains(string(badRaw), "policy action must be deny or allow") {
		t.Fatalf("action %d %s", badRes.StatusCode, badRaw)
	}

	same, _ := http.NewRequest("POST", ts.URL+"/api/v1/networks/policies", strings.NewReader(`{"name":"loop","action":"deny","src_workload_id":"`+a+`","dst_workload_id":"`+a+`"}`))
	same.Header.Set("Content-Type", "application/json")
	same.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	sameRes, _ := ts.Client().Do(same)
	sameRaw, _ := io.ReadAll(sameRes.Body)
	_ = sameRes.Body.Close()
	if sameRes.StatusCode != http.StatusBadRequest || !strings.Contains(string(sameRaw), "policy source and destination must differ") {
		t.Fatalf("same workload %d %s", sameRes.StatusCode, sameRaw)
	}

	get, _ := http.NewRequest("GET", ts.URL+"/api/v1/networks", nil)
	get.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	got, _ := ts.Client().Do(get)
	listRaw, _ := io.ReadAll(got.Body)
	_ = got.Body.Close()
	if strings.Contains(string(listRaw), `"name":"bad-action"`) || strings.Contains(string(listRaw), `"name":"loop"`) {
		t.Fatalf("GET /networks must not list an unapplyable policy: %s", listRaw)
	}
}
