package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/no-dal/ndl-ce/internal/appdb"
)

func TestDeleteNodeKeepsTheHostAndRemovesStaleNodes(t *testing.T) {
	s, mem, token := testServer(t)
	cluster, _ := mem.GetCluster(context.Background())
	host := seedNode(t, mem, cluster.ID, debianInv(), false)
	stale := appdb.Node{ID: uuid.NewString(), ClusterID: cluster.ID, Name: "cert-lab-nodeb", Role: "worker", HostPlatform: json.RawMessage(`{}`)}
	busy := appdb.Node{ID: uuid.NewString(), ClusterID: cluster.ID, Name: "busy", Role: "worker", HostPlatform: json.RawMessage(`{}`)}
	for _, n := range []appdb.Node{stale, busy} {
		if err := mem.UpsertNode(context.Background(), n); err != nil {
			t.Fatal(err)
		}
	}
	pool := appdb.StoragePool{ID: uuid.NewString(), ClusterID: cluster.ID, NodeID: stale.ID, Name: "lab"}
	if err := mem.CreateStoragePool(context.Background(), pool); err != nil {
		t.Fatal(err)
	}
	if err := mem.CreateWorkload(context.Background(), appdb.Workload{
		ID: uuid.NewString(), ClusterID: cluster.ID, NodeID: busy.ID, OwnerNodeID: busy.ID, DesiredNodeID: busy.ID, Name: "app", Kind: "vm",
	}); err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)

	_, listed := updatesCall(t, ts, cookie, "GET", "/nodes", "")
	locals := 0
	for _, it := range listed["items"].([]any) {
		row := it.(map[string]any)
		if row["local"] == true {
			locals++
			if row["id"] != host.ID {
				t.Fatalf("only the host is local: %v", row)
			}
		}
	}
	if locals != 1 {
		t.Fatalf("exactly one local host: %v", listed)
	}
	if code, _ := updatesCall(t, ts, cookie, "DELETE", "/nodes/"+host.ID, ""); code != http.StatusConflict {
		t.Fatalf("the host must never be deleted, got %d", code)
	}
	if code, _ := updatesCall(t, ts, cookie, "DELETE", "/nodes/"+busy.ID, ""); code != http.StatusConflict {
		t.Fatalf("a node with workloads must be refused, got %d", code)
	}
	if code, body := updatesCall(t, ts, cookie, "DELETE", "/nodes/"+stale.ID, ""); code != http.StatusOK {
		t.Fatalf("a stale node must be deletable: %d %v", code, body)
	}
	if n, _ := mem.GetNodeByID(context.Background(), cluster.ID, stale.ID); n != nil {
		t.Fatal("the node must be gone")
	}
	if p, _ := mem.GetStoragePool(context.Background(), cluster.ID, pool.ID); p != nil {
		t.Fatal("pools recorded for the node must go with it")
	}
}
