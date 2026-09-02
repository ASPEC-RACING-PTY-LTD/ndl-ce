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
	"github.com/no-dal/ndl-ce/internal/inventory"
	"github.com/no-dal/ndl-ce/internal/migrate"
)

func TestPhase31AutomaticLandsOnGPUNodeWithoutWrongCopy(t *testing.T) {
	s, mem, token := testServer(t)
	clusterRow, _ := mem.GetCluster(t.Context())
	control := seedNode(t, mem, clusterRow.ID, debianInv(), false)
	worker := appdb.Node{ID: uuid.NewString(), ClusterID: clusterRow.ID, Name: "box-b", Role: "worker", Hostname: "box-b"}
	if err := mem.UpsertNode(t.Context(), worker); err != nil {
		t.Fatal(err)
	}
	gpuInv := debianInv()
	gpuInv.GPUs = []inventory.GPU{{ID: "0000:01:00.0", Model: "test-gpu"}}
	body, _ := json.Marshal(gpuInv)
	if err := mem.UpsertInventory(t.Context(), appdb.HardwareInventory{
		NodeID: worker.ID, ClusterID: clusterRow.ID, Payload: body,
	}); err != nil {
		t.Fatal(err)
	}
	_ = mem.CreateWorkload(t.Context(), appdb.Workload{
		ID: uuid.NewString(), ClusterID: clusterRow.ID, NodeID: control.ID,
		OwnerNodeID: control.ID, DesiredNodeID: control.ID,
		Name: "keep-a", Kind: "vm", Status: "running",
	})

	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)

	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/workloads", strings.NewReader(
		`{"name":"gpu-vm","kind":"vm","require_gpu":true,"placement":"automatic"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ := ts.Client().Do(req)
	raw, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("create %d %s", res.StatusCode, raw)
	}
	var created map[string]any
	if err := json.Unmarshal(raw, &created); err != nil {
		t.Fatal(err)
	}
	if created["desired_node_id"] != worker.ID {
		t.Fatalf("wanted worker placement %s", raw)
	}
	if created["status"] == "running" {
		t.Fatal("must not start a second copy on the control node")
	}
	if !strings.Contains(fmtString(created["reason"]), "remote apply") {
		t.Fatalf("reason %s", raw)
	}

	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/workloads/"+created["id"].(string)+"/start", strings.NewReader(`{}`))
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(req)
	raw, _ = io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusConflict {
		t.Fatalf("start on wrong node %d %s", res.StatusCode, raw)
	}

	kept, _ := mem.ListWorkloads(t.Context(), clusterRow.ID)
	for _, w := range kept {
		if w.Name == "keep-a" && (w.Status != "running" || w.NodeID != control.ID) {
			t.Fatalf("existing VM on node A must stay %+v", w)
		}
	}
}

func TestPhase31MaintainQueuesMigrateAndSkipsNode(t *testing.T) {
	s, mem, token := testServer(t)
	clusterRow, _ := mem.GetCluster(t.Context())
	control := seedNode(t, mem, clusterRow.ID, debianInv(), false)
	_ = mem.CreateWorkload(t.Context(), appdb.Workload{
		ID: uuid.NewString(), ClusterID: clusterRow.ID, NodeID: control.ID,
		OwnerNodeID: control.ID, DesiredNodeID: control.ID, Name: "drain-me", Kind: "vm", Status: "running",
	})
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)
	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/nodes/"+control.ID+"/maintain", strings.NewReader(`{"reason":"disk"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ := ts.Client().Do(req)
	raw, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK || !strings.Contains(string(raw), "drain-me") || !strings.Contains(string(raw), "migrate_operation_id") {
		t.Fatalf("maintain %d %s", res.StatusCode, raw)
	}
	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/placement/preview", strings.NewReader(`{"placement":"automatic"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(req)
	raw, _ = io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("preview while control draining %d %s", res.StatusCode, raw)
	}
}

func TestPhase31MaintainMigratesWhenDestIsLocal(t *testing.T) {
	s, mem, token := testServer(t)
	s.Migrate = migrate.NewFake()
	clusterRow, _ := mem.GetCluster(t.Context())
	control := seedNode(t, mem, clusterRow.ID, debianInv(), false)
	worker := appdb.Node{ID: uuid.NewString(), ClusterID: clusterRow.ID, Name: "box-b", Role: "worker", Hostname: "box-b"}
	if err := mem.UpsertNode(t.Context(), worker); err != nil {
		t.Fatal(err)
	}
	if err := mem.UpsertInventory(t.Context(), appdb.HardwareInventory{
		NodeID: worker.ID, ClusterID: clusterRow.ID, Payload: mustInvJSON(debianInv()),
	}); err != nil {
		t.Fatal(err)
	}
	wlID := uuid.NewString()
	s.Migrate.(*migrate.Fake).SetSourceRunning(wlID, true)
	if err := mem.CreateWorkload(t.Context(), appdb.Workload{
		ID: wlID, ClusterID: clusterRow.ID, NodeID: worker.ID,
		OwnerNodeID: worker.ID, DesiredNodeID: worker.ID,
		Name: "drain-local", Kind: "vm", Status: "running",
	}); err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)
	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/nodes/"+worker.ID+"/maintain", strings.NewReader(`{"reason":"disk"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ := ts.Client().Do(req)
	raw, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("maintain %d %s", res.StatusCode, raw)
	}
	got, _ := mem.GetWorkload(t.Context(), clusterRow.ID, wlID)
	if got.NodeID != control.ID {
		t.Fatalf("local dest must run migrate %+v", got)
	}
	if got.Status != "running" {
		t.Fatalf("guest must stay running %+v", got)
	}
}

func TestPhase31MaintainRemoteDestQueuesWithoutLocalStart(t *testing.T) {
	s, mem, token := testServer(t)
	fake := migrate.NewFake()
	s.Migrate = fake
	clusterRow, _ := mem.GetCluster(t.Context())
	control := seedNode(t, mem, clusterRow.ID, debianInv(), false)
	worker := appdb.Node{ID: uuid.NewString(), ClusterID: clusterRow.ID, Name: "box-b", Role: "worker", Hostname: "box-b"}
	if err := mem.UpsertNode(t.Context(), worker); err != nil {
		t.Fatal(err)
	}
	if err := mem.UpsertInventory(t.Context(), appdb.HardwareInventory{
		NodeID: worker.ID, ClusterID: clusterRow.ID, Payload: mustInvJSON(debianInv()),
	}); err != nil {
		t.Fatal(err)
	}
	wlID := uuid.NewString()
	fake.SetSourceRunning(wlID, true)
	if err := mem.CreateWorkload(t.Context(), appdb.Workload{
		ID: wlID, ClusterID: clusterRow.ID, NodeID: control.ID,
		OwnerNodeID: control.ID, DesiredNodeID: control.ID,
		Name: "stay-put", Kind: "vm", Status: "running",
	}); err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)
	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/nodes/"+control.ID+"/maintain", strings.NewReader(`{"reason":"disk"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ := ts.Client().Do(req)
	raw, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK || !strings.Contains(string(raw), "stay-put") || !strings.Contains(string(raw), "migrate_operation_id") {
		t.Fatalf("maintain %d %s", res.StatusCode, raw)
	}
	if !strings.Contains(string(raw), "dest agent is not connected") {
		t.Fatalf("remote dest must stay queued %s", raw)
	}
	got, _ := mem.GetWorkload(t.Context(), clusterRow.ID, wlID)
	if got.NodeID != control.ID || got.Status != "running" || got.OwnershipEpoch != 0 {
		t.Fatalf("must not local-start on worker dest %+v", got)
	}
	if fake.DestRunning(wlID) {
		t.Fatal("must not start dest on the control unix agent")
	}
}

func mustInvJSON(inv inventory.Inventory) []byte {
	body, _ := json.Marshal(inv)
	return body
}

func fmtString(v any) string {
	s, _ := v.(string)
	return s
}

type failAddNodeGroupMemberStore struct {
	appdb.Store
}

func (f failAddNodeGroupMemberStore) AddNodeGroupMember(context.Context, string, string, string) error {
	return errors.New("persist failed")
}

func TestPhase31NodeGroupCreateRecordsMembers(t *testing.T) {
	s, mem, token := testServer(t)
	cluster, _ := mem.GetCluster(t.Context())
	node := seedNode(t, mem, cluster.ID, debianInv(), false)
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)
	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/node-groups", strings.NewReader(`{"name":"gpu","node_ids":["`+node.ID+`"]}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ := ts.Client().Do(req)
	raw, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("create %d %s", res.StatusCode, raw)
	}
	var created map[string]any
	if err := json.Unmarshal(raw, &created); err != nil {
		t.Fatal(err)
	}
	members, err := mem.ListNodeGroupMembers(t.Context(), cluster.ID, created["id"].(string))
	if err != nil || len(members) != 1 || members[0] != node.ID {
		t.Fatalf("members %+v %v", members, err)
	}
}

func TestPhase31NodeGroupCreateFailsClosedWhenMemberPersistFails(t *testing.T) {
	s, mem, token := testServer(t)
	cluster, _ := mem.GetCluster(t.Context())
	node := seedNode(t, mem, cluster.ID, debianInv(), false)
	s.Store = failAddNodeGroupMemberStore{Store: mem}
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)
	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/node-groups", strings.NewReader(`{"name":"gpu","node_ids":["`+node.ID+`"]}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ := ts.Client().Do(req)
	raw, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusInternalServerError {
		t.Fatalf("member persist %d %s", res.StatusCode, raw)
	}
	if !strings.Contains(string(raw), "could not record node group members") {
		t.Fatalf("member persist body %s", raw)
	}
}
