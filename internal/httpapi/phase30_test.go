package httpapi

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/no-dal/ndl-ce/internal/appdb"
)

func TestPhase30JoinCreatesSecondNodeAndTokenReuseFails(t *testing.T) {
	s, mem, token := testServer(t)
	clusterRow, _ := mem.GetCluster(t.Context())
	control := seedNode(t, mem, clusterRow.ID, debianInv(), false)
	_ = mem.CreateWorkload(t.Context(), appdb.Workload{
		ID: uuid.NewString(), ClusterID: clusterRow.ID, NodeID: control.ID,
		OwnerNodeID: control.ID, DesiredNodeID: control.ID,
		Name: "keep-running", Kind: "vm", Status: "running",
	})

	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)

	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/cluster/join-tokens", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ := ts.Client().Do(req)
	body, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("token create %d %s", res.StatusCode, body)
	}
	var created map[string]any
	if err := json.Unmarshal(body, &created); err != nil {
		t.Fatal(err)
	}
	joinToken, _ := created["token"].(string)
	if joinToken == "" {
		t.Fatal("token shown once")
	}
	if strings.Contains(string(body), "PRIVATE KEY") && strings.Contains(string(body), "cluster CA") {
		t.Fatal("CA private key must not be in join-token JSON")
	}

	joinBody := `{"token":"` + joinToken + `","hostname":"box-b"}`
	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/cluster/join", strings.NewReader(joinBody))
	req.Header.Set("Content-Type", "application/json")
	res, _ = ts.Client().Do(req)
	body, _ = io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("join %d %s", res.StatusCode, body)
	}
	if strings.Contains(string(body), "BEGIN EC PRIVATE KEY") && !strings.Contains(string(body), "node_key") {
		t.Fatal("unexpected key")
	}
	if !strings.Contains(string(body), `"role":"worker"`) || !strings.Contains(string(body), "node_key") {
		t.Fatalf("worker certs missing %s", body)
	}
	var joined map[string]any
	if err := json.Unmarshal(body, &joined); err != nil {
		t.Fatal(err)
	}
	if joined["id"] == control.ID {
		t.Fatal("hostname must not become node identity")
	}
	if joined["hostname"] != "box-b" {
		t.Fatalf("hostname locator %v", joined["hostname"])
	}

	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/cluster/join", strings.NewReader(joinBody))
	req.Header.Set("Content-Type", "application/json")
	res, _ = ts.Client().Do(req)
	body, _ = io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusConflict && res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("reuse %d %s", res.StatusCode, body)
	}

	req, _ = http.NewRequest("GET", ts.URL+"/api/v1/cluster", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(req)
	body, _ = io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("cluster %d %s", res.StatusCode, body)
	}
	var inv map[string]any
	if err := json.Unmarshal(body, &inv); err != nil {
		t.Fatal(err)
	}
	nodes, _ := inv["nodes"].([]any)
	if len(nodes) != 2 {
		t.Fatalf("inventory %s", body)
	}

	req, _ = http.NewRequest("GET", ts.URL+"/api/v1/nodes", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(req)
	body, _ = io.ReadAll(res.Body)
	_ = res.Body.Close()
	if !strings.Contains(string(body), control.ID) || !strings.Contains(string(body), joined["id"].(string)) {
		t.Fatalf("nodes list %s", body)
	}

	workloads, _ := mem.ListWorkloads(t.Context(), clusterRow.ID)
	if len(workloads) != 1 || workloads[0].Status != "running" || workloads[0].NodeID != control.ID {
		t.Fatalf("existing VM on node A must be untouched: %+v", workloads)
	}
}

func TestPhase30PairingTokenIsNotJoinToken(t *testing.T) {
	s, mem, token := testServer(t)
	clusterRow, _ := mem.GetCluster(t.Context())
	_ = seedNode(t, mem, clusterRow.ID, debianInv(), false)
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	_ = claimAdmin(t, ts, token)

	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/cluster/join", strings.NewReader(`{"token":"pairing-not-join","hostname":"box-b"}`))
	req.Header.Set("Content-Type", "application/json")
	res, _ := ts.Client().Do(req)
	body, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("pairing as join %d %s", res.StatusCode, body)
	}
}

func TestPhase30HostnameCollisionStillUniqueUUID(t *testing.T) {
	s, mem, token := testServer(t)
	clusterRow, _ := mem.GetCluster(t.Context())
	_ = seedNode(t, mem, clusterRow.ID, debianInv(), false)
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)

	id1 := mintJoin(t, ts, cookie, "box-b")
	id2 := mintJoin(t, ts, cookie, "box-b")
	if id1 == id2 {
		t.Fatal("hostname is not identity")
	}
	nodes, _ := mem.ListClusterNodes(t.Context(), clusterRow.ID)
	names := map[string]int{}
	for _, n := range nodes {
		names[n.Name]++
	}
	for name, count := range names {
		if count > 1 {
			t.Fatalf("duplicate name %s", name)
		}
	}
}

func TestPhase30RevokeWorkerAndRefuseControl(t *testing.T) {
	s, mem, token := testServer(t)
	clusterRow, _ := mem.GetCluster(t.Context())
	control := seedNode(t, mem, clusterRow.ID, debianInv(), false)
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)
	workerID := mintJoin(t, ts, cookie, "box-b")

	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/cluster/nodes/"+control.ID+"/revoke", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ := ts.Client().Do(req)
	if res.StatusCode != http.StatusConflict {
		t.Fatalf("revoke control %d", res.StatusCode)
	}
	_ = res.Body.Close()

	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/cluster/nodes/"+workerID+"/revoke", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(req)
	body, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("revoke worker %d %s", res.StatusCode, body)
	}
	got, _ := mem.GetNodeByID(t.Context(), clusterRow.ID, workerID)
	if got == nil || got.RevokedAt == nil {
		t.Fatal("worker must be revoked")
	}
}

func TestPhase30SecondWriterLeaseRefusesWrites(t *testing.T) {
	s, mem, token := testServer(t)
	clusterRow, _ := mem.GetCluster(t.Context())
	_ = seedNode(t, mem, clusterRow.ID, debianInv(), false)
	exp := time.Now().UTC().Add(time.Minute)
	if err := mem.AcquireLease(t.Context(), clusterRow.ID, "writer-a", exp); err != nil {
		t.Fatal(err)
	}
	s.LeaseHolder = "writer-b"
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)
	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/cluster/join-tokens", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ := ts.Client().Do(req)
	body, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusConflict {
		t.Fatalf("second writer %d %s", res.StatusCode, body)
	}
}

func mintJoin(t *testing.T, ts *httptest.Server, cookie, hostname string) string {
	t.Helper()
	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/cluster/join-tokens", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("token %d %s", res.StatusCode, body)
	}
	var created map[string]any
	if err := json.Unmarshal(body, &created); err != nil {
		t.Fatal(err)
	}
	joinBody := `{"token":"` + created["token"].(string) + `","hostname":"` + hostname + `"}`
	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/cluster/join", strings.NewReader(joinBody))
	req.Header.Set("Content-Type", "application/json")
	res, _ = ts.Client().Do(req)
	body, _ = io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("join %d %s", res.StatusCode, body)
	}
	var joined map[string]any
	if err := json.Unmarshal(body, &joined); err != nil {
		t.Fatal(err)
	}
	id, _ := joined["id"].(string)
	if id == "" {
		t.Fatal("missing node id")
	}
	return id
}
