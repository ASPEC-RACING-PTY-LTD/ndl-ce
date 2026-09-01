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
	"github.com/no-dal/ndl-ce/internal/migrate"
)

func TestPhase32LiveMigrateMovesOwnership(t *testing.T) {
	s, mem, token := testServer(t)
	s.Migrate = migrate.NewFake()
	clusterRow, _ := mem.GetCluster(t.Context())
	control := seedNode(t, mem, clusterRow.ID, debianInv(), false)
	worker := appdb.Node{ID: uuid.NewString(), ClusterID: clusterRow.ID, Name: "box-b", Role: "worker", Hostname: "box-b"}
	if err := mem.UpsertNode(t.Context(), worker); err != nil {
		t.Fatal(err)
	}
	wlID := uuid.NewString()
	s.Migrate.(*migrate.Fake).SetSourceRunning(wlID, true)
	if err := mem.CreateWorkload(t.Context(), appdb.Workload{
		ID: wlID, ClusterID: clusterRow.ID, NodeID: control.ID,
		OwnerNodeID: control.ID, DesiredNodeID: control.ID,
		Name: "move-me", Kind: "vm", Status: "running",
	}); err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)
	body := `{"dest_node_id":"` + worker.ID + `","mode":"live"}`
	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/workloads/"+wlID+"/migrate", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ := ts.Client().Do(req)
	raw, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("migrate %d %s", res.StatusCode, raw)
	}
	got, _ := mem.GetWorkload(t.Context(), clusterRow.ID, wlID)
	if got.NodeID != worker.ID || got.OwnerNodeID != worker.ID || got.OwnershipEpoch != 1 {
		t.Fatalf("ownership %+v", got)
	}
	if got.Status != "running" {
		t.Fatalf("dest status %s", got.Status)
	}
	listed, _ := mem.ListWorkloads(t.Context(), clusterRow.ID)
	if len(listed) != 1 {
		t.Fatalf("must not create a second workload copy: %d", len(listed))
	}
}

func TestPhase32FailedLiveLeavesSourceRunning(t *testing.T) {
	s, mem, token := testServer(t)
	fake := migrate.NewFake()
	fake.FailLive = true
	s.Migrate = fake
	clusterRow, _ := mem.GetCluster(t.Context())
	control := seedNode(t, mem, clusterRow.ID, debianInv(), false)
	worker := appdb.Node{ID: uuid.NewString(), ClusterID: clusterRow.ID, Name: "box-b", Role: "worker"}
	if err := mem.UpsertNode(t.Context(), worker); err != nil {
		t.Fatal(err)
	}
	wlID := uuid.NewString()
	fake.SetSourceRunning(wlID, true)
	if err := mem.CreateWorkload(t.Context(), appdb.Workload{
		ID: wlID, ClusterID: clusterRow.ID, NodeID: control.ID,
		OwnerNodeID: control.ID, DesiredNodeID: control.ID,
		Name: "stay", Kind: "vm", Status: "running",
	}); err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)
	body := `{"dest_node_id":"` + worker.ID + `","mode":"live"}`
	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/workloads/"+wlID+"/migrate", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ := ts.Client().Do(req)
	raw, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusConflict {
		t.Fatalf("failed live %d %s", res.StatusCode, raw)
	}
	if !strings.Contains(string(raw), "source remains running") {
		t.Fatalf("error %s", raw)
	}
	got, _ := mem.GetWorkload(t.Context(), clusterRow.ID, wlID)
	if got.NodeID != control.ID || got.OwnershipEpoch != 0 {
		t.Fatalf("source ownership must not move %+v", got)
	}
	if got.Status != "running" {
		t.Fatalf("source must stay running: %s %s", got.Status, got.Reason)
	}
	if fake.DestRunning(wlID) || fake.DestIncoming(wlID) {
		t.Fatal("dest must be aborted")
	}
}

func TestPhase32CTLiveRefusedOfflineOK(t *testing.T) {
	s, mem, token := testServer(t)
	s.Migrate = migrate.NewFake()
	clusterRow, _ := mem.GetCluster(t.Context())
	control := seedNode(t, mem, clusterRow.ID, debianInv(), false)
	worker := appdb.Node{ID: uuid.NewString(), ClusterID: clusterRow.ID, Name: "box-b", Role: "worker"}
	if err := mem.UpsertNode(t.Context(), worker); err != nil {
		t.Fatal(err)
	}
	wlID := uuid.NewString()
	s.Migrate.(*migrate.Fake).SetSourceRunning(wlID, true)
	if err := mem.CreateWorkload(t.Context(), appdb.Workload{
		ID: wlID, ClusterID: clusterRow.ID, NodeID: control.ID,
		OwnerNodeID: control.ID, DesiredNodeID: control.ID,
		Name: "ct-1", Kind: "system-container", Status: "running",
	}); err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)
	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/workloads/"+wlID+"/migrate", strings.NewReader(
		`{"dest_node_id":"`+worker.ID+`","mode":"live"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ := ts.Client().Do(req)
	raw, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("ct live %d %s", res.StatusCode, raw)
	}
	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/workloads/"+wlID+"/migrate", strings.NewReader(
		`{"dest_node_id":"`+worker.ID+`","mode":"offline"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(req)
	raw, _ = io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("ct offline %d %s", res.StatusCode, raw)
	}
	got, _ := mem.GetWorkload(t.Context(), clusterRow.ID, wlID)
	if got.NodeID != worker.ID || got.OwnershipEpoch != 1 {
		t.Fatalf("ct ownership %+v", got)
	}
}

func TestPhase32MissingDestAgentLeavesSource(t *testing.T) {
	s, mem, token := testServer(t)
	clusterRow, _ := mem.GetCluster(t.Context())
	control := seedNode(t, mem, clusterRow.ID, debianInv(), false)
	worker := appdb.Node{ID: uuid.NewString(), ClusterID: clusterRow.ID, Name: "box-b", Role: "worker"}
	if err := mem.UpsertNode(t.Context(), worker); err != nil {
		t.Fatal(err)
	}
	wlID := uuid.NewString()
	if err := mem.CreateWorkload(t.Context(), appdb.Workload{
		ID: wlID, ClusterID: clusterRow.ID, NodeID: control.ID,
		OwnerNodeID: control.ID, DesiredNodeID: control.ID,
		Name: "solo", Kind: "vm", Status: "running",
	}); err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)
	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/workloads/"+wlID+"/migrate", strings.NewReader(
		`{"dest_node_id":"`+worker.ID+`","mode":"live"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ := ts.Client().Do(req)
	raw, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusFailedDependency {
		t.Fatalf("missing dest agent %d %s", res.StatusCode, raw)
	}
	got, _ := mem.GetWorkload(t.Context(), clusterRow.ID, wlID)
	if got.NodeID != control.ID || got.Status != "running" || got.OwnershipEpoch != 0 {
		t.Fatalf("source must be untouched %+v", got)
	}
}

func TestPhase32SameNodeAndCPUHostRefused(t *testing.T) {
	s, mem, token := testServer(t)
	s.Migrate = migrate.NewFake()
	clusterRow, _ := mem.GetCluster(t.Context())
	control := seedNode(t, mem, clusterRow.ID, debianInv(), false)
	worker := appdb.Node{ID: uuid.NewString(), ClusterID: clusterRow.ID, Name: "box-b", Role: "worker"}
	if err := mem.UpsertNode(t.Context(), worker); err != nil {
		t.Fatal(err)
	}
	wlID := uuid.NewString()
	applied, _ := json.Marshal(map[string]any{"argv": []string{"/usr/bin/qemu-system-x86_64", "-cpu", "host"}})
	if err := mem.CreateWorkload(t.Context(), appdb.Workload{
		ID: wlID, ClusterID: clusterRow.ID, NodeID: control.ID,
		OwnerNodeID: control.ID, DesiredNodeID: control.ID,
		Name: "host-cpu", Kind: "vm", Status: "running", AppliedJSON: applied,
	}); err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)
	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/workloads/"+wlID+"/migrate", strings.NewReader(
		`{"dest_node_id":"`+control.ID+`","mode":"offline"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ := ts.Client().Do(req)
	if res.StatusCode != http.StatusUnprocessableEntity {
		raw, _ := io.ReadAll(res.Body)
		_ = res.Body.Close()
		t.Fatalf("same node %d %s", res.StatusCode, raw)
	}
	_ = res.Body.Close()
	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/workloads/"+wlID+"/migrate", strings.NewReader(
		`{"dest_node_id":"`+worker.ID+`","mode":"live"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(req)
	raw, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusConflict && res.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("cpu host live %d %s", res.StatusCode, raw)
	}
	if !strings.Contains(string(raw), "cpu host") && !strings.Contains(string(raw), "source remains") {
		t.Fatalf("cpu host %s", raw)
	}
	got, _ := mem.GetWorkload(t.Context(), clusterRow.ID, wlID)
	if got.NodeID != control.ID {
		t.Fatalf("cpu host live must not move %+v", got)
	}
}
