package httpapi

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/no-dal/ndl-ce/internal/appdb"
	"github.com/no-dal/ndl-ce/internal/gameserver"
)

func TestGameServersSayDockerIsMissingBeforeCreating(t *testing.T) {
	s, mem, token := testServer(t)
	rt := gameserver.NewRuntime(t.TempDir())
	rt.Run = func(ctx context.Context, name string, args ...string) ([]byte, error) {
		if len(args) > 0 && args[0] == "version" {
			return nil, errors.New("fork/exec /usr/bin/docker: no such file or directory")
		}
		return []byte("ok"), nil
	}
	s.Game = rt
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)
	cluster, _ := mem.GetCluster(context.Background())
	enableGameServers(t, mem, cluster.ID)

	_, ready := updatesCall(t, ts, cookie, "GET", "/game-servers/runtime", "")
	if ready["ready"] != false || !strings.Contains(ready["reason"].(string), "Add Features") {
		t.Fatalf("missing Docker must be reported with guidance: %v", ready)
	}
	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/game-servers", strings.NewReader(`{"template_id":"ndl-mindustry","name":"x"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("create without Docker must be refused up front, got %d", res.StatusCode)
	}
	rows, _ := mem.ListGameServers(context.Background(), cluster.ID)
	if len(rows) != 0 {
		t.Fatal("no half-created server may be left behind")
	}

	// A server that failed before the message existed shows today's message.
	_ = mem.CreateGameServer(context.Background(), appdb.GameServer{
		ID: "gs-old", ClusterID: cluster.ID, Name: "Old", TemplateID: "ndl-mindustry", Status: "failed",
		ErrorRaw: "fork/exec /usr/bin/docker: no such file or directory", ErrorHuman: "The operation failed. fork/exec /usr/bin/docker: no such file or directory",
		EnvJSON: []byte(`{}`), PortsJSON: []byte(`[]`), Capabilities: []byte(`[]`), CreatedAt: time.Now().UTC(),
	})
	_, got := updatesCall(t, ts, cookie, "GET", "/game-servers/gs-old", "")
	if msg, _ := got["error_human"].(string); !strings.Contains(msg, "Docker Engine is not installed") {
		t.Fatalf("stored failures must show the current plain message: %v", got["error_human"])
	}
}
