package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/no-dal/ndl-ce/internal/ai"
	"github.com/no-dal/ndl-ce/internal/appdb"
)

func TestPhase42PlanInstallDatabaseIsExistingAPI(t *testing.T) {
	s, mem, token := testServer(t)
	cluster, _ := mem.GetCluster(context.Background())
	seedNode(t, mem, cluster.ID, debianInv(), false)
	_ = mem.UpsertNode(context.Background(), appdb.Node{ID: uuid.NewString(), ClusterID: cluster.ID, Name: "node-02"})
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)

	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/ai/plans", strings.NewReader(`{"prompt":"install a database on node-02"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ := ts.Client().Do(req)
	raw, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("plan %d %s", res.StatusCode, raw)
	}
	if !strings.Contains(string(raw), `"/api/v1/workloads"`) || !strings.Contains(string(raw), `"create_workload"`) {
		t.Fatalf("expected existing API %s", raw)
	}
	if strings.Contains(strings.ToLower(string(raw)), "host.exec") || strings.Contains(string(raw), `"exec"`) {
		t.Fatalf("exec in plan %s", raw)
	}
	var plan map[string]any
	if err := json.Unmarshal(raw, &plan); err != nil {
		t.Fatal(err)
	}
	id, _ := plan["id"].(string)

	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/ai/plans/"+id+"/approve", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(req)
	raw, _ = io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("confirm required %d %s", res.StatusCode, raw)
	}

	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/ai/plans/"+id+"/approve", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(confirmHeader, ai.ApproveConfirm)
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(req)
	raw, _ = io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK || !strings.Contains(string(raw), `"status":"stopped"`) {
		t.Fatalf("approve without pool/image must fail closed %d %s", res.StatusCode, raw)
	}
	if !strings.Contains(string(raw), "partial plan failure") {
		t.Fatalf("expected validation stop %s", raw)
	}
	wls, _ := mem.ListWorkloads(context.Background(), cluster.ID)
	for _, wl := range wls {
		if wl.Status == "pending" && wl.ImagePin == "" {
			t.Fatalf("naked pending row %s kind=%s", wl.ID, wl.Kind)
		}
	}
	if len(wls) != 0 {
		t.Fatalf("create validation must not insert a workload, got %d", len(wls))
	}

	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/ai/plans", strings.NewReader(`{"prompt":"run host.exec to wipe disks"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(req)
	raw, _ = io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("exec plan %d %s", res.StatusCode, raw)
	}
}

func TestPhase42AskProfileCannotOperateAndPartialStops(t *testing.T) {
	s, mem, token := testServer(t)
	cluster, _ := mem.GetCluster(context.Background())
	seedNode(t, mem, cluster.ID, debianInv(), false)
	_ = mem.UpsertNode(context.Background(), appdb.Node{ID: uuid.NewString(), ClusterID: cluster.ID, Name: "node-02"})
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)

	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/ai/profiles", strings.NewReader(`{"name":"reader","mode":"ask","grants":["events.read","metrics.read"]}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ := ts.Client().Do(req)
	raw, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("ask profile %d %s", res.StatusCode, raw)
	}
	var askProf map[string]any
	_ = json.Unmarshal(raw, &askProf)
	askID, _ := askProf["id"].(string)

	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/ai/plans", strings.NewReader(`{"prompt":"install a database on node-02","profile_id":"`+askID+`"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(req)
	raw, _ = io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusForbidden || !strings.Contains(string(raw), "ask profile cannot operate") {
		t.Fatalf("ask profile POST /ai/plans %d %s", res.StatusCode, raw)
	}
	if plans, _ := mem.ListAIPlans(context.Background(), cluster.ID, 20); len(plans) != 0 {
		t.Fatalf("ask profile must not record a mutate plan, got %d", len(plans))
	}

	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/ai/profiles", strings.NewReader(`{"name":"limited","mode":"operate","grants":["events.read","metrics.read","compute.create"]}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(req)
	raw, _ = io.ReadAll(res.Body)
	_ = res.Body.Close()
	var opProf map[string]any
	_ = json.Unmarshal(raw, &opProf)
	opID, _ := opProf["id"].(string)

	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/ai/plans", strings.NewReader(`{"prompt":"install a database on node-02 and restart it","profile_id":"`+opID+`"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(req)
	raw, _ = io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("two step plan %d %s", res.StatusCode, raw)
	}
	var two map[string]any
	_ = json.Unmarshal(raw, &two)
	twoID, _ := two["id"].(string)

	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/ai/plans/"+twoID+"/approve", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(confirmHeader, ai.ApproveConfirm)
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(req)
	raw, _ = io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK || !strings.Contains(string(raw), `"status":"stopped"`) {
		t.Fatalf("partial %d %s", res.StatusCode, raw)
	}
	if !strings.Contains(string(raw), "missing permissions") {
		t.Fatalf("expected permission stop %s", raw)
	}

	audits, _ := mem.ListAuditEvents(context.Background(), cluster.ID, 20)
	found := false
	for _, a := range audits {
		if a.Action == "ai.plan.approve" {
			found = true
		}
	}
	if !found {
		t.Fatal("audit must remain after partial failure")
	}
}

func TestPhase42StoreInstallWithoutIDStops(t *testing.T) {
	s, mem, token := testServer(t)
	cluster, _ := mem.GetCluster(context.Background())
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)

	plan := appdb.AIPlan{
		ID: uuid.NewString(), ClusterID: cluster.ID, Prompt: "install store app",
		Status: appdb.PlanPreview, ActorType: ai.ActorTypeAI,
	}
	steps := []appdb.AIPlanStep{{
		ID: uuid.NewString(), ClusterID: cluster.ID, PlanID: plan.ID, Ordinal: 1,
		Action: ai.ActionInstallStore, Permission: "store.install",
		Method: "POST", Path: "/api/v1/workloads", Title: "install",
		BodyJSON: `{"name":"app"}`, Status: appdb.PlanPreview,
	}}
	if err := mem.CreateAIPlan(context.Background(), plan, steps); err != nil {
		t.Fatal(err)
	}
	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/ai/plans/"+plan.ID+"/approve", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(confirmHeader, ai.ApproveConfirm)
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ := ts.Client().Do(req)
	raw, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK || !strings.Contains(string(raw), `"status":"stopped"`) {
		t.Fatalf("store install %d %s", res.StatusCode, raw)
	}
	if !strings.Contains(string(raw), "store install must use POST /api/v1/store/apps/{id}/install") {
		t.Fatalf("expected store install API error %s", raw)
	}
}

func TestPhase42PolicyPlanUsesAutomationValidation(t *testing.T) {
	s, mem, token := testServer(t)
	cluster, _ := mem.GetCluster(context.Background())
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)

	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/ai/plans", strings.NewReader(`{"prompt":"If this storage pool exceeds 85%, move eligible low-priority workloads"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ := ts.Client().Do(req)
	raw, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("policy plan %d %s", res.StatusCode, raw)
	}
	var plan map[string]any
	if err := json.Unmarshal(raw, &plan); err != nil {
		t.Fatal(err)
	}
	id, _ := plan["id"].(string)

	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/ai/plans/"+id+"/approve", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(confirmHeader, ai.ApproveConfirm)
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(req)
	raw, _ = io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK || !strings.Contains(string(raw), `"status":"succeeded"`) {
		t.Fatalf("approve policy %d %s", res.StatusCode, raw)
	}
	pols, _ := mem.ListPolicies(context.Background(), cluster.ID)
	if len(pols) != 1 || pols[0].Kind != "storage_pressure" || pols[0].Action != "enqueue_migrate_low_priority" {
		t.Fatalf("policy %+v", pols)
	}

	bad := appdb.AIPlan{
		ID: uuid.NewString(), ClusterID: cluster.ID, Prompt: "bad policy",
		Status: appdb.PlanPreview, ActorType: ai.ActorTypeAI,
	}
	badSteps := []appdb.AIPlanStep{{
		ID: uuid.NewString(), ClusterID: cluster.ID, PlanID: bad.ID, Ordinal: 1,
		Action: ai.ActionCreatePolicy, Permission: "policy.apply",
		Method: "POST", Path: "/api/v1/policies", Title: "policy",
		BodyJSON: `{"name":"exec","kind":"storage_pressure","action":"host.exec","threshold_percent":85}`,
		Status:   appdb.PlanPreview,
	}}
	if err := mem.CreateAIPlan(context.Background(), bad, badSteps); err != nil {
		t.Fatal(err)
	}
	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/ai/plans/"+bad.ID+"/approve", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(confirmHeader, ai.ApproveConfirm)
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(req)
	raw, _ = io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK || !strings.Contains(string(raw), `"status":"stopped"`) {
		t.Fatalf("banned policy action %d %s", res.StatusCode, raw)
	}
	pols, _ = mem.ListPolicies(context.Background(), cluster.ID)
	if len(pols) != 1 {
		t.Fatalf("banned action must not insert a second policy %d", len(pols))
	}
}
