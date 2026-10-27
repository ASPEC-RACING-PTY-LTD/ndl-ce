package httpapi

import (
	"context"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/no-dal/ndl-ce/internal/appdb"
	"github.com/no-dal/ndl-ce/internal/gameserver"
)

func TestGameFilesZipAndExtract(t *testing.T) {
	dir := t.TempDir()
	s, mem, token := testServer(t)
	s.Game = gameserver.NewRuntime(dir)
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)
	cluster, _ := mem.GetCluster(context.Background())
	enableGameServers(t, mem, cluster.ID)
	data := filepath.Join(dir, "gameservers", "gs-zip")
	for name, body := range map[string]string{
		"garrysmod/addons/wiremod/lua/init.lua": "print('wire')",
		"garrysmod/addons/wiremod/addon.json":   `{"title":"Wire"}`,
		"garrysmod/cfg/server.cfg":              "hostname JvS",
	} {
		p := filepath.Join(data, name)
		_ = os.MkdirAll(filepath.Dir(p), 0o750)
		_ = os.WriteFile(p, []byte(body), 0o640)
	}
	_ = os.MkdirAll(filepath.Join(data, "garrysmod/addons/wiremod/empty"), 0o750)
	_ = mem.CreateGameServer(context.Background(), appdb.GameServer{
		ID: "gs-zip", ClusterID: cluster.ID, Name: "Zip", TemplateID: "ndl-mindustry", Status: "ready",
		DataDir: data, EnvJSON: []byte(`{}`), PortsJSON: []byte(`[]`), Capabilities: []byte(`["files"]`), CreatedAt: time.Now().UTC(),
	})
	call := func(path, body string) (int, map[string]any) {
		return jsonCall(t, ts, cookie, "POST", "/api/v1/game-servers/gs-zip/files/"+path, body)
	}

	// A folder zips to <name>.zip beside it, with its tree inside.
	code, out := call("compress", `{"paths":["garrysmod/addons/wiremod"]}`)
	if code != 201 || out["path"] != "garrysmod/addons/wiremod.zip" || out["files"].(float64) != 2 {
		t.Fatalf("compress %d %v", code, out)
	}
	if code, _ := call("compress", `{"paths":["garrysmod/addons/wiremod"]}`); code != 409 {
		t.Fatalf("zip over an existing archive = %d", code)
	}
	code, out = call("compress", `{"paths":["garrysmod/cfg/server.cfg","garrysmod/addons/wiremod/addon.json"],"dest":"garrysmod/bundle"}`)
	if code != 201 || out["path"] != "garrysmod/bundle.zip" {
		t.Fatalf("compress files %d %v", code, out)
	}

	// It extracts somewhere else intact, empty folders included.
	code, out = call("extract", `{"path":"garrysmod/addons/wiremod.zip","dest":"restored"}`)
	if code != 200 {
		t.Fatalf("extract %d %v", code, out)
	}
	got, err := os.ReadFile(filepath.Join(data, "restored/wiremod/lua/init.lua"))
	if err != nil || string(got) != "print('wire')" {
		t.Fatalf("extracted file = %q, %v", got, err)
	}
	if st, err := os.Stat(filepath.Join(data, "restored/wiremod/empty")); err != nil || !st.IsDir() {
		t.Fatalf("empty folder not restored: %v", err)
	}
	// Without a destination it extracts next to the archive.
	if code, out := call("extract", `{"path":"garrysmod/bundle.zip"}`); code != 200 || out["dest"] != "garrysmod" {
		t.Fatalf("extract beside %d %v", code, out)
	}
	if _, err := os.Stat(filepath.Join(data, "garrysmod/server.cfg")); err != nil {
		t.Fatalf("bundle not extracted beside it: %v", err)
	}

	// Escapes and non-archives are refused.
	for _, body := range []string{
		`{"path":"garrysmod/cfg/server.cfg"}`,
		`{"path":"garrysmod/bundle.zip","dest":"../outside"}`,
		`{"path":"../x.zip"}`,
	} {
		if code, out := call("extract", body); code != 400 {
			t.Fatalf("extract %s = %d %v", body, code, out)
		}
	}
	if code, _ := call("compress", `{"paths":["../"]}`); code != 400 {
		t.Fatalf("compress outside = %d", code)
	}
	if _, err := os.Stat(filepath.Join(dir, "gameservers", "outside")); err == nil || strings.Contains(dir, "outside") {
		t.Fatal("extracted outside the server")
	}
}
