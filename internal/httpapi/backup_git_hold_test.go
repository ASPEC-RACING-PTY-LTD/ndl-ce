package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/no-dal/ndl-ce/internal/appdb"
	"github.com/no-dal/ndl-ce/internal/backup"
	"github.com/no-dal/ndl-ce/internal/backuphost"
	"github.com/no-dal/ndl-ce/internal/qemu"
	"github.com/no-dal/ndl-ce/internal/storage"
)

// gitHoldBackup holds every capture that asks to be held until the Git work
// is saved.
type gitHoldBackup struct {
	*fakeBackup
	mu    sync.Mutex
	dirty bool
	saved []string
	holdN int
}

func (f *gitHoldBackup) CopyBackup(ctx context.Context, action, src, dest string) (storage.CopyResult, error) {
	var req backuphost.Request
	_ = json.Unmarshal([]byte(dest), &req)
	f.mu.Lock()
	defer f.mu.Unlock()
	switch {
	case action == qemu.BackupV2Capture && req.HoldUncommittedGit && f.dirty:
		f.holdN++
		extra, _ := json.Marshal(backuphost.Result{GitHold: []backup.GitRepo{{
			Path: "/root/app", Branch: "main", Commit: strings.Repeat("a", 40), Dirty: true,
			Changed: []string{"src/main.go", ".env"}, Kept: []string{"/root/app/.env"},
			Remotes: []backup.GitRemote{{Name: "origin", URL: "https://git.example.com/me/app.git"}},
		}}})
		return storage.CopyResult{Extra: string(extra)}, nil
	case action == qemu.BackupV2GitSave:
		f.saved = append(f.saved, req.Repos...)
		f.dirty = false
		extra, _ := json.Marshal(backuphost.Result{GitSave: []backuphost.GitSaveResult{{Path: req.Repos[0], Committed: true, Pushed: true}}})
		return storage.CopyResult{Extra: string(extra)}, nil
	}
	return f.fakeBackup.CopyBackup(ctx, action, src, dest)
}

func TestUncommittedGitHoldsTheBackupUntilTheOwnerSavesIt(t *testing.T) {
	s, mem, token := testServer(t)
	cluster, _ := mem.GetCluster(context.Background())
	nodeID := uuid.NewString()
	_ = mem.UpsertNode(context.Background(), appdb.Node{ID: nodeID, ClusterID: cluster.ID, Name: "local"})
	poolID, netID := seedCompute(t, mem, cluster.ID, nodeID)
	s.Storage = fakeStorage{vol: storage.CreateVolumeResult{Handle: storage.VolumeHandle{
		BackendType: storage.BackendDirectory, BackendRef: "volumes/container-root/x",
		Kind: storage.KindFilesystem, Class: storage.ClassContainerRoot, Format: storage.FormatDirectory,
	}}}
	s.Workloads = &fakeWorkloads{}
	fb := &gitHoldBackup{fakeBackup: &fakeBackup{}, dirty: true}
	s.Backup = fb
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)
	call := func(method, path, body string) (int, map[string]any) {
		t.Helper()
		req, _ := http.NewRequest(method, ts.URL+path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
		res, err := ts.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := io.ReadAll(res.Body)
		_ = res.Body.Close()
		out := map[string]any{}
		_ = json.Unmarshal(raw, &out)
		return res.StatusCode, out
	}
	code, ct := call("POST", "/api/v1/workloads", `{"name":"app","kind":"system-container","image_pin":"alpine/3.21/amd64/default","pool_id":"`+poolID+`","network_id":"`+netID+`","desired_power":"stopped"}`)
	if code != http.StatusCreated {
		t.Fatalf("ct %d %v", code, ct)
	}
	wid := ct["id"].(string)
	code, tgt := call("POST", "/api/v1/backups/targets", `{"name":"local-disk","kind":"local","locator":"`+t.TempDir()+`","password":"p"}`)
	if code != http.StatusCreated {
		t.Fatalf("target %d %v", code, tgt)
	}
	run := `{"workload_id":"` + wid + `","target_id":"` + tgt["id"].(string) + `","capture_mode":"smart"}`

	// Ignoring is the default: the capture is not asked to hold.
	if _, ws := call("GET", "/api/v1/backups/workspace", ""); ws["ignore_uncommitted_git"] != true {
		t.Fatalf("ignoring uncommitted Git must be on by default: %v", ws["ignore_uncommitted_git"])
	}
	if code, out := call("POST", "/api/v1/backups/run", run); code != http.StatusAccepted || out["status"] != appdb.BackupSucceeded {
		t.Fatalf("default run %d %v", code, out)
	}

	if code, ws := call("PATCH", "/api/v1/backups/workspace", `{"ignore_uncommitted_git":false}`); code != http.StatusOK || ws["ignore_uncommitted_git"] != false {
		t.Fatalf("turn ignoring off %d %v", code, ws)
	}
	code, out := call("POST", "/api/v1/backups/run", run)
	if code != http.StatusAccepted || out["status"] != appdb.BackupFailed || !strings.Contains(out["error"].(string), "backup held") {
		t.Fatalf("held run %d %v", code, out)
	}
	call("POST", "/api/v1/backups/run", run)
	_, holds := call("GET", "/api/v1/backups/git-holds", "")
	items, _ := holds["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("holds %v", holds)
	}
	repo := items[0].(map[string]any)["repos"].([]any)[0].(map[string]any)
	if unsaved := repo["unsaved"].([]any); len(unsaved) != 1 || unsaved[0] != "src/main.go" {
		t.Fatalf("a kept .env is not unsaved work: %v", repo)
	}
	events, _ := mem.ListEvents(context.Background(), cluster.ID, 100)
	n := 0
	for _, e := range events {
		if e.Type == "backup.git_hold" {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("the Owner is told once per change, got %d events", n)
	}

	code, out = call("POST", "/api/v1/backups/git-holds/"+wid, `{"action":"save"}`)
	if code != http.StatusOK || out["held"] != false || len(fb.saved) != 1 || fb.saved[0] != "/root/app" {
		t.Fatalf("save %d %v saved=%v", code, out, fb.saved)
	}
	if r, _ := out["run"].(map[string]any); r["status"] != appdb.BackupSucceeded {
		t.Fatalf("the retried backup must succeed: %v", out)
	}
	if _, holds := call("GET", "/api/v1/backups/git-holds", ""); len(holds["items"].([]any)) != 0 {
		t.Fatalf("hold must clear: %v", holds)
	}

	fb.dirty = true
	call("POST", "/api/v1/backups/run", run)
	call("PATCH", "/api/v1/backups/workspace", `{"ignore_uncommitted_git":true}`)
	if _, holds := call("GET", "/api/v1/backups/git-holds", ""); len(holds["items"].([]any)) != 0 {
		t.Fatalf("ignoring again releases holds: %v", holds)
	}
}
