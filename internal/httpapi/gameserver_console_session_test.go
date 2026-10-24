package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/no-dal/ndl-ce/internal/appdb"
	"github.com/no-dal/ndl-ce/internal/gameserver"
	"github.com/no-dal/ndl-ce/internal/rbac"
)

// fakeGameRuntime installs and runs servers without Docker.
func fakeGameRuntime(t *testing.T) *gameserver.Runtime {
	rt := gameserver.NewRuntime(t.TempDir())
	rt.Run = func(ctx context.Context, name string, args ...string) ([]byte, error) {
		joined := strings.Join(args, " ")
		if strings.Contains(joined, "inspect") {
			return []byte("true"), nil
		}
		return []byte("ok"), nil
	}
	return rt
}

func createPaperServer(t *testing.T, ts *httptest.Server, mem *appdb.Memory, cookie, clusterID string) string {
	t.Helper()
	code, created := apiJSON(t, ts, cookie, "POST", "/api/v1/game-servers", `{"name":"Paper Lab","template_id":"ndl-minecraft-paper","env":{"EULA":"true"}}`)
	if code != http.StatusAccepted {
		t.Fatalf("create %d %v", code, created)
	}
	id, _ := created["id"].(string)
	deadline := time.Now().Add(3 * time.Second)
	for {
		row, _ := mem.GetGameServer(context.Background(), clusterID, id)
		if row != nil && row.Status != gameserver.StatusPending && row.Status != gameserver.StatusInstalling {
			return id
		}
		if time.Now().After(deadline) {
			t.Fatal("install did not finish")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestGameConsoleSessionStreamsThroughTerminalSocket(t *testing.T) {
	s, mem, token := testServer(t)
	s.Game = fakeGameRuntime(t)
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)
	cluster, _ := mem.GetCluster(context.Background())
	enableGameServers(t, mem, cluster.ID)
	id := createPaperServer(t, ts, mem, cookie, cluster.ID)

	code, got := apiJSON(t, ts, cookie, "POST", "/api/v1/game-servers/"+id+"/console/sessions", `{}`)
	if code != http.StatusCreated || got["target_kind"] != "gameserver" || got["target_id"] != id || got["ticket"] == "" {
		t.Fatalf("console session %d %v", code, got)
	}
	if got["ws_path"] != "/api/v1/io/sessions/"+got["id"].(string)+"/ws" {
		t.Fatalf("console must use the terminal socket: %v", got)
	}
	// Game console sessions stay out of the Terminal workspace list.
	_, list := apiJSON(t, ts, cookie, "GET", "/api/v1/io/sessions", "")
	if items, _ := list["items"].([]any); len(items) != 0 {
		t.Fatalf("game consoles must not show as terminals: %v", items)
	}
	if code, _ := apiJSON(t, ts, cookie, "POST", "/api/v1/game-servers/"+uuidLike()+"/console/sessions", `{}`); code != http.StatusNotFound {
		t.Fatalf("an unknown server must be refused: %d", code)
	}
}

func uuidLike() string { return "00000000-0000-4000-8000-000000000000" }

func TestIOSessionGrantForGameConsole(t *testing.T) {
	if g := ioSessionGrant(appdb.IOSession{TargetKind: ioTargetGameServer, Kind: appdb.IOKindTerminal}); g != rbac.GameServerConsole {
		t.Fatalf("a game console needs the game console grant, got %s", g)
	}
	if g := ioSessionGrant(appdb.IOSession{TargetKind: appdb.IOTargetHost, Kind: appdb.IOKindTerminal}); g != rbac.TerminalOpen {
		t.Fatalf("a host terminal needs terminal.open, got %s", g)
	}
}

func TestModpacksAskForCurseForgeKey(t *testing.T) {
	s, mem, token := testServer(t)
	s.Game = fakeGameRuntime(t)
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)
	cluster, _ := mem.GetCluster(context.Background())
	enableGameServers(t, mem, cluster.ID)

	_, st := apiJSON(t, ts, cookie, "GET", "/api/v1/game-servers/settings", "")
	if st["curseforge_key_set"] != false {
		t.Fatalf("no key yet: %v", st)
	}
	code, got := apiJSON(t, ts, cookie, "GET", "/api/v1/game-servers/modpacks?provider=curseforge&q=atm", "")
	if code != http.StatusOK || got["needs_key"] != true {
		t.Fatalf("CurseForge without a key must say so: %d %v", code, got)
	}
	code, got = apiJSON(t, ts, cookie, "PUT", "/api/v1/game-servers/settings", `{"clear_curseforge":true}`)
	if code != http.StatusOK || got["curseforge_key_set"] != false {
		t.Fatalf("clear %d %v", code, got)
	}
}

func TestMinecraftModsOfferBothCatalogues(t *testing.T) {
	spec := gameserver.ContentSpec{Provider: "modrinth", Kind: "mod", GameID: "minecraft"}
	if got := contentSources(spec); len(got) != 2 || got[1] != "curseforge" {
		t.Fatalf("Minecraft mods: %v", got)
	}
	if got := contentSources(gameserver.ContentSpec{Provider: "workshop", Kind: "mod", GameID: "garrysmod"}); len(got) != 1 {
		t.Fatalf("other games keep their catalogue: %v", got)
	}
	s := &Server{Store: appdb.NewMemory()}
	if _, sp := s.contentProvider(context.Background(), "", spec, "curseforge"); sp.Provider != "curseforge" {
		t.Fatal("source must switch the catalogue")
	}
	if _, sp := s.contentProvider(context.Background(), "", gameserver.ContentSpec{Provider: "workshop", GameID: "garrysmod"}, "curseforge"); sp.Provider != "workshop" {
		t.Fatal("source must not switch a non-Minecraft catalogue")
	}
}
