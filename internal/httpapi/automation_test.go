package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/no-dal/ndl-ce/internal/appdb"
)

func enableAutomation(t *testing.T, mem *appdb.Memory) {
	t.Helper()
	setAutomationFeature(t, mem, true)
}

func setAutomationFeature(t *testing.T, mem *appdb.Memory, on bool) {
	t.Helper()
	cluster, _ := mem.GetCluster(context.Background())
	if err := mem.UpsertFeature(context.Background(), appdb.Feature{ClusterID: cluster.ID, ID: automationFeatureID, Enabled: on}); err != nil {
		t.Fatal(err)
	}
}

type autoTest struct {
	t       *testing.T
	s       *Server
	mem     *appdb.Memory
	ts      *httptest.Server
	cookie  string
	cluster string
}

func newAutoTest(t *testing.T) *autoTest {
	s, mem, token := testServer(t)
	s.CFKeyPath = filepath.Join(t.TempDir(), "keys", "secrets.key")
	enableAutomation(t, mem)
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close)
	cookie := claimAdmin(t, ts, token)
	cluster, _ := mem.GetCluster(context.Background())
	s.automation().bus.Sync = true
	return &autoTest{t: t, s: s, mem: mem, ts: ts, cookie: cookie, cluster: cluster.ID}
}

func (a *autoTest) call(method, path, body string, headers map[string]string) (int, map[string]any) {
	a.t.Helper()
	res := authedJSON(a.t, a.ts, method, path, a.cookie, body, headers)
	raw, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	out := map[string]any{}
	_ = json.Unmarshal(raw, &out)
	out["_raw"] = string(raw)
	return res.StatusCode, out
}

func (a *autoTest) step(runID string) *appdb.AutomationRun {
	a.t.Helper()
	if err := a.s.automation().eng.Step(context.Background(), a.cluster, runID); err != nil {
		a.t.Fatal(err)
	}
	run, _ := a.mem.GetAutomationRun(context.Background(), a.cluster, runID)
	return run
}

func TestAutomationFeatureGate(t *testing.T) {
	a := newAutoTest(t)
	code, flow := a.call("POST", "/api/v1/automations/flows", `{"name":"hooked","definition":{"schema_version":1,"trigger":{"id":"trigger","type":"trigger.webhook"},"nodes":[],"edges":[]}}`, nil)
	if code != http.StatusCreated {
		t.Fatalf("create %d %v", code, flow["_raw"])
	}
	id := flow["id"].(string)
	_, hook := a.call("POST", "/api/v1/automations/flows/"+id+"/webhook", "", nil)
	setAutomationFeature(t, a.mem, false)
	for _, path := range []string{"/api/v1/automations/flows", "/api/v1/automations/runs", "/api/v1/automations/nodes", "/api/v1/policies", "/api/v1/policy-runs"} {
		if code, body := a.call("GET", path, "", nil); code != http.StatusConflict || !strings.Contains(body["_raw"].(string), "automation is disabled") {
			t.Fatalf("%s: %d %v", path, code, body["_raw"])
		}
	}
	if code, _ := a.call("POST", "/api/v1/automations/flows/"+id+"/execute", "{}", nil); code != http.StatusConflict {
		t.Fatalf("execute %d", code)
	}
	if code, _ := a.call("DELETE", "/api/v1/policies/nope", "", nil); code != http.StatusConflict {
		t.Fatalf("policy delete %d", code)
	}
	code, body := a.call("POST", hook["path"].(string), `{}`, map[string]string{"X-Nodal-Webhook-Secret": hook["secret"].(string)})
	if code != http.StatusConflict {
		t.Fatalf("inbound webhook while off %d %v", code, body["_raw"])
	}
}

func TestAutomationWebhookAuth(t *testing.T) {
	a := newAutoTest(t)
	def := `{"schema_version":1,"trigger":{"id":"trigger","type":"trigger.webhook"},"nodes":[{"id":"v","type":"logic.set_variable","config":{"name":"who","value":"{{ trigger.user }}"}}],"edges":[{"from":"trigger","to":"v"}]}`
	code, flow := a.call("POST", "/api/v1/automations/flows", `{"name":"hook","definition":`+def+`}`, nil)
	if code != http.StatusCreated {
		t.Fatalf("%d %v", code, flow["_raw"])
	}
	id := flow["id"].(string)
	if code, body := a.call("POST", "/api/v1/automations/flows/"+id+"/publish", "{}", nil); code != http.StatusOK {
		t.Fatalf("publish %d %v", code, body["_raw"])
	}
	if code, body := a.call("POST", "/api/v1/automations/flows/"+id+"/enable", "{}", nil); code != http.StatusOK {
		t.Fatalf("enable %d %v", code, body["_raw"])
	}
	code, hook := a.call("POST", "/api/v1/automations/flows/"+id+"/webhook", "", nil)
	if code != http.StatusOK || hook["secret"] == nil {
		t.Fatalf("webhook %d %v", code, hook["_raw"])
	}
	path, secret := hook["path"].(string), hook["secret"].(string)
	if code, _ := a.call("POST", path, `{"user":"x"}`, map[string]string{"X-Nodal-Webhook-Secret": "wrong"}); code != http.StatusUnauthorized {
		t.Fatalf("wrong secret %d", code)
	}
	if code, _ := a.call("POST", path, `{"user":"x"}`, nil); code != http.StatusUnauthorized {
		t.Fatalf("no secret %d", code)
	}
	if code, _ := a.call("POST", "/api/v1/hooks/flows/not-a-token", `{}`, map[string]string{"X-Nodal-Webhook-Secret": secret}); code != http.StatusNotFound {
		t.Fatalf("bad token %d", code)
	}
	headers := map[string]string{"X-Nodal-Webhook-Secret": secret, "Idempotency-Key": "abc"}
	code, first := a.call("POST", path, `{"user":"alice","password":"hunter2"}`, headers)
	if code != http.StatusAccepted || first["run_id"] == nil {
		t.Fatalf("accepted %d %v", code, first["_raw"])
	}
	code, again := a.call("POST", path, `{"user":"alice"}`, headers)
	if code != http.StatusAccepted || again["run_id"] != first["run_id"] || again["existing"] != true {
		t.Fatalf("idempotency %d %v", code, again["_raw"])
	}
	big := `{"x":"` + strings.Repeat("a", 1<<20) + `"}`
	if code, _ := a.call("POST", path, big, map[string]string{"X-Nodal-Webhook-Secret": secret}); code != http.StatusRequestEntityTooLarge {
		t.Fatalf("body cap %d", code)
	}
	run := a.step(first["run_id"].(string))
	if run.Status != appdb.AutoRunSucceeded || strings.Contains(string(run.TriggerPayload), "hunter2") {
		t.Fatalf("%s %s", run.Status, run.TriggerPayload)
	}
	_, got := a.call("GET", "/api/v1/automations/flows/"+id+"/webhook", "", nil)
	if strings.Contains(got["_raw"].(string), "hunter2") || !strings.Contains(got["_raw"].(string), "[redacted]") {
		t.Fatalf("deliveries must be redacted: %v", got["_raw"])
	}
	// Rotating replaces the token.
	_, rotated := a.call("POST", "/api/v1/automations/flows/"+id+"/webhook/rotate", "", nil)
	if code, _ := a.call("POST", path, `{}`, map[string]string{"X-Nodal-Webhook-Secret": secret}); code != http.StatusNotFound || rotated["path"] == path {
		t.Fatalf("old token after rotate %d", code)
	}
}

func TestAutomationDestructivePublishNeedsDryRunAndUsesTheHandler(t *testing.T) {
	a := newAutoTest(t)
	node := seedNode(t, a.mem, a.cluster, debianInv(), false)
	if err := a.mem.CreateWorkload(context.Background(), appdb.Workload{ID: "11111111-1111-4111-8111-111111111111", ClusterID: a.cluster, NodeID: node.ID, Name: "web-1", Kind: "system-container", Status: "running"}); err != nil {
		t.Fatal(err)
	}
	def := `{"schema_version":1,"trigger":{"id":"trigger","type":"trigger.manual"},"nodes":[{"id":"restart","type":"ndl.workload.restart","config":{"workload":"web-1"}}],"edges":[{"from":"trigger","to":"restart"}]}`
	_, flow := a.call("POST", "/api/v1/automations/flows", `{"name":"restart web","definition":`+def+`}`, nil)
	id := flow["id"].(string)
	code, body := a.call("POST", "/api/v1/automations/flows/"+id+"/publish", "{}", nil)
	if code != http.StatusConflict || body["needs_dry_run"] != true {
		t.Fatalf("publish without a test %d %v", code, body["_raw"])
	}
	// Flows run as the system: the test needs no extra grants.
	code, body = a.call("POST", "/api/v1/automations/flows/"+id+"/dry-run", `{}`, nil)
	raw := body["_raw"].(string)
	if code != http.StatusOK || !strings.Contains(raw, "would restart container web-1") || body["flow"].(map[string]any)["dry_run_passed"] != true {
		t.Fatalf("dry run %d %s", code, raw)
	}
	ops, _ := a.mem.ListOperations(context.Background(), a.cluster, 100)
	for _, op := range ops {
		if op.Kind == "workload.restart" {
			t.Fatal("a test run must not start operations")
		}
	}
	if code, body := a.call("POST", "/api/v1/automations/flows/"+id+"/publish", "{}", nil); code != http.StatusOK {
		t.Fatalf("publish after the test %d %v", code, body["_raw"])
	}
	code, body = a.call("POST", "/api/v1/automations/flows/"+id+"/execute", "{}", nil)
	if code != http.StatusAccepted {
		t.Fatalf("execute %d %v", code, body["_raw"])
	}
	runID := body["run"].(map[string]any)["id"].(string)
	run := a.step(runID)
	// The step went through the real workload handler, which has no agent
	// in this test.
	if run.Status != appdb.AutoRunFailed || !strings.Contains(run.Error, "workload agent is unavailable") {
		t.Fatalf("%s %s", run.Status, run.Error)
	}
	audits, _ := a.mem.ListAuditEvents(context.Background(), a.cluster, 200)
	seen := map[string]bool{}
	for _, e := range audits {
		seen[e.Action] = true
	}
	for _, want := range []string{"automation.flow.create", "automation.flow.dry_run", "automation.flow.publish", "automation.flow.execute"} {
		if !seen[want] {
			t.Fatalf("audit %s missing", want)
		}
	}
}

func TestAutomationTemplateRunsFromAnEvent(t *testing.T) {
	a := newAutoTest(t)
	code, flow := a.call("POST", "/api/v1/automations/flows", `{"template":"notify-failed-backup"}`, nil)
	if code != http.StatusCreated || flow["name"] != "Notify on failed backup" {
		t.Fatalf("%d %v", code, flow["_raw"])
	}
	id := flow["id"].(string)
	if code, body := a.call("POST", "/api/v1/automations/flows/"+id+"/publish", "{}", nil); code != http.StatusOK {
		t.Fatalf("publish %d %v", code, body["_raw"])
	}
	a.call("POST", "/api/v1/automations/flows/"+id+"/enable", "{}", nil)
	a.s.automationBackupEvent(context.Background(), a.cluster, "w1", appdb.BackupRun{ID: "r1", Status: appdb.BackupFailed, Error: "disk full"}, nil)
	runs, _ := a.mem.ListAutomationRuns(context.Background(), a.cluster, appdb.AutomationRunFilter{FlowID: id})
	if len(runs) != 1 {
		t.Fatalf("runs %d", len(runs))
	}
	run := a.step(runs[0].ID)
	if run.Status != appdb.AutoRunSucceeded {
		t.Fatalf("%s %s", run.Status, run.Error)
	}
	events, _ := a.mem.ListEvents(context.Background(), a.cluster, 50)
	found := false
	for _, e := range events {
		if e.Type == "automation.notify" && strings.Contains(string(e.Payload), "disk full") {
			found = true
		}
	}
	if !found {
		t.Fatal("notify adds an Activity event")
	}
	code, nodes := a.call("GET", "/api/v1/automations/runs/"+run.ID+"/nodes", "", nil)
	if code != http.StatusOK || !strings.Contains(nodes["_raw"].(string), `"node_id":"tell"`) {
		t.Fatalf("%d %v", code, nodes["_raw"])
	}
}

func TestAutomationPoliciesBecomeFlowsAndConnectionsHideSecrets(t *testing.T) {
	a := newAutoTest(t)
	if code, body := a.call("POST", "/api/v1/policies", `{"name":"pressure","kind":"storage_pressure","threshold_percent":90}`, nil); code != http.StatusCreated {
		t.Fatalf("%d %v", code, body["_raw"])
	}
	flows, _ := a.mem.ListAutomationFlows(context.Background(), a.cluster)
	if len(flows) != 1 || flows[0].Enabled || flows[0].PublishedVersion != 1 || !strings.Contains(string(flows[0].Draft), "ndl.storage.pressure") {
		t.Fatalf("%+v", flows)
	}
	a.s.migrateLegacyPolicies(context.Background(), a.cluster)
	if again, _ := a.mem.ListAutomationFlows(context.Background(), a.cluster); len(again) != 1 {
		t.Fatal("a policy becomes one flow")
	}
	code, conn := a.call("POST", "/api/v1/automations/connections", `{"name":"api","kind":"bearer","config":{"base_url":"https://example.com"},"secret":{"token":"s3cret-value"}}`, nil)
	if code != http.StatusCreated || strings.Contains(conn["_raw"].(string), "s3cret") {
		t.Fatalf("%d %v", code, conn["_raw"])
	}
	_, list := a.call("GET", "/api/v1/automations/connections", "", nil)
	if strings.Contains(list["_raw"].(string), "s3cret") || !strings.Contains(list["_raw"].(string), `"secret_fields":["token"]`) {
		t.Fatalf("%v", list["_raw"])
	}
	stored, _ := a.mem.GetAutomationConnection(context.Background(), a.cluster, conn["id"].(string))
	if stored == nil || strings.Contains(stored.Sealed, "s3cret") {
		t.Fatal("the secret is stored sealed")
	}
	opened, err := a.s.openAutomationConnection(context.Background(), a.cluster, stored.ID)
	if err != nil || opened["token"] != "s3cret-value" {
		t.Fatalf("%v %v", opened, err)
	}
	if code, body := a.call("POST", "/api/v1/automations/connections", `{"name":"bad","config":{"password":"x"}}`, nil); code != http.StatusBadRequest {
		t.Fatalf("secrets in config are refused %d %v", code, body["_raw"])
	}
}
