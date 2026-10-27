package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/no-dal/ndl-ce/internal/appdb"
	"github.com/no-dal/ndl-ce/internal/hostos"
)

type fakeUpdater struct{ actions []string }

func (f *fakeUpdater) HostUpdate(_ context.Context, req hostos.UpdateRequest) (hostos.UpdateResult, error) {
	f.actions = append(f.actions, req.Action)
	return hostos.UpdateResult{Supported: true, Action: req.Action, Status: "running"}, nil
}

func TestWorkerUpdateGoesThroughItsAgent(t *testing.T) {
	ctx := context.Background()
	s, mem, token := testServer(t)
	cluster, _ := mem.GetCluster(ctx)
	home, vps := uuid.NewString(), uuid.NewString()
	_ = mem.UpsertNode(ctx, appdb.Node{ID: home, ClusterID: cluster.ID, Name: "local", Role: "control"})
	_ = mem.UpsertNode(ctx, appdb.Node{ID: vps, ClusterID: cluster.ID, Name: "r-server71377-llv", Role: "worker"})
	up := &fakeUpdater{}
	s.WorkerUpdaters = map[string]HostUpdater{vps: up}
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)

	if code, _ := jsonCall(t, ts, cookie, "POST", "/api/v1/nodes/"+vps+"/update", `{}`); code != http.StatusUnprocessableEntity {
		t.Fatalf("update without confirm = %d", code)
	}
	if code, _ := jsonCall(t, ts, cookie, "POST", "/api/v1/nodes/"+home+"/update", `{}`); code != http.StatusUnprocessableEntity {
		t.Fatalf("control node update = %d", code)
	}
	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/nodes/"+vps+"/update", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Nodal-Confirm", "update-node")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK || strings.Join(up.actions, ",") != "check,apply" {
		t.Fatalf("update = %d, agent saw %v", res.StatusCode, up.actions)
	}
	if code, out := jsonCall(t, ts, cookie, "GET", "/api/v1/nodes/"+vps+"/update", ""); code != http.StatusOK || out["action"] != "apply-status" {
		t.Fatalf("status = %d %v", code, out)
	}
}
