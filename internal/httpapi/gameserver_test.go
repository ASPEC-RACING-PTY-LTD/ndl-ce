package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/no-dal/ndl-ce/internal/appdb"
	"github.com/no-dal/ndl-ce/internal/features"
	"github.com/no-dal/ndl-ce/internal/gameserver"
	"github.com/no-dal/ndl-ce/internal/rbac"
	"github.com/no-dal/ndl-ce/internal/secutil"
)

func enableGameServers(t *testing.T, mem *appdb.Memory, clusterID string) {
	t.Helper()
	if err := mem.UpsertFeature(context.Background(), appdb.Feature{
		ClusterID: clusterID, ID: features.IDGameServers, Enabled: true,
		PackageStatus: appdb.FeatureInstalled, RuntimeStatus: appdb.FeatureNotStarted,
		Reason: features.GameServersReason, UpdatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
}

func TestGameServersHiddenUntilEnabled(t *testing.T) {
	s, mem, token := testServer(t)
	s.Game = gameserver.NewRuntime(t.TempDir())
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)
	req, _ := http.NewRequest("GET", ts.URL+"/api/v1/game-servers", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ := ts.Client().Do(req)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusNotFound {
		t.Fatalf("disabled=%d", res.StatusCode)
	}
	cluster, _ := mem.GetCluster(context.Background())
	enableGameServers(t, mem, cluster.ID)
	req, _ = http.NewRequest("GET", ts.URL+"/api/v1/game-servers", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(req)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("enabled=%d", res.StatusCode)
	}
	_ = res.Body.Close()
}

func TestGameServerLifecycleWithFakeRuntime(t *testing.T) {
	s, mem, token := testServer(t)
	rt := gameserver.NewRuntime(t.TempDir())
	rt.Run = func(ctx context.Context, name string, args ...string) ([]byte, error) {
		joined := strings.Join(args, " ")
		if strings.Contains(joined, "run") && !strings.Contains(joined, "--rm") {
			return []byte("cid-test"), nil
		}
		if strings.Contains(joined, "inspect") {
			return []byte("true"), nil
		}
		return []byte("ok"), nil
	}
	s.Game = rt
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)
	cluster, _ := mem.GetCluster(context.Background())
	enableGameServers(t, mem, cluster.ID)

	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/game-servers", strings.NewReader(`{"name":"Paper Lab","template_id":"ndl-minecraft-paper","env":{"EULA":"true"}}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusAccepted {
		t.Fatalf("create %d %s", res.StatusCode, raw)
	}
	var created map[string]any
	if err := json.Unmarshal(raw, &created); err != nil {
		t.Fatal(err)
	}
	id, _ := created["id"].(string)
	if id == "" {
		t.Fatal(string(raw))
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		row, _ := mem.GetGameServer(context.Background(), cluster.ID, id)
		if row != nil && (row.Status == gameserver.StatusReady || row.Status == gameserver.StatusFailed) {
			if row.Status != gameserver.StatusReady {
				t.Fatalf("install %s %s", row.Status, row.ErrorHuman)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("install did not finish")
		}
		time.Sleep(20 * time.Millisecond)
	}

	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/game-servers/"+id+"/start", strings.NewReader(`{}`))
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(req)
	raw, _ = io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("start %d %s", res.StatusCode, raw)
	}

	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/game-servers/"+id+"/files/content", strings.NewReader(`{"path":"server.properties","content":"motd=Hi\nmax-players=8\n"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(req)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("write %d", res.StatusCode)
	}

	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/game-servers/"+id+"/files/content", strings.NewReader(`{"path":"../escape","content":"no"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(req)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("escape %d", res.StatusCode)
	}

	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/game-servers/"+id+"/backups", strings.NewReader(`{"name":"snap"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(req)
	raw, _ = io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("backup %d %s", res.StatusCode, raw)
	}

	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/game-servers/"+id+"/stop", strings.NewReader(`{}`))
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(req)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("stop %d", res.StatusCode)
	}

	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/game-servers/"+id+"/delete", strings.NewReader(`{}`))
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(req)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusConflict {
		t.Fatalf("delete without confirm %d", res.StatusCode)
	}
	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/game-servers/"+id+"/delete", strings.NewReader(`{}`))
	req.Header.Set(confirmHeader, "delete")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(req)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("delete %d", res.StatusCode)
	}
}

func TestGameServerDisableDoesNotDelete(t *testing.T) {
	s, mem, token := testServer(t)
	rt := gameserver.NewRuntime(t.TempDir())
	rt.Run = func(ctx context.Context, name string, args ...string) ([]byte, error) { return []byte("ok"), nil }
	s.Game = rt
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)
	cluster, _ := mem.GetCluster(context.Background())
	enableGameServers(t, mem, cluster.ID)
	_ = mem.CreateGameServer(context.Background(), appdb.GameServer{
		ID: "gs-keep", ClusterID: cluster.ID, Name: "Keep", TemplateID: "ndl-mindustry", Status: "ready",
		EnvJSON: []byte(`{}`), PortsJSON: []byte(`[]`), Capabilities: []byte(`[]`), CreatedAt: time.Now().UTC(),
	})
	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/features/gameservers/disable", strings.NewReader(`{}`))
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ := ts.Client().Do(req)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("disable without confirm %d", res.StatusCode)
	}
	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/features/gameservers/disable", strings.NewReader(`{}`))
	req.Header.Set(confirmHeader, features.DisableConfirm)
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(req)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("disable %d", res.StatusCode)
	}
	row, _ := mem.GetGameServer(context.Background(), cluster.ID, "gs-keep")
	if row == nil {
		t.Fatal("disable deleted the server")
	}
}

func TestGameServerSecretsRedactedAndViewerDenied(t *testing.T) {
	s, mem, token := testServer(t)
	s.Game = gameserver.NewRuntime(t.TempDir())
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)
	cluster, _ := mem.GetCluster(context.Background())
	enableGameServers(t, mem, cluster.ID)
	_ = mem.CreateGameServer(context.Background(), appdb.GameServer{
		ID: "gs-secret", ClusterID: cluster.ID, Name: "Secret", TemplateID: "ndl-fivem", Status: "ready",
		OwnerUserID: "nobody", EnvJSON: []byte(`{"FIVEM_LICENSE":"super-secret-key","SERVER_NAME":"RP"}`),
		PortsJSON: []byte(`[]`), Capabilities: []byte(`["console"]`), CreatedAt: time.Now().UTC(),
	})
	req, _ := http.NewRequest("GET", ts.URL+"/api/v1/game-servers/gs-secret", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ := ts.Client().Do(req)
	raw, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("%d %s", res.StatusCode, raw)
	}
	if strings.Contains(string(raw), "super-secret-key") {
		t.Fatalf("license leaked: %s", raw)
	}

	viewer := appdb.User{ID: uuid.NewString(), ClusterID: cluster.ID, Username: "view"}
	if err := mem.CreateUser(context.Background(), viewer); err != nil {
		t.Fatal(err)
	}
	if err := mem.BindRole(context.Background(), cluster.ID, viewer.ID, rbac.Viewer); err != nil {
		t.Fatal(err)
	}
	plain := "ndl_viewer_gs"
	_ = mem.CreateToken(context.Background(), appdb.APIToken{
		ID: uuid.NewString(), ClusterID: cluster.ID, UserID: viewer.ID, Name: "v",
		TokenHash: secutil.HashSHA256(plain), Prefix: "ndl_view",
	})
	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/game-servers/gs-secret/start", strings.NewReader(`{}`))
	req.Header.Set("Authorization", "Bearer "+plain)
	res, _ = ts.Client().Do(req)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusForbidden && res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("viewer power %d", res.StatusCode)
	}
}

func TestGameFileWriteStaysInJail(t *testing.T) {
	dir := t.TempDir()
	s, mem, token := testServer(t)
	s.Game = gameserver.NewRuntime(dir)
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)
	cluster, _ := mem.GetCluster(context.Background())
	enableGameServers(t, mem, cluster.ID)
	data := filepath.Join(dir, "gameservers", "gs-jail")
	_ = os.MkdirAll(data, 0o750)
	_ = mem.CreateGameServer(context.Background(), appdb.GameServer{
		ID: "gs-jail", ClusterID: cluster.ID, Name: "Jail", TemplateID: "ndl-mindustry", Status: "ready",
		DataDir: data, EnvJSON: []byte(`{}`), PortsJSON: []byte(`[]`), Capabilities: []byte(`["files"]`), CreatedAt: time.Now().UTC(),
	})
	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/game-servers/gs-jail/files/content", strings.NewReader(`{"path":"ok.txt","content":"hello"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ := ts.Client().Do(req)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("write %d", res.StatusCode)
	}
	if _, err := os.Stat(filepath.Join(dir, "ok.txt")); err == nil {
		t.Fatal("wrote outside jail")
	}
}

func TestGameCatalogueSearchAndRequiredFields(t *testing.T) {
	s, mem, token := testServer(t)
	rt := gameserver.NewRuntime(t.TempDir())
	rt.Run = func(ctx context.Context, name string, args ...string) ([]byte, error) { return []byte("ok"), nil }
	s.Game = rt
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)
	cluster, _ := mem.GetCluster(context.Background())
	enableGameServers(t, mem, cluster.ID)

	req, _ := http.NewRequest("GET", ts.URL+"/api/v1/game-servers/catalogue?q=pz", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ := ts.Client().Do(req)
	raw, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK || !strings.Contains(string(raw), "ndl-zomboid") {
		t.Fatalf("pz search %d %s", res.StatusCode, raw)
	}

	req, _ = http.NewRequest("GET", ts.URL+"/api/v1/game-servers/catalogue?group=minecraft", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(req)
	raw, _ = io.ReadAll(res.Body)
	_ = res.Body.Close()
	if !strings.Contains(string(raw), "ndl-minecraft-paper") || strings.Contains(string(raw), "ndl-valheim") {
		t.Fatalf("minecraft group %s", raw)
	}

	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/game-servers", strings.NewReader(`{"name":"No Name","template_id":"ndl-valheim","env":{"SERVER_PASSWORD":""}}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(req)
	raw, _ = io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("valheim empty password %d %s", res.StatusCode, raw)
	}

	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/game-servers", strings.NewReader(`{"name":"DST Lab","template_id":"ndl-dst","env":{"CLUSTER_TOKEN":""}}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ = io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusAccepted {
		t.Fatalf("dst create without token should install %d %s", res.StatusCode, raw)
	}
	var created map[string]any
	if err := json.Unmarshal(raw, &created); err != nil {
		t.Fatal(err)
	}
	id, _ := created["id"].(string)
	deadline := time.Now().Add(3 * time.Second)
	for {
		row, _ := mem.GetGameServer(context.Background(), cluster.ID, id)
		if row != nil && (row.Status == gameserver.StatusReady || row.Status == gameserver.StatusFailed) {
			break
		}
		if time.Now().After(deadline) {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/game-servers/"+id+"/start", strings.NewReader(`{}`))
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(req)
	raw, _ = io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusBadGateway || !strings.Contains(string(raw), "Klei") {
		t.Fatalf("dst start without token %d %s", res.StatusCode, raw)
	}
}

func TestArchiveDirCopiesExactHeaderSize(t *testing.T) {
	src := t.TempDir()
	dest := filepath.Join(t.TempDir(), "snap.tar.gz")
	if err := os.WriteFile(filepath.Join(src, "server.jar"), bytes.Repeat([]byte("j"), 256<<10), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := archiveDir(src, dest); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(dest)
	if err != nil || st.Size() < 100 {
		t.Fatalf("archive %v %v", st, err)
	}
}
