package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/no-dal/ndl-ce/internal/automation/engine"
	"github.com/no-dal/ndl-ce/internal/rbac"
)

// Round 3: root SuperAdmin, automation creators, the guard, approvals and
// notifications.

func (a *autoTest) as(cookie, method, path, body string) (int, map[string]any) {
	a.t.Helper()
	res := authedJSON(a.t, a.ts, method, path, cookie, body, nil)
	raw, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	out := map[string]any{}
	_ = json.Unmarshal(raw, &out)
	out["_raw"] = string(raw)
	return res.StatusCode, out
}

// passDryRun marks the flow's draft as tested, as a passing test run does.
func (a *autoTest) passDryRun(id string) {
	a.t.Helper()
	f, _ := a.mem.GetAutomationFlow(context.Background(), a.cluster, id)
	f.DryRunHash = engine.DefinitionHash(f.Draft)
	if err := a.mem.UpdateAutomationFlow(context.Background(), *f); err != nil {
		a.t.Fatal(err)
	}
}

func (a *autoTest) userID(name string) string {
	a.t.Helper()
	u, _ := a.mem.GetUserByName(context.Background(), a.cluster, name)
	if u == nil {
		a.t.Fatalf("no user %s", name)
	}
	return u.ID
}

// prunePlusNotify changes what pruneFlow does (names and positions are not
// part of the approved hash).
const prunePlusNotify = `{"schema_version":1,"trigger":{"id":"trigger","type":"trigger.manual"},"nodes":[{"id":"prune","name":"Prune","type":"ndl.docker.prune","config":{}},{"id":"tell","type":"action.notify","config":{"title":"pruned","message":"done"}}],"edges":[{"from":"trigger","to":"prune"},{"from":"prune","to":"tell"}]}`

const pruneFlow = `{"schema_version":1,"trigger":{"id":"trigger","type":"trigger.manual"},"nodes":[{"id":"prune","name":"Prune","type":"ndl.docker.prune","config":{}}],"edges":[{"from":"trigger","to":"prune"}]}`

func TestRootStoredAtSetupAndProtected(t *testing.T) {
	a := newAutoTest(t)
	st, _ := a.mem.GetIAMSettings(context.Background(), a.cluster)
	if st.RootUserID != a.userID("admin") {
		t.Fatalf("root %q", st.RootUserID)
	}
	other := loginRole(t, a.ts, a.mem, "sa2", rbac.SuperAdmin)
	// Another SuperAdmin cannot change the root account.
	if code, _ := a.as(other, "PATCH", "/api/v1/users/"+a.userID("admin"), `{"display_name":"x"}`); code != http.StatusForbidden {
		t.Fatalf("other SuperAdmin edited root: %d", code)
	}
	// set-root works only as root on the host.
	if code, _ := a.as(a.cookie, "POST", "/api/v1/console/set-root", `{"username":"sa2"}`); code != http.StatusForbidden {
		t.Fatalf("set-root over the web %d", code)
	}
	code, body := a.call("GET", "/api/v1/iam/settings", "", nil)
	if code != http.StatusOK || body["root"].(map[string]any)["is_me"] != true {
		t.Fatalf("settings %d %v", code, body["_raw"])
	}
}

func TestRolesAboveSuperAdminOnlyRoot(t *testing.T) {
	a := newAutoTest(t)
	other := loginRole(t, a.ts, a.mem, "sa2", rbac.SuperAdmin)
	if code, body := a.call("PUT", "/api/v1/iam/roles/founder", `{"title":"Founder","permissions":[]}`, nil); code != http.StatusNoContent {
		t.Fatalf("create %d %v", code, body["_raw"])
	}
	// Another SuperAdmin cannot move it above SuperAdmin.
	if code, _ := a.as(other, "PUT", "/api/v1/iam/roles/order", `{"names":["founder","superadmin","admin","automation","user"]}`); code != http.StatusNoContent {
		t.Fatalf("order as sa2 %d", code)
	}
	defs, _ := a.mem.ListRoleDefs(context.Background(), a.cluster)
	if superAdminRank(defs) != 0 {
		t.Fatalf("sa2 moved a role above SuperAdmin: %+v", defs)
	}
	// Root can.
	if code, body := a.call("PUT", "/api/v1/iam/roles/order", `{"names":["founder","superadmin","admin","automation","user"]}`, nil); code != http.StatusNoContent {
		t.Fatalf("order as root %d %v", code, body["_raw"])
	}
	forgetRoleDefs(a.cluster)
	defs, _ = a.mem.ListRoleDefs(context.Background(), a.cluster)
	if got := rolesAboveSuperAdmin(defs); len(got) != 1 || got[0] != "founder" {
		t.Fatalf("above top %v", got)
	}
	// Now only root manages that role.
	if code, _ := a.as(other, "PUT", "/api/v1/iam/roles/founder", `{"title":"Founder","permissions":["events.read"]}`); code != http.StatusForbidden {
		t.Fatalf("sa2 edited a role above SuperAdmin: %d", code)
	}
	if code, body := a.call("PUT", "/api/v1/iam/roles/founder", `{"title":"Founder","permissions":["events.read"]}`, nil); code != http.StatusNoContent {
		t.Fatalf("root edit %d %v", code, body["_raw"])
	}
}

func TestRoleIDsFollowNamesAndLegacyAlias(t *testing.T) {
	a := newAutoTest(t)
	if code, _ := a.call("PUT", "/api/v1/iam/roles/support", `{"title":"Help desk","permissions":[]}`, nil); code != http.StatusBadRequest {
		t.Fatalf("mismatched id accepted: %d", code)
	}
	if code, body := a.call("PUT", "/api/v1/iam/roles/help-desk", `{"title":"Help desk","permissions":[]}`, nil); code != http.StatusNoContent {
		t.Fatalf("matching id %d %v", code, body["_raw"])
	}
	if code, _ := a.call("PUT", "/api/v1/iam/roles/automation", `{"title":"Automation","permissions":[]}`, nil); code != http.StatusForbidden {
		t.Fatalf("automation role edited: %d", code)
	}
	if code, _ := a.call("DELETE", "/api/v1/iam/roles/automation", "", nil); code != http.StatusForbidden {
		t.Fatalf("automation role deleted: %d", code)
	}
	if roleIDFor("  Help   Desk! ") != "help-desk" {
		t.Fatal(roleIDFor("  Help   Desk! "))
	}
}

func TestAutomationCreatorsSetting(t *testing.T) {
	a := newAutoTest(t)
	ops := loginRole(t, a.ts, a.mem, "ops", rbac.Admin)
	// Admins may change it until a SuperAdmin saves it.
	if code, body := a.as(ops, "PUT", "/api/v1/iam/settings/automation-creators", `{"min_role":"admin"}`); code != http.StatusOK {
		t.Fatalf("admin save %d %v", code, body["_raw"])
	}
	if code, _ := a.call("PUT", "/api/v1/iam/settings/automation-creators", `{"min_role":"superadmin"}`, nil); code != http.StatusOK {
		t.Fatalf("superadmin save %d", code)
	}
	if code, _ := a.as(ops, "PUT", "/api/v1/iam/settings/automation-creators", `{"min_role":"admin"}`); code != http.StatusForbidden {
		t.Fatalf("locked setting changed by admin: %d", code)
	}
	if code, body := a.as(ops, "POST", "/api/v1/automations/flows", `{"name":"x"}`); code != http.StatusForbidden || !strings.Contains(body["_raw"].(string), "Automation creators") {
		t.Fatalf("admin created a flow below min_role: %d %v", code, body["_raw"])
	}
	// The SuperAdmin lets admins change it again.
	if code, _ := a.call("PUT", "/api/v1/iam/settings/automation-creators", `{"min_role":"admin","admins_may_edit":true}`, nil); code != http.StatusOK {
		t.Fatalf("superadmin save %d", code)
	}
	if code, _ := a.as(ops, "PUT", "/api/v1/iam/settings/automation-creators", `{"min_role":"admin"}`); code != http.StatusOK {
		t.Fatalf("admins_may_edit ignored: %d", code)
	}
	if code, _ := a.as(ops, "POST", "/api/v1/automations/flows", `{"name":"x"}`); code != http.StatusCreated {
		t.Fatalf("admin cannot create: %d", code)
	}
}

func TestProtectedStepsNeedApproval(t *testing.T) {
	a := newAutoTest(t)
	ops := loginRole(t, a.ts, a.mem, "ops", rbac.Admin)
	code, flow := a.as(ops, "POST", "/api/v1/automations/flows", `{"name":"prune","definition":`+pruneFlow+`}`)
	if code != http.StatusCreated {
		t.Fatalf("create %d %v", code, flow["_raw"])
	}
	id := flow["id"].(string)
	_, got := a.as(ops, "GET", "/api/v1/automations/flows/"+id, "")
	if got["publish_needs_approval"] != true || len(got["protected_steps"].([]any)) != 1 {
		t.Fatalf("protected steps %v", got["_raw"])
	}
	a.passDryRun(id)
	code, body := a.as(ops, "POST", "/api/v1/automations/flows/"+id+"/publish", "{}")
	if code != http.StatusAccepted || body["waiting_for_approval"] != true || body["published_version"].(float64) != 0 {
		t.Fatalf("publish %d %v", code, body["_raw"])
	}
	// The waiting version never runs.
	if code, _ := a.as(ops, "POST", "/api/v1/automations/flows/"+id+"/execute", "{}"); code == http.StatusAccepted {
		t.Fatal("an unapproved version ran")
	}
	// The SuperAdmin was told; the requester cannot see approvals.
	if n, _ := a.mem.CountUnreadNotifications(context.Background(), a.cluster, a.userID("admin")); n != 1 {
		t.Fatalf("decider notifications %d", n)
	}
	if code, _ := a.as(ops, "GET", "/api/v1/approvals", ""); code != http.StatusForbidden {
		t.Fatalf("admin listed approvals: %d", code)
	}
	code, list := a.call("GET", "/api/v1/approvals?status=pending", "", nil)
	if code != http.StatusOK || list["pending"].(float64) != 1 {
		t.Fatalf("list %d %v", code, list["_raw"])
	}
	code, detail := a.call("GET", "/api/v1/approvals/"+id+"/1", "", nil)
	if code != http.StatusOK || detail["can_decide"] != true || detail["definition"] == nil {
		t.Fatalf("detail %d %v", code, detail["_raw"])
	}
	if code, body := a.call("POST", "/api/v1/approvals/"+id+"/1/approve", `{"note":"ok"}`, nil); code != http.StatusOK || body["status"] != "approved" {
		t.Fatalf("approve %d %v", code, body["_raw"])
	}
	f, _ := a.mem.GetAutomationFlow(context.Background(), a.cluster, id)
	if f.PublishedVersion != 1 || f.PendingVersion != 0 || f.DraftTainted {
		t.Fatalf("after approve %+v", f)
	}
	if n, _ := a.mem.CountUnreadNotifications(context.Background(), a.cluster, a.userID("ops")); n != 1 {
		t.Fatalf("requester notifications %d", n)
	}

	// A later edit needs approval again; Deny rolls the draft back and v1
	// keeps running.
	edited := prunePlusNotify
	if code, body := a.as(ops, "PATCH", "/api/v1/automations/flows/"+id, `{"definition":`+edited+`}`); code != http.StatusOK {
		t.Fatalf("patch %d %v", code, body["_raw"])
	}
	a.passDryRun(id)
	if code, body := a.as(ops, "POST", "/api/v1/automations/flows/"+id+"/publish", "{}"); code != http.StatusAccepted {
		t.Fatalf("publish v2 %d %v", code, body["_raw"])
	}
	if code, body := a.call("POST", "/api/v1/approvals/"+id+"/2/deny", `{"note":"no"}`, nil); code != http.StatusOK || body["status"] != "denied" {
		t.Fatalf("deny %d %v", code, body["_raw"])
	}
	f, _ = a.mem.GetAutomationFlow(context.Background(), a.cluster, id)
	v1, _ := a.mem.GetAutomationFlowVersion(context.Background(), a.cluster, id, 1)
	if f.PublishedVersion != 1 || f.PendingVersion != 0 || engine.DefinitionHash(f.Draft) != engine.DefinitionHash(v1.Definition) {
		t.Fatalf("after deny %+v", f)
	}
	if code, _ := a.call("POST", "/api/v1/approvals/"+id+"/2/approve", "{}", nil); code != http.StatusConflict {
		t.Fatalf("decided twice: %d", code)
	}
	audits, _ := a.mem.ListAuditEvents(context.Background(), a.cluster, 200)
	seen := map[string]bool{}
	for _, e := range audits {
		seen[e.Action] = true
	}
	for _, want := range []string{"automation.flow.approval.request", "automation.flow.approval.approve", "automation.flow.approval.deny"} {
		if !seen[want] {
			t.Fatalf("audit %s missing", want)
		}
	}
}

func TestSuperAdminFlowsAreExempt(t *testing.T) {
	a := newAutoTest(t)
	_, flow := a.call("POST", "/api/v1/automations/flows", `{"name":"prune","definition":`+pruneFlow+`}`, nil)
	id := flow["id"].(string)
	a.passDryRun(id)
	if code, body := a.call("POST", "/api/v1/automations/flows/"+id+"/publish", "{}", nil); code != http.StatusOK || body["published_version"].(float64) != 1 {
		t.Fatalf("superadmin publish %d %v", code, body["_raw"])
	}
	// An admin's edit takes the exemption away.
	ops := loginRole(t, a.ts, a.mem, "ops", rbac.Admin)
	edited := prunePlusNotify
	if code, _ := a.as(ops, "PATCH", "/api/v1/automations/flows/"+id, `{"definition":`+edited+`}`); code != http.StatusOK {
		t.Fatalf("patch %d", code)
	}
	a.passDryRun(id)
	if code, body := a.call("POST", "/api/v1/automations/flows/"+id+"/publish", "{}", nil); code != http.StatusAccepted {
		t.Fatalf("tainted publish %d %v", code, body["_raw"])
	}
}

func TestOwnAPIStepsRefusedBelowSuperAdmin(t *testing.T) {
	a := newAutoTest(t)
	ops := loginRole(t, a.ts, a.mem, "ops", rbac.Admin)
	def := `{"schema_version":1,"trigger":{"id":"trigger","type":"trigger.manual"},"nodes":[{"id":"call","name":"Call","type":"action.http","config":{"method":"DELETE","url":"https://127.0.0.1/api/v1/users/x"}}],"edges":[{"from":"trigger","to":"call"}]}`
	if code, body := a.as(ops, "POST", "/api/v1/automations/flows", `{"name":"self","definition":`+def+`}`); code != http.StatusForbidden || body["own_api"] == nil {
		t.Fatalf("own API call saved by admin: %d %v", code, body["_raw"])
	}
	code, body := a.call("POST", "/api/v1/automations/flows", `{"name":"self","definition":`+def+`}`, nil)
	if code != http.StatusCreated {
		t.Fatalf("superadmin save %d %v", code, body["_raw"])
	}
	// A templated URL is checked when it runs.
	in := &engine.ExecInput{ClusterID: a.cluster, FlowID: body["id"].(string)}
	if err := a.s.selfTargetAtRun(context.Background(), in, "http://localhost:8080/api/v1/users"); err == nil {
		t.Fatal("run-time check let an unapproved flow call ndl-ce")
	}
	if err := a.s.selfTargetAtRun(context.Background(), in, "https://example.invalid/hook"); err != nil {
		t.Fatalf("external URL refused: %v", err)
	}
}

func TestApprovalsAccessAndNotifications(t *testing.T) {
	a := newAutoTest(t)
	ops := loginRole(t, a.ts, a.mem, "ops", rbac.Admin)
	if code, _ := a.as(ops, "PUT", "/api/v1/iam/settings/approvals-access", `{"access":{"admin":["approvals.view"]}}`); code != http.StatusForbidden {
		t.Fatalf("admin changed approvals access: %d", code)
	}
	if code, body := a.call("PUT", "/api/v1/iam/settings/approvals-access", `{"access":{"admin":["approvals.decide"]}}`, nil); code != http.StatusOK {
		t.Fatalf("access %d %v", code, body["_raw"])
	}
	st, _ := a.mem.GetIAMSettings(context.Background(), a.cluster)
	if got := st.ApprovalsAccess["admin"]; len(got) != 2 {
		t.Fatalf("decide should include view: %v", got)
	}
	if code, _ := a.as(ops, "GET", "/api/v1/approvals", ""); code != http.StatusOK {
		t.Fatalf("admin with approvals.view: %d", code)
	}
	code, me := a.as(ops, "GET", "/api/v1/me", "")
	if code != http.StatusOK || !strings.Contains(me["_raw"].(string), "approvals.decide") {
		t.Fatalf("me grants %d %v", code, me["_raw"])
	}
	// The admin cannot decide its own request.
	_, flow := a.as(ops, "POST", "/api/v1/automations/flows", `{"name":"prune","definition":`+pruneFlow+`}`)
	id := flow["id"].(string)
	a.passDryRun(id)
	_, _ = a.as(ops, "POST", "/api/v1/automations/flows/"+id+"/publish", "{}")
	if code, _ := a.as(ops, "POST", "/api/v1/approvals/"+id+"/1/approve", "{}"); code != http.StatusForbidden {
		t.Fatalf("self approval: %d", code)
	}

	// Notifications: list, count, mark read.
	code, list := a.call("GET", "/api/v1/notifications", "", nil)
	items, _ := list["items"].([]any)
	if code != http.StatusOK || len(items) != 1 || list["unread"].(float64) != 1 {
		t.Fatalf("notifications %d %v", code, list["_raw"])
	}
	nid := items[0].(map[string]any)["id"].(string)
	if code, body := a.call("POST", "/api/v1/notifications/read", `{"ids":["`+nid+`"]}`, nil); code != http.StatusOK || body["unread"].(float64) != 0 {
		t.Fatalf("mark read %d %v", code, body["_raw"])
	}
	if code, body := a.call("GET", "/api/v1/notifications/unread-count", "", nil); code != http.StatusOK || body["unread"].(float64) != 0 {
		t.Fatalf("unread %d %v", code, body["_raw"])
	}
	// Notifications are per person.
	_, other := a.as(ops, "GET", "/api/v1/notifications", "")
	for _, it := range other["items"].([]any) {
		if it.(map[string]any)["id"] == nid {
			t.Fatal("saw another person's notification")
		}
	}
}

func TestLegacyRoleIDsAccepted(t *testing.T) {
	a := newAutoTest(t)
	code, body := a.call("POST", "/api/v1/users", `{"username":"old","password":"password1-long","role":"operator"}`, nil)
	if code != http.StatusCreated && code != http.StatusOK {
		t.Fatalf("create with old id %d %v", code, body["_raw"])
	}
	roles, _ := a.mem.UserRoles(context.Background(), a.userID("old"))
	if len(roles) != 1 || roles[0] != rbac.Admin {
		t.Fatalf("roles %v", roles)
	}
}
