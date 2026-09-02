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
	"github.com/no-dal/ndl-ce/internal/auth"
	"github.com/no-dal/ndl-ce/internal/hostos"
	"github.com/no-dal/ndl-ce/internal/rbac"
)

type fakeUpdate struct {
	supported bool
	reason    string
	calls     []hostos.UpdateRequest
}

func (f *fakeUpdate) HostUpdate(_ context.Context, req hostos.UpdateRequest) (hostos.UpdateResult, error) {
	f.calls = append(f.calls, req)
	if !f.supported {
		reason := f.reason
		if reason == "" {
			reason = hostos.UpdateUnsupportedReason
		}
		return hostos.EvaluateUpdate(hostos.Platform{ID: "ubuntu", VersionID: "24.04", Architecture: "amd64"}, req), nil
	}
	p, _ := hostos.DetectFrom(strings.NewReader("ID=debian\nVERSION_ID=13\nPRETTY_NAME=\"Debian GNU/Linux 13\"\n"), "amd64")
	return hostos.RunUpdate(context.Background(), p, req, nil)
}

func TestUpdatesGetUsesStatusNotCheck(t *testing.T) {
	s, _, token := testServer(t)
	fu := &fakeUpdate{}
	s.Update = fu
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)
	req, _ := http.NewRequest("GET", ts.URL+"/api/v1/updates", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ := ts.Client().Do(req)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("%d", res.StatusCode)
	}
	if len(fu.calls) != 1 || fu.calls[0].Action != "status" {
		t.Fatalf("GET must not refresh package indexes: %+v", fu.calls)
	}
}

func TestUpdatesUnsupportedHostHonest(t *testing.T) {
	s, _, token := testServer(t)
	s.Update = &fakeUpdate{}
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)

	req, _ := http.NewRequest("GET", ts.URL+"/api/v1/updates", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("%d %s", res.StatusCode, b)
	}
	lower := strings.ToLower(string(b))
	if strings.Contains(lower, "apt-get") || strings.Contains(lower, "dpkg") || strings.Contains(lower, "yum") {
		t.Fatalf("public JSON leaked package manager verb: %s", b)
	}
	var body map[string]any
	if err := json.Unmarshal(b, &body); err != nil {
		t.Fatal(err)
	}
	if body["host_supported"] != false {
		t.Fatalf("%s", b)
	}
	if body["channel"] != "stable" {
		t.Fatalf("%s", b)
	}
	if !strings.Contains(body["host_reason"].(string), "Debian 13") {
		t.Fatalf("%s", b)
	}
}

func TestUpdatesCheckIsAlwaysDryRun(t *testing.T) {
	s, _, token := testServer(t)
	fu := &fakeUpdate{}
	s.Update = fu
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)
	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/updates/check", strings.NewReader("{}"))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ := ts.Client().Do(req)
	b, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("%d %s", res.StatusCode, b)
	}
	if !strings.Contains(string(b), `"dry_run":true`) {
		t.Fatalf("%s", b)
	}
	if len(fu.calls) != 1 || !fu.calls[0].DryRun || fu.calls[0].Action != "check" {
		t.Fatalf("%+v", fu.calls)
	}
}

func TestUpdatesApplyRequiresConfirm(t *testing.T) {
	s, mem, token := testServer(t)
	s.Update = &fakeUpdate{}
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)
	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/updates/apply", strings.NewReader("{}"))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ := ts.Client().Do(req)
	if res.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("missing confirm %d", res.StatusCode)
	}
	_ = res.Body.Close()

	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/updates/apply", strings.NewReader("{}"))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Nodal-Confirm", "apply-update")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(req)
	b, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("%d %s", res.StatusCode, b)
	}
	if strings.Contains(strings.ToLower(string(b)), "stop") {
		t.Fatalf("apply must not mention stopping guests: %s", b)
	}
	var op map[string]any
	if err := json.Unmarshal(b, &op); err != nil {
		t.Fatal(err)
	}
	if op["status"] != "unsupported" || op["action"] != "apply" || op["dry_run"] != false {
		t.Fatalf("%s", b)
	}
	cluster, _ := mem.GetCluster(context.Background())
	stored, err := mem.GetLatestUpdateOperation(context.Background(), cluster.ID)
	if err != nil || stored == nil || stored.Action != "apply" || stored.Status != "unsupported" {
		t.Fatalf("apply row %+v %v", stored, err)
	}
}

func TestUpdatesRollbackRequiresConfirm(t *testing.T) {
	s, _, token := testServer(t)
	s.Update = &fakeUpdate{}
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)
	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/updates/rollback", strings.NewReader("{}"))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ := ts.Client().Do(req)
	if res.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("missing confirm %d", res.StatusCode)
	}
	_ = res.Body.Close()
}

func TestUpdatesViewerForbidden(t *testing.T) {
	s, mem, token := testServer(t)
	s.Update = &fakeUpdate{}
	cluster, _ := mem.GetCluster(context.Background())
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	_ = claimAdmin(t, ts, token)
	hash, err := auth.HashPassword("password1")
	if err != nil {
		t.Fatal(err)
	}
	u := appdb.User{ID: uuid.NewString(), ClusterID: cluster.ID, Username: "view", PasswordHash: hash}
	_ = mem.CreateUser(context.Background(), u)
	_ = mem.BindRole(context.Background(), cluster.ID, u.ID, rbac.Viewer)
	login, _ := ts.Client().Post(ts.URL+"/api/v1/auth/login", "application/json", strings.NewReader(`{"username":"view","password":"password1"}`))
	var viewCookie string
	for _, c := range login.Cookies() {
		if c.Name == sessionCookie {
			viewCookie = c.Value
		}
	}
	_ = login.Body.Close()

	req, _ := http.NewRequest("GET", ts.URL+"/api/v1/updates", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: viewCookie})
	res, _ := ts.Client().Do(req)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("viewer GET %d", res.StatusCode)
	}
	_ = res.Body.Close()

	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/updates/check", strings.NewReader("{}"))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: viewCookie})
	res, _ = ts.Client().Do(req)
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("viewer check %d", res.StatusCode)
	}
	_ = res.Body.Close()
}

func TestUpdatesCheckpointUnsupportedNoFakeDump(t *testing.T) {
	s, _, token := testServer(t)
	s.Update = &fakeUpdate{}
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)
	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/updates/checkpoint", strings.NewReader("{}"))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ := ts.Client().Do(req)
	b, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("%d %s", res.StatusCode, b)
	}
	var body map[string]any
	if err := json.Unmarshal(b, &body); err != nil {
		t.Fatal(err)
	}
	if body["status"] != "unsupported" {
		t.Fatalf("%s", b)
	}
	if body["postgres_dump"] != false {
		t.Fatalf("must not fake a dump: %s", b)
	}
}

func TestUpdatesPreflightStoreHook(t *testing.T) {
	s, _, token := testServer(t)
	s.Update = &fakeUpdate{}
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)
	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/updates/preflight", strings.NewReader("{}"))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ := ts.Client().Do(req)
	b, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("%d %s", res.StatusCode, b)
	}
	if !strings.Contains(string(b), "store_compatibility") || !strings.Contains(string(b), "Helper scripts") {
		t.Fatalf("%s", b)
	}
	var body map[string]any
	if err := json.Unmarshal(b, &body); err != nil {
		t.Fatal(err)
	}
	if body["ok"] != false {
		t.Fatalf("unsupported host preflight must not be ok: %s", b)
	}
}

func TestUpdatesPreflightStoreNotImplemented(t *testing.T) {
	s, _, token := testServer(t)
	s.Update = &fakeUpdate{supported: true}
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)
	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/updates/preflight", strings.NewReader("{}"))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ := ts.Client().Do(req)
	b, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("%d %s", res.StatusCode, b)
	}
	var body struct {
		Checks []struct {
			Name   string `json:"name"`
			Status string `json:"status"`
			Detail string `json:"detail"`
		} `json:"checks"`
	}
	if err := json.Unmarshal(b, &body); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, c := range body.Checks {
		if c.Name != "store_compatibility" {
			continue
		}
		found = true
		if c.Status == "ok" {
			t.Fatalf("store check must not be ok: %s", b)
		}
		if c.Status != "skipped" && c.Status != "unavailable" {
			t.Fatalf("store check status %q: %s", c.Status, b)
		}
		if !strings.Contains(c.Detail, "not implemented") {
			t.Fatalf("store detail %q", c.Detail)
		}
	}
	if !found {
		t.Fatalf("store_compatibility missing: %s", b)
	}
}

func TestUpdatesApplySupportedDoesNotStopGuests(t *testing.T) {
	s, _, token := testServer(t)
	s.Update = &fakeUpdate{supported: true}
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)
	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/updates/apply", strings.NewReader("{}"))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Nodal-Confirm", "apply-update")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ := ts.Client().Do(req)
	b, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("%d %s", res.StatusCode, b)
	}
	lower := strings.ToLower(string(b))
	if strings.Contains(lower, "apt-get") || strings.Contains(lower, "stop") {
		t.Fatalf("%s", b)
	}
	var op map[string]any
	if err := json.Unmarshal(b, &op); err != nil {
		t.Fatal(err)
	}
	if op["status"] != "succeeded" || op["action"] != "apply" {
		t.Fatalf("%s", b)
	}
}

type failUpdateUpdateOperationStore struct {
	appdb.Store
}

func (f failUpdateUpdateOperationStore) UpdateUpdateOperation(context.Context, appdb.UpdateOperation) error {
	return errors.New("persist failed")
}

func TestUpdatesApplyFailsClosedWhenPersistFails(t *testing.T) {
	s, mem, token := testServer(t)
	s.Update = &fakeUpdate{supported: true}
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)
	s.Store = failUpdateUpdateOperationStore{Store: mem}

	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/updates/apply", strings.NewReader("{}"))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Nodal-Confirm", "apply-update")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ := ts.Client().Do(req)
	b, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusInternalServerError {
		t.Fatalf("apply persist %d %s", res.StatusCode, b)
	}
	if !strings.Contains(string(b), "could not record update operation") {
		t.Fatalf("apply persist body %s", b)
	}
}
