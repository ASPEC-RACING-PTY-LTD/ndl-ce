package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/no-dal/ndl-ce/internal/gameserver"
)

// gmodTestServer creates a Garry's Mod server on a fake runtime. onRun
// sees every docker call.
func gmodTestServer(t *testing.T, onRun func(args string, rt *gameserver.Runtime)) (*Server, *gameserver.Runtime, string, func(method, path, body string) (int, map[string]any), func() *gameServerRowView) {
	t.Helper()
	s, mem, token := testServer(t)
	rt := gameserver.NewRuntime(t.TempDir())
	var mu sync.Mutex
	rt.Run = func(ctx context.Context, name string, args ...string) ([]byte, error) {
		joined := strings.Join(args, " ")
		mu.Lock()
		if onRun != nil {
			onRun(joined, rt)
		}
		mu.Unlock()
		if strings.HasPrefix(joined, "inspect") {
			return []byte("true"), nil
		}
		if strings.HasPrefix(joined, "run") || strings.HasPrefix(joined, "create") {
			return []byte("cid-test"), nil
		}
		return []byte("ok"), nil
	}
	s.Game = rt
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close)
	cookie := claimAdmin(t, ts, token)
	cluster, _ := mem.GetCluster(context.Background())
	enableGameServers(t, mem, cluster.ID)
	call := func(method, path, body string) (int, map[string]any) {
		req, _ := http.NewRequest(method, ts.URL+path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
		res, err := ts.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := io.ReadAll(res.Body)
		_ = res.Body.Close()
		var out map[string]any
		_ = json.Unmarshal(raw, &out)
		return res.StatusCode, out
	}
	code, created := call("POST", "/api/v1/game-servers", `{"name":"GMod","template_id":"ndl-gmod"}`)
	if code != http.StatusAccepted {
		t.Fatalf("create %d %v", code, created)
	}
	id, _ := created["id"].(string)
	row := func() *gameServerRowView {
		r, _ := mem.GetGameServer(context.Background(), cluster.ID, id)
		if r == nil {
			return nil
		}
		return &gameServerRowView{Status: r.Status, Startup: r.Startup, Env: string(r.EnvJSON), Error: r.ErrorHuman}
	}
	waitFor(t, func() bool { r := row(); return r != nil && r.Status == gameserver.StatusReady })
	return s, rt, id, call, row
}

type gameServerRowView struct {
	Status, Startup, Env, Error string
}

func waitFor(t *testing.T, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatal("timed out")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestGameBuildSwitchDownloadsThenStarts(t *testing.T) {
	var installArgs string
	_, _, id, call, row := gmodTestServer(t, func(args string, rt *gameserver.Runtime) {
		// The SteamCMD container puts the 64-bit launcher down.
		if strings.Contains(args, "-install") && strings.Contains(args, "STEAM_BETA=x86-64") {
			installArgs = args
			for _, f := range strings.Fields(args) {
				if strings.HasPrefix(f, "ndl-gs-") && strings.HasSuffix(f, "-install") {
					sid := strings.TrimSuffix(strings.TrimPrefix(f, "ndl-gs-"), "-install")
					_ = os.WriteFile(filepath.Join(rt.DataDir(sid), "srcds_run_x64"), []byte("x"), 0o755)
				}
			}
		}
	})
	code, st := call("GET", "/api/v1/game-servers/"+id+"/build", "")
	if code != http.StatusOK || st["current"] != "x86" || st["supported"] != true {
		t.Fatalf("build state %d %v", code, st)
	}
	if code, out := call("PUT", "/api/v1/game-servers/"+id+"/build", `{"build":"nope"}`); code != http.StatusBadRequest {
		t.Fatalf("unknown build %d %v", code, out)
	}
	if code, out := call("PUT", "/api/v1/game-servers/"+id+"/build", `{"build":"x86-64"}`); code != http.StatusOK || out["current"] != "x86-64" {
		t.Fatalf("set build %d %v", code, out)
	}
	r := row()
	if !strings.HasPrefix(r.Startup, "./srcds_run_x64 -game garrysmod") || !strings.Contains(r.Env, `"STEAM_BETA":"x86-64"`) {
		t.Fatalf("after switch: %+v", r)
	}
	// Starting downloads the files first, then starts by itself.
	if code, out := call("POST", "/api/v1/game-servers/"+id+"/start", `{}`); code != http.StatusAccepted {
		t.Fatalf("start %d %v", code, out)
	}
	waitFor(t, func() bool {
		r := row()
		return r.Status == gameserver.StatusRunning || r.Status == gameserver.StatusFailed
	})
	if r := row(); r.Status != gameserver.StatusRunning {
		t.Fatalf("after download: %+v", r)
	}
	if !strings.Contains(installArgs, "NDL_STEAM_VALIDATE=0") {
		t.Fatalf("the build download must skip validate: %s", installArgs)
	}
	// A running server cannot switch.
	if code, _ := call("PUT", "/api/v1/game-servers/"+id+"/build", `{"build":"x86"}`); code != http.StatusConflict {
		t.Fatalf("switch while running %d", code)
	}
	if code, _ := call("POST", "/api/v1/game-servers/"+id+"/stop", `{}`); code != http.StatusOK {
		t.Fatal("stop")
	}
	// Back to 32-bit: the launcher name changes, the arguments stay.
	if code, out := call("PUT", "/api/v1/game-servers/"+id+"/build", `{"build":"x86"}`); code != http.StatusOK || out["current"] != "x86" {
		t.Fatalf("back to 32-bit %d %v", code, out)
	}
	if r := row(); !strings.HasPrefix(r.Startup, "./srcds_run -game garrysmod") || strings.Contains(r.Env, "STEAM_BETA") {
		t.Fatalf("after switching back: %+v", r)
	}
}

func TestGameDatabaseAPI(t *testing.T) {
	s, _, id, call, _ := gmodTestServer(t, nil)
	code, db := call("GET", "/api/v1/game-servers/"+id+"/database", "")
	if code != http.StatusOK || db["supported"] != true || db["state"] != "none" {
		t.Fatalf("empty %d %v", code, db)
	}
	if code, _ := call("PUT", "/api/v1/game-servers/"+id+"/database", `{"database":"x; DROP"}`); code != http.StatusBadRequest {
		t.Fatalf("unsafe name %d", code)
	}
	if code, _ := call("POST", "/api/v1/game-servers/"+id+"/database/install", `{}`); code != http.StatusUnprocessableEntity {
		t.Fatalf("install before naming %d", code)
	}
	code, db = call("PUT", "/api/v1/game-servers/"+id+"/database", `{"database":"jvs"}`)
	if code != http.StatusOK || db["state"] != "configured" || db["database"] != "jvs" || db["host"] != "mariadb" {
		t.Fatalf("configure %d %v", code, db)
	}
	if _, ok := db["password"]; ok {
		t.Fatal("the plain view must not carry the password")
	}
	code, cred := call("GET", "/api/v1/game-servers/"+id+"/database/credentials", "")
	if pw, _ := cred["password"].(string); code != http.StatusOK || len(pw) != 32 {
		t.Fatalf("credentials %d %v", code, cred)
	}
	if code, out := call("POST", "/api/v1/game-servers/"+id+"/database/install", `{}`); code != http.StatusAccepted {
		t.Fatalf("install %d %v", code, out)
	}
	s.waitGameJobs()
	code, db = call("GET", "/api/v1/game-servers/"+id+"/database", "")
	job, _ := db["job"].(map[string]any)
	if db["state"] != "running" || db["installed_at"] == nil || job["op"] != "database" || job["error"] != nil {
		t.Fatalf("after install %d %v", code, db)
	}
	if code, _ := call("PUT", "/api/v1/game-servers/"+id+"/database", `{"database":"renamed"}`); code != http.StatusUnprocessableEntity {
		t.Fatalf("rename after install %d", code)
	}
	if code, _ := call("POST", "/api/v1/game-servers/"+id+"/database/remove", `{"delete_data":true,"confirm":"wrong"}`); code != http.StatusConflict {
		t.Fatalf("delete without the right name %d", code)
	}
	if code, out := call("POST", "/api/v1/game-servers/"+id+"/database/remove", `{"delete_data":true,"confirm":"jvs"}`); code != http.StatusOK || out["state"] != "none" {
		t.Fatalf("delete %d %v", code, out)
	}
	// A server is listed with the database tab once its game supports it.
	_, srv := call("GET", "/api/v1/game-servers/"+id, "")
	caps, _ := json.Marshal(srv["capabilities"])
	if !strings.Contains(string(caps), "databases") {
		t.Fatalf("capabilities %s", caps)
	}
}
