package httpapi

import (
	"net/http"
	"testing"

	"github.com/no-dal/ndl-ce/internal/rbac"
)

func TestNodeNicknamesAreSharedAndGuarded(t *testing.T) {
	a := newAutoTest(t)
	viewer := loginRole(t, a.ts, a.mem, "watcher", rbac.User)

	if code, out := a.call("PUT", "/api/v1/node-nicknames", `{"node_id":"n1","nickname":"  Home   lab "}`, nil); code != http.StatusOK {
		t.Fatalf("set nickname %d %v", code, out["_raw"])
	}
	code, got := a.as(viewer, "GET", "/api/v1/node-nicknames", "")
	names, _ := got["nicknames"].(map[string]any)
	if code != http.StatusOK || names["n1"] != "Home lab" || got["can_edit"] != false {
		t.Fatalf("a user should read the shared nickname but not edit: %d %v", code, got["_raw"])
	}
	if code, _ := a.as(viewer, "PUT", "/api/v1/node-nicknames", `{"node_id":"n1","nickname":"x"}`); code != http.StatusForbidden {
		t.Fatalf("a user renamed a node: %d", code)
	}
	if code, _ := a.call("PUT", "/api/v1/node-nicknames", `{"node_id":"n1","nickname":"bad\u0007name"}`, nil); code != http.StatusBadRequest {
		t.Fatalf("control characters accepted: %d", code)
	}
	// Appearance saves keep the nicknames.
	if code, _ := a.call("PUT", "/api/v1/settings/appearance", `{"nav_layout":"classic"}`, nil); code != http.StatusOK {
		t.Fatalf("appearance save %d", code)
	}
	if code, _ := a.call("PUT", "/api/v1/node-nicknames", `{"node_id":"n2","nickname":"VPS"}`, nil); code != http.StatusOK {
		t.Fatalf("second nickname %d", code)
	}
	_, got = a.call("GET", "/api/v1/node-nicknames", "", nil)
	names, _ = got["nicknames"].(map[string]any)
	if names["n1"] != "Home lab" || names["n2"] != "VPS" {
		t.Fatalf("nicknames lost: %v", got["_raw"])
	}
	// An empty nickname goes back to the node's own name.
	a.call("PUT", "/api/v1/node-nicknames", `{"node_id":"n1","nickname":""}`, nil)
	_, got = a.call("GET", "/api/v1/node-nicknames", "", nil)
	if names, _ = got["nicknames"].(map[string]any); names["n1"] != nil {
		t.Fatalf("nickname not cleared: %v", got["_raw"])
	}
}
