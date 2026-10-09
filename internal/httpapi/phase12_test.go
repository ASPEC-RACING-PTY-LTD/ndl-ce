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
	"time"

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
	result    *hostos.UpdateResult
}

func (f *fakeUpdate) HostUpdate(_ context.Context, req hostos.UpdateRequest) (hostos.UpdateResult, error) {
	f.calls = append(f.calls, req)
	if f.result != nil {
		return *f.result, nil
	}
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

func TestBackgroundUpdateCheckPersistsCandidatesAndNotifiesOnTransitions(t *testing.T) {
	s, mem, token := testServer(t)
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	claimAdmin(t, ts, token)
	cluster, err := mem.GetCluster(context.Background())
	if err != nil || cluster == nil {
		t.Fatalf("cluster: %+v %v", cluster, err)
	}
	candidate := appdb.UpdateCandidate{Name: "nodal", CurrentVersion: "1.0.1", CandidateVersion: "1.0.2"}
	fake := &fakeUpdate{result: &hostos.UpdateResult{
		Supported: true,
		Status:    appdb.UpdateSucceeded,
		Version:   "1.0.1",
		Items: []hostos.PreviewItem{{
			Name: candidate.Name, CurrentVersion: candidate.CurrentVersion,
			CandidateVersion: candidate.CandidateVersion, Action: "upgrade",
		}},
	}}
	s.Update = fake

	s.TickUpdateCheck(context.Background())
	check, err := mem.GetLatestCheckUpdateOperation(context.Background(), cluster.ID)
	if err != nil || check == nil || len(check.Candidates) != 1 || check.Candidates[0] != candidate {
		t.Fatalf("scheduled candidate not persisted: %+v %v", check, err)
	}
	if check.Version != "1.0.1" {
		t.Fatalf("installed version used by rollback was changed: %+v", check)
	}
	assertUpdateEventCount(t, mem, cluster.ID, "update.available", 1)

	s.TickUpdateCheck(context.Background())
	assertUpdateEventCount(t, mem, cluster.ID, "update.available", 1)

	fake.result = &hostos.UpdateResult{Supported: true, Status: appdb.UpdateSucceeded, Version: "1.0.2"}
	s.TickUpdateCheck(context.Background())
	assertUpdateEventCount(t, mem, cluster.ID, "update.current", 1)
}

func assertUpdateEventCount(t *testing.T, mem *appdb.Memory, clusterID, eventType string, want int) {
	t.Helper()
	events, err := mem.ListEvents(context.Background(), clusterID, 100)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, event := range events {
		if event.Type == eventType {
			count++
		}
	}
	if count != want {
		t.Fatalf("%s count=%d, want %d (events=%+v)", eventType, count, want, events)
	}
}

func TestUpdatesGetUsesStatusNotCheck(t *testing.T) {
	s, mem, token := testServer(t)
	fu := &fakeUpdate{}
	s.Update = fu
	cluster, err := mem.GetCluster(context.Background())
	if err != nil || cluster == nil {
		t.Fatalf("cluster: %+v %v", cluster, err)
	}
	if err := mem.CreateUpdateOperation(context.Background(), appdb.UpdateOperation{
		ID: uuid.NewString(), ClusterID: cluster.ID, Action: "check", Status: appdb.UpdateSucceeded,
		Version: "1.0.1", Candidates: []appdb.UpdateCandidate{{
			Name: "nodal", CurrentVersion: "1.0.1", CandidateVersion: "1.0.2",
		}}, StartedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)
	req, _ := http.NewRequest("GET", ts.URL+"/api/v1/updates", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("%d %s", res.StatusCode, body)
	}
	if len(fu.calls) != 1 || fu.calls[0].Action != "status" {
		t.Fatalf("GET must not refresh package indexes: %+v", fu.calls)
	}
	if !strings.Contains(string(body), `"last_check"`) || !strings.Contains(string(body), `"candidate_version":"1.0.2"`) {
		t.Fatalf("GET omitted persisted update candidates: %s", body)
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
	if op["status"] != "running" || op["action"] != "apply" || op["dry_run"] != false {
		t.Fatalf("%s", b)
	}
	s.waitHostChange()
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
	if res.StatusCode != http.StatusForbidden {
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

func TestUpdatesPreflightAddsControlChecks(t *testing.T) {
	s, _, token := testServer(t)
	s.Update = &fakeUpdate{supported: true}
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)
	_, body := updatesCall(t, ts, cookie, "POST", "/updates/preflight", "")
	names := map[string]string{}
	for _, c := range body["checks"].([]any) {
		row := c.(map[string]any)
		names[row["name"].(string)] = row["status"].(string)
	}
	if names["store_compatibility"] != "ok" || names["running_work"] != "ok" || names["backups"] != "ok" {
		t.Fatalf("preflight must report store, running work and backups: %v", names)
	}
}

func TestUpdatesApplySupportedDoesNotStopGuests(t *testing.T) {
	s, _, token := testServer(t)
	s.Update = &detachedUpdate{applyState: appdb.UpdateSucceeded}
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)
	code, op := updatesCall(t, ts, cookie, "POST", "/updates/apply", "apply-update")
	if code != http.StatusOK || op["action"] != "apply" {
		t.Fatalf("%d %v", code, op)
	}
	s.waitHostChange()
	raw, _ := json.Marshal(op)
	if strings.Contains(strings.ToLower(string(raw)), "apt-get") || strings.Contains(strings.ToLower(string(raw)), "stop") {
		t.Fatalf("%s", raw)
	}
	_, listed := updatesCall(t, ts, cookie, "GET", "/updates", "")
	last, _ := listed["last_operation"].(map[string]any)
	if last == nil || last["id"] != op["id"] || last["status"] != "succeeded" {
		t.Fatalf("apply must settle to the host result: %v", listed)
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
	code, _ := updatesCall(t, ts, cookie, "POST", "/updates/apply", "apply-update")
	if code != http.StatusOK {
		t.Fatalf("apply %d", code)
	}
	s.waitHostChange()
	_, listed := updatesCall(t, ts, cookie, "GET", "/updates", "")
	if last, _ := listed["last_operation"].(map[string]any); last == nil || last["status"] == "succeeded" {
		t.Fatalf("GET must not claim succeeded when the result could not be stored: %v", listed)
	}
}

type missUpdateUpdateOperationStore struct {
	appdb.Store
}

func (missUpdateUpdateOperationStore) UpdateUpdateOperation(context.Context, appdb.UpdateOperation) error {
	return nil
}

func TestUpdatesApplyFailsClosedWhenPersistMisses(t *testing.T) {
	s, mem, token := testServer(t)
	s.Update = &fakeUpdate{supported: true}
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)
	s.Store = missUpdateUpdateOperationStore{Store: mem}
	if code, _ := updatesCall(t, ts, cookie, "POST", "/updates/apply", "apply-update"); code != http.StatusOK {
		t.Fatalf("apply %d", code)
	}
	s.waitHostChange()
	_, listed := updatesCall(t, ts, cookie, "GET", "/updates", "")
	last, _ := listed["last_operation"].(map[string]any)
	if last == nil || last["status"] != "running" {
		t.Fatalf("GET must stay running when the result was not stored: %v", listed)
	}
}

type failCreateUpdateOperationStore struct {
	appdb.Store
}

func (f failCreateUpdateOperationStore) CreateUpdateOperation(context.Context, appdb.UpdateOperation) error {
	return errors.New("persist failed")
}

func TestUpdatesApplyFailsClosedWhenCreatePersistFails(t *testing.T) {
	s, mem, token := testServer(t)
	fu := &fakeUpdate{supported: true}
	s.Update = fu
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)
	s.Store = failCreateUpdateOperationStore{Store: mem}

	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/updates/apply", strings.NewReader("{}"))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Nodal-Confirm", "apply-update")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ := ts.Client().Do(req)
	b, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusInternalServerError {
		t.Fatalf("apply create persist %d %s", res.StatusCode, b)
	}
	if !strings.Contains(string(b), "could not record update operation") {
		t.Fatalf("apply create persist body %s", b)
	}
	for _, call := range fu.calls {
		if call.Action == "apply" {
			t.Fatalf("host apply must not run when the operation row cannot be recorded: %+v", fu.calls)
		}
	}
	get, _ := http.NewRequest("GET", ts.URL+"/api/v1/updates", nil)
	get.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	got, _ := ts.Client().Do(get)
	raw, _ := io.ReadAll(got.Body)
	_ = got.Body.Close()
	if strings.Contains(string(raw), `"action":"apply"`) {
		t.Fatalf("GET last_operation must not invent apply %s", raw)
	}
}

func TestUpdatesRollbackUsesTheVersionsTheApplyReplaced(t *testing.T) {
	for _, tc := range []struct {
		name      string
		schema    string
		restoreDB bool
	}{
		{"same schema keeps data", latestSchemaVersion(), false},
		{"changed schema restores database", "0001_init.sql", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, mem, token := testServer(t)
			host := &detachedUpdate{applyState: appdb.UpdateRunning}
			s.Update = host
			cluster, _ := mem.GetCluster(context.Background())
			apply := appdb.UpdateOperation{
				ID: uuid.NewString(), ClusterID: cluster.ID, Action: "apply", Status: appdb.UpdateSucceeded,
				Version: "1.1.5", CheckpointID: "cp-1", SchemaVersion: tc.schema,
				Candidates: []appdb.UpdateCandidate{
					{Name: "ndl-control", CurrentVersion: "1.1.4", CandidateVersion: "1.1.5"},
					{Name: "nodal", CurrentVersion: "1.1.4", CandidateVersion: "1.1.5"},
				},
				StartedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
			}
			if err := mem.CreateUpdateOperation(context.Background(), apply); err != nil {
				t.Fatal(err)
			}
			for i := 0; i < 60; i++ {
				_ = mem.CreateUpdateOperation(context.Background(), appdb.UpdateOperation{
					ID: uuid.NewString(), ClusterID: cluster.ID, Action: "check", Status: appdb.UpdateSucceeded,
					Version: "1.1.5", StartedAt: time.Date(2026, 2, 1, 0, i, 0, 0, time.UTC),
				})
			}
			ts := httptest.NewServer(s.Handler())
			defer ts.Close()
			cookie := claimAdmin(t, ts, token)
			_, status := updatesCall(t, ts, cookie, "GET", "/updates", "")
			rb := status["rollback"].(map[string]any)
			if rb["available"] != true || rb["version"] != "1.1.4" || rb["restores_database"] != tc.restoreDB {
				t.Fatalf("rollback plan %v", rb)
			}
			code, op := updatesCall(t, ts, cookie, "POST", "/updates/rollback", "rollback-update")
			if code != http.StatusOK || op["status"] != "running" {
				t.Fatalf("rollback %d %v", code, op)
			}
			s.waitHostChange()
			if len(host.reqs) != 1 || host.reqs[0].Action != "rollback" || host.reqs[0].Version != "1.1.4" {
				t.Fatalf("rollback must move every package back to 1.1.4: %+v", host.reqs)
			}
			if wantCP := map[bool]string{true: "cp-1", false: ""}[tc.restoreDB]; host.reqs[0].CheckpointID != wantCP {
				t.Fatalf("checkpoint %q, want %q", host.reqs[0].CheckpointID, wantCP)
			}
		})
	}
}

func TestUpdatesRollbackRefusesWithoutARecordedApply(t *testing.T) {
	s, _, token := testServer(t)
	host := &detachedUpdate{}
	s.Update = host
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)
	_, status := updatesCall(t, ts, cookie, "GET", "/updates", "")
	if rb := status["rollback"].(map[string]any); rb["available"] != false || rb["reason"] == "" {
		t.Fatalf("rollback must be unavailable: %v", rb)
	}
	if code, _ := updatesCall(t, ts, cookie, "POST", "/updates/rollback", "rollback-update"); code != http.StatusConflict {
		t.Fatalf("rollback without a recorded apply must be refused, got %d", code)
	}
	if len(host.reqs) != 0 {
		t.Fatalf("nothing may run: %+v", host.reqs)
	}
}
