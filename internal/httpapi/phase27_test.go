package httpapi

import (
	"encoding/json"
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
	if res.StatusCode != http.StatusCreated || !strings.Contains(string(body), "Phase 30") {
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
