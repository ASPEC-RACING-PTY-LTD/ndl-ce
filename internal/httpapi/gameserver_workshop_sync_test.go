package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/no-dal/ndl-ce/internal/appdb"
	"github.com/no-dal/ndl-ce/internal/gameserver"
)

type stubWorkshop struct{ updated int64 }

func (s *stubWorkshop) Collection(context.Context, string) ([]string, error) {
	return []string{"100"}, nil
}

func (s *stubWorkshop) Files(context.Context, []string) (map[string]gameserver.WorkshopFile, error) {
	return map[string]gameserver.WorkshopFile{"100": {ID: "100", Title: "Props", TimeUpdated: s.updated, Available: true, AppID: 4000}}, nil
}

func TestWorkshopSyncSchedule(t *testing.T) {
	s, mem, token := testServer(t)
	rt := gameserver.NewRuntime(t.TempDir())
	var mu sync.Mutex
	var sent []string
	mounted := false
	rt.Run = func(_ context.Context, _ string, args ...string) ([]byte, error) {
		if len(args) > 0 && args[0] == "inspect" {
			if mounted {
				return []byte("/home/container|true\n/home/container/garrysmod/data/cs_sync/items|false\n"), nil
			}
			return []byte("/home/container|true\n"), nil
		}
		if len(args) > 0 && args[0] == "exec" {
			mu.Lock()
			sent = append(sent, args[len(args)-1])
			mu.Unlock()
		}
		return []byte("ok"), nil
	}
	s.Game = rt
	now := time.Unix(1731700000, 0)
	s.Now = func() time.Time { return now }
	steam := &stubWorkshop{updated: 1000}
	fetches := 0
	s.wsync.api = steam
	s.wsync.fetch = func(_ context.Context, ids []string, dir string) (map[string]string, map[string]string) {
		fetches++
		got := map[string]string{}
		for _, id := range ids {
			p := filepath.Join(dir, id)
			_ = os.WriteFile(p, []byte("GMAD\x03"+strings.Repeat("\x00", 17)+"n\x00d\x00a\x00\x01\x00\x00\x00\x00\x00\x00\x00"), 0o644)
			got[id] = p
		}
		return got, nil
	}
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)
	cluster, _ := mem.GetCluster(context.Background())
	enableGameServers(t, mem, cluster.ID)
	data := t.TempDir()
	row := appdb.GameServer{ID: "11111111-2222-3333-4444-555555555555", ClusterID: cluster.ID, Name: "JvS", TemplateID: "ndl-gmod", Game: "gmod",
		Status: gameserver.StatusRunning, ContainerID: "c1", DataDir: data, EnvJSON: []byte(`{"WORKSHOP_COLLECTION":"3520969473"}`)}
	if err := mem.CreateGameServer(context.Background(), row); err != nil {
		t.Fatal(err)
	}
	run := func() { s.workshopSyncServer(context.Background(), row) }
	manifest := func() gameserver.SyncManifest {
		var m gameserver.SyncManifest
		raw, _ := os.ReadFile(filepath.Join(data, gameserver.SyncDirRel, gameserver.SyncManifestFile))
		_ = json.Unmarshal(raw, &m)
		return m
	}
	request := func(id string) {
		raw, _ := json.Marshal(gameserver.SyncRequest{Version: 1, ID: id, Type: "check"})
		_ = os.WriteFile(filepath.Join(data, gameserver.SyncDirRel, gameserver.SyncRequestFile), raw, 0o644)
	}

	if code, got := apiJSON(t, ts, cookie, "PUT", "/api/v1/game-servers/"+row.ID+"/workshop-sync", `{"enabled":true}`); code != http.StatusOK || got["enabled"] != true || got["collection"] != "3520969473" {
		t.Fatalf("enable %d %v", code, got)
	}
	// The panel reads .length on both lists: never null.
	if code, got := apiJSON(t, ts, cookie, "GET", "/api/v1/game-servers/"+row.ID+"/workshop-sync", ``); code != http.StatusOK {
		t.Fatalf("get %d", code)
	} else if _, ok := got["errors"].([]any); !ok {
		t.Fatalf("errors must be a list: %#v", got["errors"])
	} else if _, ok := got["items"].([]any); !ok {
		t.Fatalf("items must be a list: %#v", got["items"])
	}
	// A server started before the packs folder existed gets nothing.
	run()
	if st := s.loadSyncState(row.ID); !strings.Contains(st.LastError, "restart the server once") || manifest().Seq != 0 {
		t.Fatalf("without the read-only mount: %+v", st)
	}
	mounted = true
	now = now.Add(2 * time.Minute) // past the retry pause
	// First run after enabling: baseline (what runs now), then a scheduled check.
	run()
	if m := manifest(); m.Seq != 1 || len(m.Items) != 0 || fetches != 0 {
		t.Fatalf("first check %+v fetches %d", m, fetches)
	}
	if len(sent) != 1 || sent[0] != "cs_sync_ready 1" {
		t.Fatalf("console %v", sent)
	}

	// A request inside the 5 minutes waits for the next allowed check.
	steam.updated = 2000
	request("req-1")
	now = now.Add(time.Minute)
	run()
	if m := manifest(); m.Seq != 1 {
		t.Fatal("a request inside the window must wait")
	}
	if got := s.loadSyncState(row.ID); got.Pending != "req-1" {
		t.Fatalf("pending %q", got.Pending)
	}
	now = now.Add(5 * time.Minute)
	run()
	m := manifest()
	if m.Seq != 2 || m.RequestID == nil || *m.RequestID != "req-1" || len(m.Items) != 1 || m.Items[0].State != "updated" || fetches != 1 {
		t.Fatalf("answered check %+v", m)
	}
	// The same id is never processed twice.
	now = now.Add(10 * time.Minute)
	run()
	if manifest().Seq != 2 {
		t.Fatal("a handled request ran again")
	}
	// The hour passes: a scheduled check.
	now = now.Add(time.Hour)
	run()
	if m := manifest(); m.Seq != 3 || m.RequestID != nil {
		t.Fatalf("hourly %+v", m)
	}
	// A restart takes a new baseline.
	row.ContainerID = "c2"
	run()
	if st := s.loadSyncState(row.ID); st.ContainerID != "c2" || st.Baseline["100"] != 2000 {
		t.Fatalf("baseline after restart %+v", st)
	}
	// Check now from the panel queues a request.
	code, got := apiJSON(t, ts, cookie, "POST", "/api/v1/game-servers/"+row.ID+"/workshop-sync/check", `{}`)
	if code != http.StatusAccepted || !strings.HasPrefix(got["pending_request"].(string), "panel-") {
		t.Fatalf("check now %d %v", code, got)
	}
	// Turned off: nothing runs.
	apiJSON(t, ts, cookie, "PUT", "/api/v1/game-servers/"+row.ID+"/workshop-sync", `{"enabled":false}`)
	now = now.Add(2 * time.Hour)
	run()
	if manifest().Seq != 3 {
		t.Fatal("a disabled sync must not run")
	}
}
