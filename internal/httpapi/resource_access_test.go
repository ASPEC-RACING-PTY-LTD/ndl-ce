package httpapi

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/no-dal/ndl-ce/internal/appdb"
	"github.com/no-dal/ndl-ce/internal/rbac"
)

// loginNoRole creates a person with no sign-in role through the API and signs in.
func (a *autoTest) loginNoRole(username string) (id, cookie string) {
	a.t.Helper()
	code, out := a.call("POST", "/api/v1/users", `{"username":"`+username+`","password":"Correct-horse-9","role":"none"}`, nil)
	if code != http.StatusCreated {
		a.t.Fatalf("create role-less user %d %v", code, out["_raw"])
	}
	id, _ = out["id"].(string)
	if id == "" {
		id, _ = out["user_id"].(string)
	}
	res, err := a.ts.Client().Post(a.ts.URL+"/api/v1/auth/login", "application/json", strings.NewReader(`{"username":"`+username+`","password":"Correct-horse-9"}`))
	if err != nil {
		a.t.Fatal(err)
	}
	defer res.Body.Close()
	for _, c := range res.Cookies() {
		if c.Name == sessionCookie {
			return id, c.Value
		}
	}
	a.t.Fatalf("no session for %s: %d", username, res.StatusCode)
	return "", ""
}

func listIDs(out map[string]any) []string {
	items, _ := out["items"].([]any)
	var ids []string
	for _, it := range items {
		m, _ := it.(map[string]any)
		id, _ := m["id"].(string)
		ids = append(ids, id)
	}
	return ids
}

func TestRoleLessUserSeesOnlyGrantedWorkload(t *testing.T) {
	a := newAutoTest(t)
	ctx := context.Background()
	mine, other := "3b1d0a9e-0000-4000-8000-0000000000a1", "3b1d0a9e-0000-4000-8000-0000000000a2"
	_ = a.mem.CreateWorkload(ctx, appdb.Workload{ID: mine, ClusterID: a.cluster, Name: "vaultdock", Kind: "system-container"})
	_ = a.mem.CreateWorkload(ctx, appdb.Workload{ID: other, ClusterID: a.cluster, Name: "secret", Kind: "system-container"})
	uid, friend := a.loginNoRole("friend")

	// Without a grant: nothing, but their own profile works.
	if code, _ := a.as(friend, "GET", "/api/v1/workloads", ""); code != http.StatusForbidden {
		t.Fatalf("role-less list without grants: %d", code)
	}
	if code, out := a.as(friend, "GET", "/api/v1/me", ""); code != http.StatusOK {
		t.Fatalf("me %d %v", code, out["_raw"])
	}

	// Ungrantable permissions are refused.
	if code, _ := a.call("PUT", "/api/v1/iam/resource-access", `{"subject_kind":"user","subject_id":"`+uid+`","resource_kind":"workload","resource_id":"`+mine+`","permissions":["compute.delete"]}`, nil); code != http.StatusBadRequest {
		t.Fatalf("compute.delete granted on one workload: %d", code)
	}
	if code, out := a.call("PUT", "/api/v1/iam/resource-access", `{"subject_kind":"user","subject_id":"`+uid+`","resource_kind":"workload","resource_id":"`+mine+`","permissions":["files.read"]}`, nil); code != http.StatusOK {
		t.Fatalf("grant %d %v", code, out["_raw"])
	}

	code, out := a.as(friend, "GET", "/api/v1/workloads", "")
	if ids := listIDs(out); code != http.StatusOK || len(ids) != 1 || ids[0] != mine {
		t.Fatalf("filtered list %d %v", code, out["_raw"])
	}
	if code, _ := a.as(friend, "GET", "/api/v1/workloads/"+mine, ""); code != http.StatusOK {
		t.Fatalf("granted workload: %d", code)
	}
	if code, _ := a.as(friend, "GET", "/api/v1/workloads/"+other, ""); code != http.StatusForbidden {
		t.Fatalf("other workload: %d", code)
	}
	// Seeing it does not allow deleting it, and nothing outside the item opens up.
	if code, _ := a.as(friend, "POST", "/api/v1/workloads/"+mine+"/delete", ""); code != http.StatusForbidden {
		t.Fatalf("delete with read grant: %d", code)
	}
	for _, path := range []string{"/api/v1/nodes", "/api/v1/users", "/api/v1/iam/resource-access", "/api/v1/storage/pools"} {
		if code, _ := a.as(friend, "GET", path, ""); code != http.StatusForbidden {
			t.Fatalf("%s opened up: %d", path, code)
		}
	}
	code, me := a.as(friend, "GET", "/api/v1/me", "")
	access, _ := me["item_access"].(map[string]any)
	if code != http.StatusOK || access["workload"] == nil {
		t.Fatalf("me item_access: %v", me["_raw"])
	}

	// Removing the grant takes it away.
	_, all := a.call("GET", "/api/v1/iam/resource-access", "", nil)
	grants, _ := all["grants"].([]any)
	gid, _ := grants[0].(map[string]any)["id"].(string)
	if code, _ := a.call("DELETE", "/api/v1/iam/resource-access/"+gid, "", nil); code != http.StatusOK {
		t.Fatalf("delete grant %d", code)
	}
	if code, _ := a.as(friend, "GET", "/api/v1/workloads/"+mine, ""); code != http.StatusForbidden {
		t.Fatalf("after removal: %d", code)
	}
}

func TestGroupGrantAndRoleCombination(t *testing.T) {
	a := newAutoTest(t)
	ctx := context.Background()
	wl := "3b1d0a9e-0000-4000-8000-0000000000b1"
	_ = a.mem.CreateWorkload(ctx, appdb.Workload{ID: wl, ClusterID: a.cluster, Name: "shared", Kind: "system-container"})
	uid, friend := a.loginNoRole("mate")
	code, grp := a.call("POST", "/api/v1/groups", `{"name":"friends"}`, nil)
	gid, _ := grp["id"].(string)
	if code >= 300 || gid == "" {
		t.Fatalf("group %d %v", code, grp["_raw"])
	}
	if code, out := a.call("POST", "/api/v1/groups/"+gid+"/members", `{"user_id":"`+uid+`"}`, nil); code >= 300 {
		t.Fatalf("member %d %v", code, out["_raw"])
	}
	if code, out := a.call("PUT", "/api/v1/iam/resource-access", `{"subject_kind":"group","subject_id":"`+gid+`","resource_kind":"workload","resource_id":"`+wl+`","permissions":["compute.read"]}`, nil); code != http.StatusOK {
		t.Fatalf("group grant %d %v", code, out["_raw"])
	}
	if code, _ := a.as(friend, "GET", "/api/v1/workloads/"+wl, ""); code != http.StatusOK {
		t.Fatalf("group member reading granted workload: %d", code)
	}

	// A User role sees every workload; a grant adds power on one.
	viewer := loginRole(t, a.ts, a.mem, "viewer", rbac.User)
	code, out := a.as(viewer, "GET", "/api/v1/workloads", "")
	if code != http.StatusOK || len(listIDs(out)) != 1 {
		t.Fatalf("user role list %d %v", code, out["_raw"])
	}
}

func TestScopedPrincipalKeepsRoleGrantsSeparate(t *testing.T) {
	p := &principal{Grants: []string{rbac.ComputeRead}}
	if got := p.roleGrants(); len(got) != 1 {
		t.Fatalf("role grants %v", got)
	}
	p.base, p.Grants, p.Scoped = []string{rbac.ComputeRead}, []string{rbac.ComputeRead, rbac.TerminalOpen}, true
	if rbac.Authorize(p.roleGrants(), rbac.TerminalOpen) {
		t.Fatal("an item grant leaked into role grants")
	}
	if grantable(accessWorkload, rbac.ComputeDelete) || grantable(accessNode, rbac.NodeRevoke) || grantable(accessGameServer, rbac.GameServerUsers) {
		t.Fatal("a dangerous permission is grantable on one item")
	}
}

func TestRoleLessGameServerAndNodeGrants(t *testing.T) {
	a := newAutoTest(t)
	ctx := context.Background()
	enableGameServers(t, a.mem, a.cluster)
	mine, other := "3b1d0a9e-0000-4000-8000-0000000000c1", "3b1d0a9e-0000-4000-8000-0000000000c2"
	for _, gs := range []appdb.GameServer{{ID: mine, ClusterID: a.cluster, Name: "jvs", Family: "source"}, {ID: other, ClusterID: a.cluster, Name: "private", Family: "source"}} {
		if err := a.mem.CreateGameServer(ctx, gs); err != nil {
			t.Fatal(err)
		}
	}
	uid, friend := a.loginNoRole("gamer")
	if code, out := a.call("PUT", "/api/v1/iam/resource-access", `{"subject_kind":"user","subject_id":"`+uid+`","resource_kind":"gameserver","resource_id":"`+mine+`","permissions":["gameserver.console"]}`, nil); code != http.StatusOK {
		t.Fatalf("grant %d %v", code, out["_raw"])
	}
	code, out := a.as(friend, "GET", "/api/v1/game-servers", "")
	if ids := listIDs(out); code != http.StatusOK || len(ids) != 1 || ids[0] != mine {
		t.Fatalf("game server list %d %v", code, out["_raw"])
	}
	if code, _ := a.as(friend, "GET", "/api/v1/game-servers/"+other, ""); code != http.StatusForbidden {
		t.Fatalf("other game server: %d", code)
	}
	if code, out := a.as(friend, "GET", "/api/v1/game-servers/"+mine, ""); code != http.StatusOK {
		t.Fatalf("granted game server %d %v", code, out["_raw"])
	}

	// A node grant shows that node only.
	local := &appdb.Node{ID: "3b1d0a9e-0000-4000-8000-0000000000d1", ClusterID: a.cluster, Name: "friend-box", Hostname: "friend-box", Role: "worker"}
	if err := a.mem.UpsertNode(ctx, *local); err != nil {
		t.Fatal(err)
	}
	_ = a.mem.UpsertNode(ctx, appdb.Node{ID: "3b1d0a9e-0000-4000-8000-0000000000d2", ClusterID: a.cluster, Name: "other-box", Hostname: "other-box", Role: "worker"})
	if code, out := a.call("PUT", "/api/v1/iam/resource-access", `{"subject_kind":"user","subject_id":"`+uid+`","resource_kind":"node","resource_id":"`+local.ID+`","permissions":["node.read"]}`, nil); code != http.StatusOK {
		t.Fatalf("node grant %d %v", code, out["_raw"])
	}
	code, out = a.as(friend, "GET", "/api/v1/nodes", "")
	if ids := listIDs(out); code != http.StatusOK || len(ids) != 1 || ids[0] != local.ID {
		t.Fatalf("node list %d %v", code, out["_raw"])
	}
	// node.read does not open the host terminal.
	if code, _ := a.as(friend, "POST", "/api/v1/nodes/"+local.ID+"/terminal/sessions", ""); code != http.StatusForbidden {
		t.Fatalf("host terminal with node.read: %d", code)
	}
}
