package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/no-dal/ndl-ce/internal/appdb"
	"github.com/no-dal/ndl-ce/internal/gameserver"
	"github.com/no-dal/ndl-ce/internal/inventory"
)

type gameAPI struct {
	t      *testing.T
	ts     *httptest.Server
	cookie string
}

func (g gameAPI) do(method, path, body string) (int, map[string]any) {
	g.t.Helper()
	req, _ := http.NewRequest(method, g.ts.URL+"/api/v1"+path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: g.cookie})
	res, err := g.ts.Client().Do(req)
	if err != nil {
		g.t.Fatal(err)
	}
	raw, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	out := map[string]any{}
	_ = json.Unmarshal(raw, &out)
	out["_raw"] = string(raw)
	return res.StatusCode, out
}

func strList(v any) []string {
	var out []string
	if arr, ok := v.([]any); ok {
		for _, x := range arr {
			if s, ok := x.(string); ok {
				out = append(out, s)
			}
		}
	}
	return out
}

func anyContains(list []string, sub string) bool {
	for _, s := range list {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

func setupProvisioning(t *testing.T, arch string, memBytes uint64) (gameAPI, *appdb.Memory, string, appdb.Node) {
	t.Helper()
	s, mem, token := testServer(t)
	rt := gameserver.NewRuntime(t.TempDir())
	rt.Run = func(ctx context.Context, name string, args ...string) ([]byte, error) { return []byte("ok"), nil }
	s.Game = rt
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close)
	cookie := claimAdmin(t, ts, token)
	cluster, _ := mem.GetCluster(context.Background())
	enableGameServers(t, mem, cluster.ID)
	node, _ := mem.GetNode(context.Background(), cluster.ID)
	if node == nil {
		node = &appdb.Node{ID: uuid.NewString(), ClusterID: cluster.ID, Name: "local"}
	}
	node.HostPlatform = json.RawMessage(`{"architecture":"` + arch + `"}`)
	if err := mem.UpsertNode(context.Background(), *node); err != nil {
		t.Fatal(err)
	}
	inv := inventory.Inventory{}
	inv.Memory.TotalBytes = memBytes
	inv.CPU.Threads = 8
	body, _ := json.Marshal(inv)
	if err := mem.UpsertInventory(context.Background(), appdb.HardwareInventory{NodeID: node.ID, ClusterID: cluster.ID, Payload: body, ObservedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	return gameAPI{t: t, ts: ts, cookie: cookie}, mem, cluster.ID, *node
}

func TestGamePreflightAllocatesPortsAroundExistingServers(t *testing.T) {
	api, _, _, _ := setupProvisioning(t, "amd64", 64<<30)
	code, first := api.do("POST", "/game-servers", `{"name":"Valheim A","template_id":"ndl-valheim"}`)
	if code != http.StatusAccepted {
		t.Fatalf("first create %d %s", code, first["_raw"])
	}
	code, plan := api.do("POST", "/game-servers/preflight", `{"name":"Valheim B","template_id":"ndl-valheim"}`)
	if code != http.StatusOK || plan["ok"] != true {
		t.Fatalf("preflight %d %s", code, plan["_raw"])
	}
	updates, _ := plan["env_updates"].(map[string]any)
	if updates["SERVER_PORT"] != "2458" {
		t.Fatalf("expected the whole port set to move past 2456/2457, got %s", plan["_raw"])
	}
	if !anyContains(strList(plan["warnings"]), "moved by +2") {
		t.Fatalf("expected a port move warning %s", plan["_raw"])
	}
	code, second := api.do("POST", "/game-servers", `{"name":"Valheim B","template_id":"ndl-valheim"}`)
	if code != http.StatusAccepted {
		t.Fatalf("second create %d %s", code, second["_raw"])
	}
	env, _ := second["env"].(map[string]any)
	if env["SERVER_PORT"] != "2458" || !strings.Contains(second["_raw"].(string), `"host_port":2459`) {
		t.Fatalf("create must apply the allocated ports %s", second["_raw"])
	}
	// Explicit ports that collide are refused, not silently moved.
	code, clash := api.do("POST", "/game-servers/preflight", `{"template_id":"ndl-valheim","ports":[{"name":"game","container_port":2456,"protocol":"udp","primary":true}]}`)
	if code != http.StatusOK || clash["ok"] != false || !anyContains(strList(clash["errors"]), "Valheim A") {
		t.Fatalf("explicit clash %s", clash["_raw"])
	}
}

func TestGamePreflightRejectsIncompatibleArchitecture(t *testing.T) {
	api, _, _, _ := setupProvisioning(t, "aarch64", 64<<30)
	_, plan := api.do("POST", "/game-servers/preflight", `{"template_id":"ndl-valheim"}`)
	if plan["ok"] != false || !anyContains(strList(plan["errors"]), "amd64") {
		t.Fatalf("valheim on arm64 must be refused %s", plan["_raw"])
	}
	code, created := api.do("POST", "/game-servers", `{"name":"x","template_id":"ndl-valheim"}`)
	if code != http.StatusUnprocessableEntity {
		t.Fatalf("create must refuse too: %d %s", code, created["_raw"])
	}
	_, plan = api.do("POST", "/game-servers/preflight", `{"template_id":"ndl-minecraft-paper","env":{"EULA":"true"}}`)
	if plan["ok"] != true {
		t.Fatalf("paper runs on arm64 %s", plan["_raw"])
	}
}

func TestGamePreflightResourcesAndCredentials(t *testing.T) {
	api, _, _, _ := setupProvisioning(t, "amd64", 4<<30)
	_, plan := api.do("POST", "/game-servers/preflight", `{"template_id":"ndl-rust","memory_bytes":8589934592}`)
	if plan["ok"] != false || !anyContains(strList(plan["errors"]), "more than node") {
		t.Fatalf("8 GiB on a 4 GiB node must be refused %s", plan["_raw"])
	}
	_, plan = api.do("POST", "/game-servers/preflight", `{"template_id":"ndl-rust","memory_bytes":1073741824}`)
	if plan["ok"] != true || !anyContains(strList(plan["warnings"]), "below the recommended") {
		t.Fatalf("low RAM is the operator's call but must warn %s", plan["_raw"])
	}
	_, plan = api.do("POST", "/game-servers/preflight", `{"template_id":"ndl-rust","cpus":64}`)
	if plan["ok"] != false || !anyContains(strList(plan["errors"]), "CPUs") {
		t.Fatalf("64 CPUs on an 8 thread node %s", plan["_raw"])
	}
	_, plan = api.do("POST", "/game-servers/preflight", `{"template_id":"ndl-minecraft-paper","memory_bytes":1073741824,"env":{"EULA":"true","SERVER_MEMORY":"2048"}}`)
	if plan["ok"] != false || !anyContains(strList(plan["errors"]), "heap") {
		t.Fatalf("heap above RAM %s", plan["_raw"])
	}
	_, plan = api.do("POST", "/game-servers/preflight", `{"template_id":"ndl-dayz","memory_bytes":2147483648}`)
	if plan["ok"] != false || !anyContains(strList(plan["errors"]), "STEAM_USER") {
		t.Fatalf("dayz needs an owning Steam account %s", plan["_raw"])
	}
	code, created := api.do("POST", "/game-servers", `{"name":"d","template_id":"ndl-dayz","memory_bytes":2147483648}`)
	if code != http.StatusUnprocessableEntity {
		t.Fatalf("dayz create without account %d %s", code, created["_raw"])
	}
	_, plan = api.do("POST", "/game-servers/preflight", `{"template_id":"ndl-fivem","memory_bytes":2147483648}`)
	if plan["ok"] != true || !anyContains(strList(plan["warnings"]), "will not start") {
		t.Fatalf("fivem installs without a key but warns %s", plan["_raw"])
	}
	_, plan = api.do("POST", "/game-servers/preflight", `{"template_id":"ndl-valheim","node_id":"some-other-node"}`)
	if plan["ok"] != false || !anyContains(strList(plan["errors"]), "control node") {
		t.Fatalf("remote placement must be refused honestly %s", plan["_raw"])
	}
	_, plan = api.do("POST", "/game-servers/preflight", `{"template_id":"ndl-minecraft-paper","image_label":"Java 99","env":{"EULA":"true"}}`)
	if plan["ok"] != false || !anyContains(strList(plan["errors"]), "Java 99") {
		t.Fatalf("unknown runtime label %s", plan["_raw"])
	}
}

func TestGameTemplateViewAndFavoritesPrefs(t *testing.T) {
	api, _, _, _ := setupProvisioning(t, "amd64", 64<<30)
	code, view := api.do("GET", "/game-servers/templates?id=ndl-valheim", "")
	if code != http.StatusOK || view["install_method"] != "SteamCMD" || view["update_procedure"] == "" || view["verification"] == "" {
		t.Fatalf("template view %s", view["_raw"])
	}
	code, conan := api.do("GET", "/game-servers/templates?id=ndl-conan", "")
	if code != http.StatusOK || conan["hidden"] != true {
		t.Fatalf("hidden templates must still resolve for existing servers %d %s", code, conan["_raw"])
	}
	code, _ = api.do("PUT", "/game-servers/prefs", `{"favorites":"[\"ndl-valheim\",\"ndl-rust\"]"}`)
	if code != http.StatusOK {
		t.Fatalf("save favorites %d", code)
	}
	_, prefs := api.do("GET", "/game-servers/prefs", "")
	if prefs["favorites"] != `["ndl-valheim","ndl-rust"]` {
		t.Fatalf("favorites %s", prefs["_raw"])
	}
	code, _ = api.do("PUT", "/game-servers/prefs", `{"favorites":"not json"}`)
	if code != http.StatusBadRequest {
		t.Fatalf("invalid favorites accepted %d", code)
	}
	_, cat := api.do("GET", "/game-servers/catalogue?group=no-credentials", "")
	if strings.Contains(cat["_raw"].(string), `"ndl-dayz"`) || !strings.Contains(cat["_raw"].(string), `"ndl-valheim"`) {
		t.Fatal("no-credentials catalogue filter")
	}
}
