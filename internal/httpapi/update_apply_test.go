package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/no-dal/ndl-ce/internal/appdb"
	"github.com/no-dal/ndl-ce/internal/hostos"
)

// detachedUpdate answers like a host whose apply runs in its own unit.
type detachedUpdate struct {
	applyState string
	calls      []string
}

func (d *detachedUpdate) HostUpdate(_ context.Context, req hostos.UpdateRequest) (hostos.UpdateResult, error) {
	d.calls = append(d.calls, req.Action)
	res := hostos.UpdateResult{Supported: true, Action: req.Action, Channel: hostos.ChannelStable, Status: appdb.UpdateSucceeded, RepositoryConfigured: true}
	switch req.Action {
	case "apply":
		res.Status = appdb.UpdateRunning
	case hostos.UpdateApplyStatus:
		res.Status = d.applyState
		if d.applyState == appdb.UpdateSucceeded {
			res.Version = "1.1.1"
		}
		if d.applyState == appdb.UpdateFailed {
			res.Reason = "control-plane package apply failed"
			res.Log = "E: Unable to locate package ndl-control"
		}
	}
	return res, nil
}

func updatesCall(t *testing.T, ts *httptest.Server, cookie, method, path, confirm string) (int, map[string]any) {
	t.Helper()
	req, _ := http.NewRequest(method, ts.URL+"/api/v1"+path, strings.NewReader("{}"))
	req.Header.Set("Content-Type", "application/json")
	if confirm != "" {
		req.Header.Set("X-Nodal-Confirm", confirm)
	}
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	var body map[string]any
	_ = json.Unmarshal(raw, &body)
	return res.StatusCode, body
}

func TestApplyStaysRunningUntilTheHostUnitFinishes(t *testing.T) {
	s, mem, token := testServer(t)
	host := &detachedUpdate{applyState: appdb.UpdateRunning}
	s.Update = host
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)

	code, op := updatesCall(t, ts, cookie, "POST", "/updates/apply", "apply-update")
	if code != http.StatusOK || op["status"] != "running" || op["finished_at"] != nil {
		t.Fatalf("apply must be recorded as running: %d %v", code, op)
	}

	// While the unit runs, status reads keep it running and other work is refused.
	_, status := updatesCall(t, ts, cookie, "GET", "/updates", "")
	if last := status["last_operation"].(map[string]any); last["status"] != "running" {
		t.Fatalf("%v", status)
	}
	if status["repository_configured"] != true {
		t.Fatalf("repository state must be reported: %v", status)
	}
	if code, _ := updatesCall(t, ts, cookie, "POST", "/updates/check", ""); code != http.StatusConflict {
		t.Fatalf("check during an update must be refused, got %d", code)
	}

	host.applyState = appdb.UpdateSucceeded
	_, status = updatesCall(t, ts, cookie, "GET", "/updates", "")
	last := status["last_operation"].(map[string]any)
	if last["status"] != "succeeded" || last["version"] != "1.1.1" || last["finished_at"] == nil {
		t.Fatalf("finished apply must be settled from the host: %v", last)
	}
	cluster, _ := mem.GetCluster(context.Background())
	stored, _ := mem.GetLatestUpdateOperation(context.Background(), cluster.ID)
	if stored == nil || stored.Status != appdb.UpdateSucceeded {
		t.Fatalf("settled result must be stored: %+v", stored)
	}
	assertUpdateEventCount(t, mem, cluster.ID, "update.apply", 2)
}

func TestFailedApplyCarriesPackageOutput(t *testing.T) {
	s, _, token := testServer(t)
	host := &detachedUpdate{applyState: appdb.UpdateFailed}
	s.Update = host
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)
	updatesCall(t, ts, cookie, "POST", "/updates/apply", "apply-update")
	_, status := updatesCall(t, ts, cookie, "GET", "/updates", "")
	last := status["last_operation"].(map[string]any)
	if last["status"] != "failed" || !strings.Contains(last["error"].(string), "Unable to locate package") {
		t.Fatalf("%v", last)
	}
}

func TestEnableRepositoryRequiresConfirm(t *testing.T) {
	s, _, token := testServer(t)
	s.Update = &detachedUpdate{}
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)
	if code, _ := updatesCall(t, ts, cookie, "POST", "/updates/repository", ""); code != http.StatusUnprocessableEntity {
		t.Fatalf("missing confirm %d", code)
	}
	code, body := updatesCall(t, ts, cookie, "POST", "/updates/repository", "enable-repository")
	if code != http.StatusOK || body["action"] != "repository-enable" || body["status"] != "succeeded" {
		t.Fatalf("%d %v", code, body)
	}
}
