package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/no-dal/ndl-ce/internal/agentrpc"
	"github.com/no-dal/ndl-ce/internal/appdb"
	"github.com/no-dal/ndl-ce/internal/auth"
	"github.com/no-dal/ndl-ce/internal/diskguard"
	"github.com/no-dal/ndl-ce/internal/rbac"
)

type fakeDisk struct {
	actions  []string
	category string
	protect  []string
}

func (f *fakeDisk) HostDisk(_ context.Context, action, category string, protect []string) (agentrpc.HostDiskResult, error) {
	f.actions = append(f.actions, action)
	f.category, f.protect = category, protect
	res := agentrpc.HostDiskResult{Status: diskguard.Status{Level: diskguard.LevelCritical}}
	if action == "cleanup" {
		res.Clean = &diskguard.CleanResult{Category: category, RemovedBytes: 1 << 30}
	}
	return res, nil
}

func TestHostDiskEndpoints(t *testing.T) {
	s, mem, token := testServer(t)
	ctx := context.Background()
	disk := &fakeDisk{}
	s.Disk = disk
	cluster, _ := mem.GetCluster(ctx)
	runID := uuid.NewString()
	if err := mem.CreateBackupRun(ctx, appdb.BackupRun{ID: runID, ClusterID: cluster.ID, Status: "running", StartedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)

	code, body := storageCall(t, ts, cookie, "GET", "/host/disk", "")
	if code != http.StatusOK || body["status"].(map[string]any)["level"] != "critical" {
		t.Fatalf("%d %v", code, body)
	}
	if code, _ := storageCall(t, ts, cookie, "POST", "/host/disk/cleanup", `{"category":"backup-staging"}`); code != http.StatusUnprocessableEntity {
		t.Fatalf("cleanup without confirmation must be refused, got %d", code)
	}
	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/host/disk/cleanup", strings.NewReader(`{"category":"backup-staging"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(confirmHeader, cleanupDiskConfirm)
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, err := ts.Client().Do(req)
	if err != nil || res.StatusCode != http.StatusOK {
		t.Fatalf("cleanup: %v %v", res.StatusCode, err)
	}
	_ = res.Body.Close()
	if disk.category != "backup-staging" || len(disk.protect) != 1 || disk.protect[0] != runID {
		t.Fatalf("running backups must be protected from cleanup: %+v", disk)
	}
	if code, _ := storageCall(t, ts, cookie, "POST", "/host/disk/reserve/release", `{}`); code != http.StatusUnprocessableEntity {
		t.Fatalf("release without confirmation must be refused, got %d", code)
	}

	hash, _ := auth.HashPassword("password1")
	u := appdb.User{ID: uuid.NewString(), ClusterID: cluster.ID, Username: "view", PasswordHash: hash}
	_ = mem.CreateUser(ctx, u)
	_ = mem.BindRole(ctx, cluster.ID, u.ID, rbac.Viewer)
	login, _ := ts.Client().Post(ts.URL+"/api/v1/auth/login", "application/json", strings.NewReader(`{"username":"view","password":"password1"}`))
	var view string
	for _, c := range login.Cookies() {
		if c.Name == sessionCookie {
			view = c.Value
		}
	}
	_ = login.Body.Close()
	if code, _ := storageCall(t, ts, view, "GET", "/host/disk", ""); code != http.StatusOK {
		t.Fatalf("viewers may read disk status, got %d", code)
	}
	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/host/disk/cleanup", strings.NewReader(`{"category":"backup-staging"}`))
	req.Header.Set(confirmHeader, cleanupDiskConfirm)
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: view})
	res, _ = ts.Client().Do(req)
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("viewers must not clean up, got %d", res.StatusCode)
	}
	_ = res.Body.Close()
}
