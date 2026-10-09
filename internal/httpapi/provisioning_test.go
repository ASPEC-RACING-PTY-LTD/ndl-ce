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

	"github.com/no-dal/ndl-ce/internal/gameserver"
)

func provCall(t *testing.T, ts *httptest.Server, cookie, method, path, body, confirm string) (int, map[string]any) {
	t.Helper()
	req, _ := http.NewRequest(method, ts.URL+"/api/v1"+path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if confirm != "" {
		req.Header.Set(confirmHeader, confirm)
	}
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	out := map[string]any{}
	_ = json.Unmarshal(raw, &out)
	return res.StatusCode, out
}

func TestProvisioningRunsAGameServerByExternalID(t *testing.T) {
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

	body := `{"kind":"game","name":"GS-1001","template_id":"ndl-minecraft-paper","memory_mb":4096,"disk_gb":20,"env":{"EULA":"true"},"labels":{"organisation":"org-1"}}`
	code, created := provCall(t, ts, cookie, "PUT", "/provisioning/services/svc-1001", body, "")
	if code != http.StatusCreated || created["resource_id"] == "" || created["state"] != "provisioning" {
		t.Fatalf("create %d %v", code, created)
	}
	// Retrying the same request is safe and creates nothing new.
	if code, again := provCall(t, ts, cookie, "PUT", "/provisioning/services/svc-1001", body, ""); code != http.StatusOK || again["resource_id"] != created["resource_id"] {
		t.Fatalf("retry %d %v", code, again)
	}
	if rows, _ := mem.ListGameServers(context.Background(), cluster.ID); len(rows) != 1 {
		t.Fatalf("a retried create must not make a second server: %d", len(rows))
	}
	if code, _ := provCall(t, ts, cookie, "PUT", "/provisioning/services/svc-1001", strings.Replace(body, "minecraft-paper", "minecraft-vanilla", 1), ""); code != http.StatusConflict {
		t.Fatalf("a different template on the same id must be refused, got %d", code)
	}

	// Polling starts the server once it is installed.
	deadline := time.Now().Add(5 * time.Second)
	state := ""
	for time.Now().Before(deadline) {
		_, got := provCall(t, ts, cookie, "GET", "/provisioning/services/svc-1001", "", "")
		state, _ = got["state"].(string)
		if state == "active" || state == "failed" {
			break
		}
		time.Sleep(30 * time.Millisecond)
	}
	if state != "active" {
		t.Fatalf("the service must become active, got %q", state)
	}

	if code, got := provCall(t, ts, cookie, "POST", "/provisioning/services/svc-1001/suspend", "{}", ""); code != http.StatusOK || got["state"] != "suspended" {
		t.Fatalf("suspend %d %v", code, got)
	}
	if code, got := provCall(t, ts, cookie, "POST", "/provisioning/services/svc-1001/resume", "{}", ""); code != http.StatusOK || got["suspended"] != false {
		t.Fatalf("resume %d %v", code, got)
	}

	if code, _ := provCall(t, ts, cookie, "DELETE", "/provisioning/services/svc-1001", "", ""); code != http.StatusConflict {
		t.Fatalf("destroy needs confirmation, got %d", code)
	}
	if code, got := provCall(t, ts, cookie, "DELETE", "/provisioning/services/svc-1001", "", destroyConfirm); code != http.StatusOK || got["existed"] != true {
		t.Fatalf("destroy %d %v", code, got)
	}
	if rows, _ := mem.ListGameServers(context.Background(), cluster.ID); len(rows) != 0 {
		t.Fatal("the game server must be gone")
	}
	if code, got := provCall(t, ts, cookie, "DELETE", "/provisioning/services/svc-1001", "", destroyConfirm); code != http.StatusOK || got["existed"] != false {
		t.Fatalf("a repeated destroy must succeed: %d %v", code, got)
	}

	code, capacity := provCall(t, ts, cookie, "GET", "/provisioning/capacity", "", "")
	if code != http.StatusOK || capacity["game_runtime"] == nil || capacity["allocated"] == nil {
		t.Fatalf("capacity %d %v", code, capacity)
	}
}

func TestProvisioningPassesCreateErrorsThroughAndRecordsNothing(t *testing.T) {
	s, mem, token := testServer(t)
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)
	cluster, _ := mem.GetCluster(context.Background())
	code, body := provCall(t, ts, cookie, "PUT", "/provisioning/services/ct-1", `{"kind":"container","name":"web 1","image_pin":"not a pin"}`, "")
	if code < 400 || body["error"] == nil {
		t.Fatalf("an invalid container must be refused with the reason: %d %v", code, body)
	}
	if rows, _ := mem.ListProvisionedServices(context.Background(), cluster.ID); len(rows) != 0 {
		t.Fatal("a failed create must not be recorded")
	}
	if code, _ := provCall(t, ts, cookie, "PUT", "/provisioning/services/bad id!", `{"kind":"game"}`, ""); code != http.StatusBadRequest && code != http.StatusNotFound {
		t.Fatalf("invalid external ids must be refused, got %d", code)
	}
	if got := sanitizeContainerName("GS 1001 / Minecraft"); got != "GS-1001-Minecraft" {
		t.Fatalf("container name %q", got)
	}
}
