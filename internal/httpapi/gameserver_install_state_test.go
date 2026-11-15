package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/no-dal/ndl-ce/internal/appdb"
	"github.com/no-dal/ndl-ce/internal/gameserver"
)

// An install runs in the background; what the admin changes meanwhile must
// survive it, and the server cannot be started or reinstalled until it ends.
func TestGameInstallKeepsChangesMadeWhileItRuns(t *testing.T) {
	s, mem, token := testServer(t)
	rt := gameserver.NewRuntime(t.TempDir())
	release := make(chan struct{})
	var once sync.Once
	started := make(chan struct{})
	rt.Run = func(ctx context.Context, name string, args ...string) ([]byte, error) {
		joined := strings.Join(args, " ")
		if strings.HasPrefix(joined, "run ") && strings.Contains(joined, "-install") {
			once.Do(func() { close(started) })
			<-release
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

	code, created := apiJSON(t, ts, cookie, "POST", "/api/v1/game-servers", `{"name":"Paper Lab","template_id":"ndl-minecraft-paper","env":{"EULA":"true"}}`)
	if code != http.StatusAccepted {
		t.Fatalf("create %d %v", code, created)
	}
	id := created["id"].(string)
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("install never started")
	}

	if code, got := apiJSON(t, ts, cookie, "PATCH", "/api/v1/game-servers/"+id, `{"name":"Renamed Lab"}`); code != http.StatusOK {
		t.Fatalf("rename while installing %d %v", code, got)
	}
	if code, _ := apiJSON(t, ts, cookie, "POST", "/api/v1/game-servers/"+id+"/start", `{}`); code != http.StatusConflict {
		t.Fatalf("start while installing must be refused, got %d", code)
	}
	if code, _ := apiJSON(t, ts, cookie, "POST", "/api/v1/game-servers/"+id+"/reinstall", `{}`); code != http.StatusConflict {
		t.Fatalf("reinstall while installing must be refused, got %d", code)
	}
	close(release)

	deadline := time.Now().Add(3 * time.Second)
	for {
		row, _ := mem.GetGameServer(context.Background(), cluster.ID, id)
		if row != nil && !gameInstalling(row) {
			if row.Name != "Renamed Lab" {
				t.Fatalf("the install wrote back its old copy over the rename: %q", row.Name)
			}
			if row.Status != gameserver.StatusReady {
				t.Fatalf("install must finish ready: %+v", row.Status)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("install did not finish")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestReconcileInterruptedGameInstalls(t *testing.T) {
	s, mem, _ := testServer(t)
	cluster, _ := mem.GetCluster(context.Background())
	mk := func(id, status string) {
		if err := mem.CreateGameServer(context.Background(), appdb.GameServer{ID: id, ClusterID: cluster.ID, Name: id, Status: status, InstallPhase: "downloading"}); err != nil {
			t.Fatal(err)
		}
	}
	mk("a", gameserver.StatusInstalling)
	mk("b", gameserver.StatusPending)
	mk("c", gameserver.StatusReady)
	if n := s.ReconcileInterruptedGameInstalls(context.Background()); n != 2 {
		t.Fatalf("want 2 interrupted installs, got %d", n)
	}
	for _, id := range []string{"a", "b"} {
		row, _ := mem.GetGameServer(context.Background(), cluster.ID, id)
		if row.Status != gameserver.StatusFailed || row.InstallPhase != "failed" || !strings.Contains(humanGameError(*row), "Reinstall") {
			t.Fatalf("%s must be failed with advice: %+v", id, row)
		}
	}
	if row, _ := mem.GetGameServer(context.Background(), cluster.ID, "c"); row.Status != gameserver.StatusReady {
		t.Fatalf("a finished server must be left alone: %+v", row)
	}
}
