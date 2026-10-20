package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/no-dal/ndl-ce/internal/appdb"
	"github.com/no-dal/ndl-ce/internal/guestextras"
	"github.com/no-dal/ndl-ce/internal/rbac"
)

// Round 4: appearance, design profiles, search, impact notices, the
// rollback window and wipe, and settings templates.

func TestAppearanceSettingsResolveByRole(t *testing.T) {
	a := newAutoTest(t)
	ops := loginRole(t, a.ts, a.mem, "ops", rbac.Admin)

	if code, _ := a.as(ops, "PUT", "/api/v1/settings/appearance", `{"nav_layout":"classic"}`); code != http.StatusForbidden {
		t.Fatalf("an admin changed global appearance: %d", code)
	}
	body := `{"theme":{"mode":"dark","style":"plain"},"role_themes":{"admin":{"mode":"light","style":"glass"}},"nav_layout":"classic","profile_limit":2}`
	if code, out := a.call("PUT", "/api/v1/settings/appearance", body, nil); code != http.StatusOK {
		t.Fatalf("save appearance %d %v", code, out["_raw"])
	}
	code, mine := a.as(ops, "GET", "/api/v1/me/appearance", "")
	if code != http.StatusOK {
		t.Fatalf("me/appearance %d", code)
	}
	theme, _ := mine["theme"].(map[string]any)
	if theme["style"] != "glass" || mine["nav_layout"] != "classic" || mine["profile_limit"] != float64(2) {
		t.Fatalf("an admin should get the admin role theme and the defaults: %v", mine["_raw"])
	}
	// null clears the global theme.
	if code, out := a.call("PUT", "/api/v1/settings/appearance", `{"theme":null}`, nil); code != http.StatusOK {
		t.Fatalf("clear %d %v", code, out["_raw"])
	}
	_, all := a.call("GET", "/api/v1/settings/appearance", "", nil)
	if all["theme"] != nil {
		t.Fatalf("theme not cleared: %v", all["_raw"])
	}
}

func TestAppearanceRejectsUnsafeThemesAndLimits(t *testing.T) {
	a := newAutoTest(t)
	for _, body := range []string{
		`{"theme":{"tokens":{"--accent":"url(https://evil.example/x)"}}}`,
		`{"theme":{"tokens":{"accent":"red"}}}`,
		`{"theme":{"script":"x"}}`,
		`{"theme":{"radius":99}}`,
		`{"profile_limit":4}`,
		`{"profile_limit":0}`,
	} {
		if code, out := a.call("PUT", "/api/v1/settings/appearance", body, nil); code != http.StatusBadRequest {
			t.Fatalf("%s should be refused: %d %v", body, code, out["_raw"])
		}
	}
}

func TestCheckDesignProfiles(t *testing.T) {
	p := func(ids ...string) json.RawMessage {
		var out []map[string]any
		for _, id := range ids {
			out = append(out, map[string]any{"id": id, "name": "Profile " + id, "data": map[string]any{"theme": id}})
		}
		b, _ := json.Marshal(out)
		return b
	}
	if err := checkDesignProfiles(nil, p("a", "b", "c"), 3); err != nil {
		t.Fatalf("three under a limit of three: %v", err)
	}
	if err := checkDesignProfiles(nil, p("a", "b", "c", "d"), 3); err == nil {
		t.Fatal("a fourth profile must be refused")
	}
	if err := checkDesignProfiles(nil, p("a", "a"), 3); err == nil {
		t.Fatal("duplicate ids must be refused")
	}
	// The limit was lowered to 1: the extras stay, read-only, and may be deleted.
	if err := checkDesignProfiles(p("a", "b", "c"), p("a", "b", "c"), 1); err != nil {
		t.Fatalf("unchanged extras over a lowered limit: %v", err)
	}
	if err := checkDesignProfiles(p("a", "b", "c"), p("a", "b"), 1); err != nil {
		t.Fatalf("deleting an extra over a lowered limit: %v", err)
	}
	changed := json.RawMessage(`[{"id":"a","name":"Profile a","data":{"theme":"a"}},{"id":"b","name":"Renamed","data":{"theme":"b"}}]`)
	if err := checkDesignProfiles(p("a", "b"), changed, 1); err == nil {
		t.Fatal("changing a read-only extra must be refused")
	}
}

func TestParseImpactNotices(t *testing.T) {
	log := "## 1.2.0\n* Fixed things\n* Impact (automation): Flows are turned off\n  until reviewed.\n* Impact: Restart needed\n"
	got := parseImpactNotices(log)
	if len(got) != 2 {
		t.Fatalf("want 2 notices, got %+v", got)
	}
	if got[0].Audience != "automation" || got[0].Text != "Flows are turned off until reviewed." {
		t.Fatalf("first notice %+v", got[0])
	}
	if got[1].Audience != audienceUpdates || got[1].ID == got[0].ID {
		t.Fatalf("second notice %+v", got[1])
	}
}

func TestSearchFollowsPermissions(t *testing.T) {
	a := newAutoTest(t)
	viewer := loginRole(t, a.ts, a.mem, "viewer-person", rbac.User)
	if err := a.s.refreshPrimary(context.Background(), a.cluster); err != nil {
		t.Fatal(err)
	}
	_, admin := a.call("GET", "/api/v1/search?q=viewer-person", "", nil)
	if !strings.Contains(admin["_raw"].(string), `"kind":"user"`) {
		t.Fatalf("a SuperAdmin should find the user: %v", admin["_raw"])
	}
	code, mine := a.as(viewer, "GET", "/api/v1/search?q=viewer-person", "")
	if code == http.StatusOK && strings.Contains(mine["_raw"].(string), `"kind":"user"`) {
		t.Fatalf("a user without users.read must not find users: %v", mine["_raw"])
	}
	if code, _ := a.as(viewer, "PUT", "/api/v1/search/settings", `{"deep":{"files":true}}`); code != http.StatusForbidden {
		t.Fatalf("a user changed search settings: %d", code)
	}
}

func TestRollbackWipeIsSuperAdminOnlyAndConfirmed(t *testing.T) {
	a := newAutoTest(t)
	host := &detachedUpdate{applyState: appdb.UpdateRunning}
	a.s.Update = host
	apply := appdb.UpdateOperation{
		ID: uuid.NewString(), ClusterID: a.cluster, Action: "apply", Status: appdb.UpdateSucceeded,
		Version: "1.1.5", CheckpointID: uuid.NewString(), SchemaVersion: latestSchemaVersion(),
		Candidates: []appdb.UpdateCandidate{{Name: "nodal", CurrentVersion: "1.1.4", CandidateVersion: "1.1.5"}},
		StartedAt:  time.Now().UTC().Add(-time.Hour),
	}
	if err := a.mem.CreateUpdateOperation(context.Background(), apply); err != nil {
		t.Fatal(err)
	}
	ops := loginRole(t, a.ts, a.mem, "ops", rbac.Admin)
	if code, _ := a.as(ops, "POST", "/api/v1/updates/rollback/wipe", ""); code != http.StatusForbidden {
		t.Fatalf("an admin wiped the rollback: %d", code)
	}
	if code, _ := a.call("POST", "/api/v1/updates/rollback/wipe", "", nil); code != http.StatusUnprocessableEntity {
		t.Fatalf("a wipe without the confirm header: %d", code)
	}
	code, out := a.call("POST", "/api/v1/updates/rollback/wipe", "", map[string]string{"X-Nodal-Confirm": "wipe-rollback"})
	if code != http.StatusOK {
		t.Fatalf("wipe %d %v", code, out["_raw"])
	}
	if !slices.Contains(host.calls, "checkpoint-wipe") {
		t.Fatalf("the host was not asked to delete the checkpoint: %v", host.calls)
	}
	_, status := a.call("GET", "/api/v1/updates", "", nil)
	rb, _ := status["rollback"].(map[string]any)
	if rb["available"] == true {
		t.Fatalf("rollback must be unavailable after a wipe: %v", rb)
	}
	if code, _ := a.call("POST", "/api/v1/updates/rollback/wipe", "", map[string]string{"X-Nodal-Confirm": "wipe-rollback"}); code != http.StatusConflict {
		t.Fatalf("a second wipe: %d", code)
	}
}

func TestRollbackWindowCloses(t *testing.T) {
	a := newAutoTest(t)
	a.s.Update = &detachedUpdate{applyState: appdb.UpdateRunning}
	apply := appdb.UpdateOperation{
		ID: uuid.NewString(), ClusterID: a.cluster, Action: "apply", Status: appdb.UpdateSucceeded,
		Version: "1.1.5", CheckpointID: uuid.NewString(), SchemaVersion: latestSchemaVersion(),
		Candidates: []appdb.UpdateCandidate{{Name: "nodal", CurrentVersion: "1.1.4", CandidateVersion: "1.1.5"}},
		StartedAt:  time.Now().UTC().Add(-rollbackWindow - time.Hour),
	}
	if err := a.mem.CreateUpdateOperation(context.Background(), apply); err != nil {
		t.Fatal(err)
	}
	_, status := a.call("GET", "/api/v1/updates", "", nil)
	rb, _ := status["rollback"].(map[string]any)
	if rb["available"] == true || !strings.Contains(rb["reason"].(string), "window closed") {
		t.Fatalf("rollback after the window: %v", rb)
	}
}

func TestInstanceTemplates(t *testing.T) {
	a := newAutoTest(t)
	code, made := a.call("POST", "/api/v1/instance-templates",
		`{"kind":"system-container","name":"web","spec":{"image_pin":"debian/trixie/amd64/default","cpus":2,"extras":["docker","curl"],"surprise":true}}`, nil)
	if code != http.StatusCreated {
		t.Fatalf("create %d %v", code, made["_raw"])
	}
	spec, _ := made["spec"].(map[string]any)
	if spec["surprise"] != nil || spec["cpus"] != float64(2) {
		t.Fatalf("unknown keys must be dropped: %v", made["_raw"])
	}
	extras, _ := spec["extras"].([]any)
	if len(extras) != 1 || extras[0] != guestextras.Docker {
		t.Fatalf("baseline tools are not saved in a template: %v", spec["extras"])
	}
	for _, bad := range []string{
		`{"kind":"vm","name":"x"}`,
		`{"kind":"system-container","name":""}`,
		`{"kind":"system-container","name":"x","spec":{"extras":["nope"]}}`,
		`{"kind":"oci","name":"x","spec":{"cpus":9999}}`,
		`{"kind":"game-server","name":"x","spec":{"env":{"A=B":"c"}}}`,
	} {
		if code, out := a.call("POST", "/api/v1/instance-templates", bad, nil); code != http.StatusBadRequest {
			t.Fatalf("%s should be refused: %d %v", bad, code, out["_raw"])
		}
	}
	if code, _ := a.call("POST", "/api/v1/instance-templates", `{"kind":"oci","name":"app","spec":{"image":"nginx:1"}}`, nil); code != http.StatusCreated {
		t.Fatalf("oci template %d", code)
	}
	_, list := a.call("GET", "/api/v1/instance-templates?kind=oci", "", nil)
	items, _ := list["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("kind filter: %v", list["_raw"])
	}
	viewer := loginRole(t, a.ts, a.mem, "viewer-person", rbac.User)
	if code, _ := a.as(viewer, "DELETE", "/api/v1/instance-templates/"+made["id"].(string), ""); code != http.StatusForbidden {
		t.Fatalf("a user deleted a template: %d", code)
	}
	if code, _ := a.call("DELETE", "/api/v1/instance-templates/"+made["id"].(string), "", nil); code != http.StatusNoContent {
		t.Fatalf("delete %d", code)
	}
	if code, _ := a.call("GET", "/api/v1/instance-templates/"+made["id"].(string), "", nil); code != http.StatusNotFound {
		t.Fatalf("deleted template still found: %d", code)
	}
}

// A rollback saves a checkpoint first, and undoing it puts the newer
// version back with the database from right before the rollback.
func TestRollbackCanBeUndoneFromItsCheckpoint(t *testing.T) {
	a := newAutoTest(t)
	host := &detachedUpdate{applyState: appdb.UpdateRunning}
	a.s.Update = host
	apply := appdb.UpdateOperation{
		ID: uuid.NewString(), ClusterID: a.cluster, Action: "apply", Status: appdb.UpdateSucceeded,
		Version: "1.1.5", CheckpointID: uuid.NewString(), SchemaVersion: "0001_init.sql",
		Candidates: []appdb.UpdateCandidate{{Name: "nodal", CurrentVersion: "1.1.4", CandidateVersion: "1.1.5"}},
		StartedAt:  time.Now().UTC().Add(-time.Hour),
	}
	if err := a.mem.CreateUpdateOperation(context.Background(), apply); err != nil {
		t.Fatal(err)
	}
	_, status := a.call("GET", "/api/v1/updates", "", nil)
	if u, _ := status["undo_rollback"].(map[string]any); u["available"] != false {
		t.Fatalf("nothing to undo before a rollback: %v", u)
	}
	if code, out := a.call("POST", "/api/v1/updates/rollback", "", map[string]string{"X-Nodal-Confirm": "rollback-update"}); code != http.StatusOK {
		t.Fatalf("rollback %d %v", code, out["_raw"])
	}
	a.s.waitHostChange()
	if len(host.calls) < 2 || host.calls[len(host.calls)-2] != "checkpoint" || host.calls[len(host.calls)-1] != "rollback" {
		t.Fatalf("a rollback must save a checkpoint first: %v", host.calls)
	}
	rb, _ := a.mem.GetLatestUpdateOperationByAction(context.Background(), a.cluster, "rollback")
	if rb == nil || rb.CheckpointID == "" || rb.CheckpointID == apply.CheckpointID || !rb.RestoredDB {
		t.Fatalf("the rollback must record its own checkpoint and that it restored the database: %+v", rb)
	}
	// The host unit finished the rollback.
	now := time.Now().UTC()
	rb.Status, rb.FinishedAt = appdb.UpdateSucceeded, &now
	if err := a.mem.UpdateUpdateOperation(context.Background(), *rb); err != nil {
		t.Fatal(err)
	}
	_, status = a.call("GET", "/api/v1/updates", "", nil)
	undo, _ := status["undo_rollback"].(map[string]any)
	if undo["available"] != true || undo["version"] != "1.1.5" || undo["restores_database"] != true {
		t.Fatalf("undo plan %v", undo)
	}
	if code, _ := a.call("POST", "/api/v1/updates/rollback/undo", "", nil); code != http.StatusUnprocessableEntity {
		t.Fatalf("an undo without the confirm header: %d", code)
	}
	before := len(host.reqs)
	if code, out := a.call("POST", "/api/v1/updates/rollback/undo", "", map[string]string{"X-Nodal-Confirm": "undo-rollback"}); code != http.StatusOK || out["action"] != "undo-rollback" {
		t.Fatalf("undo %d %v", code, out["_raw"])
	}
	a.s.waitHostChange()
	if len(host.reqs) != before+1 {
		t.Fatalf("undo did not reach the host: %+v", host.reqs)
	}
	req := host.reqs[len(host.reqs)-1]
	if req.Action != "rollback" || req.Version != "1.1.5" || req.CheckpointID != rb.CheckpointID {
		t.Fatalf("undo must put 1.1.5 back with the database saved before the rollback: %+v", req)
	}
	// The rollback's copy expires with the same 3-day window.
	a.s.Now = func() time.Time { return time.Now().UTC().Add(rollbackWindow + 2*time.Hour) }
	a.s.wipeExpiredCheckpoint(context.Background(), a.cluster)
	if at, _ := a.mem.CheckpointWiped(context.Background(), rb.ID); at == nil {
		t.Fatal("the rollback's checkpoint must be wiped after 3 days")
	}
}
