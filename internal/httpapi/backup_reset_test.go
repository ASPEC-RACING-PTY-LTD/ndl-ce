package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/no-dal/ndl-ce/internal/appdb"
	"github.com/no-dal/ndl-ce/internal/qemu"
	"github.com/no-dal/ndl-ce/internal/storage"
)

// resetRecorder notes the reset call without touching the disk.
type resetRecorder struct {
	fakeBackup
	reset bool
}

func (r *resetRecorder) CopyBackup(ctx context.Context, action, src, dest string) (storage.CopyResult, error) {
	if action == qemu.BackupV2Reset {
		r.mu.Lock()
		r.reset = true
		r.mu.Unlock()
		return storage.CopyResult{}, nil
	}
	return r.fakeBackup.CopyBackup(ctx, action, src, dest)
}

func TestHistoryDeleteAndFullBackupReset(t *testing.T) {
	s, mem, token := testServer(t)
	ctx := context.Background()
	cluster, _ := mem.GetCluster(ctx)
	fb := &resetRecorder{}
	s.Backup = fb
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)
	call := func(method, path string, hdr map[string]string) int {
		req, _ := http.NewRequest(method, ts.URL+path, strings.NewReader("{}"))
		req.Header.Set("Content-Type", "application/json")
		for k, v := range hdr {
			req.Header.Set(k, v)
		}
		req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
		res, err := ts.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = res.Body.Close()
		return res.StatusCode
	}
	tgt := appdb.BackupTarget{ID: uuid.NewString(), ClusterID: cluster.ID, Name: "r2", Kind: "local", Locator: t.TempDir()}
	if err := mem.CreateBackupTarget(ctx, tgt, "", ""); err != nil {
		t.Fatal(err)
	}
	pol := appdb.BackupPolicy{ID: uuid.NewString(), ClusterID: cluster.ID, Name: "p", Scope: appdb.BackupScopeAll, TargetID: tgt.ID, Schedule: appdb.BackupNightly, CreatedAt: s.now()}
	if err := mem.CreateBackupPolicy(ctx, pol); err != nil {
		t.Fatal(err)
	}
	now := s.now()
	mkRun := func() appdb.BackupRun {
		r := appdb.BackupRun{ID: uuid.NewString(), ClusterID: cluster.ID, PolicyID: pol.ID, TargetID: tgt.ID, WorkloadID: uuid.NewString(), Status: appdb.BackupSucceeded, StartedAt: now, FinishedAt: &now}
		if err := mem.CreateBackupRun(ctx, r); err != nil {
			t.Fatal(err)
		}
		return r
	}
	plain := mkRun()
	withPoint := mkRun()
	art := appdb.BackupArtifact{ID: uuid.NewString(), ClusterID: cluster.ID, RunID: withPoint.ID, WorkloadID: withPoint.WorkloadID, Format: "ndl-cab", CreatedAt: now}
	if err := mem.CreateBackupArtifact(ctx, art); err != nil {
		t.Fatal(err)
	}

	if code := call("DELETE", "/api/v1/backups/policies/"+pol.ID+"/runs", nil); code != http.StatusOK {
		t.Fatalf("history delete %d", code)
	}
	if r, _ := mem.GetBackupRun(ctx, cluster.ID, plain.ID); r != nil {
		t.Fatal("a run without a restore point must be deleted")
	}
	if r, _ := mem.GetBackupRun(ctx, cluster.ID, withPoint.ID); r == nil {
		t.Fatal("a run with a restore point must stay, or the restore point would lose its record")
	}

	if code := call("POST", "/api/v1/backups/reset", nil); code != http.StatusConflict {
		t.Fatalf("reset without confirmation must be refused, got %d", code)
	}
	if code := call("POST", "/api/v1/backups/reset", map[string]string{confirmHeader: backupResetConfirm}); code != http.StatusOK {
		t.Fatalf("reset %d", code)
	}
	runs, _ := mem.ListBackupRuns(ctx, cluster.ID)
	arts, _ := mem.ListBackupArtifacts(ctx, cluster.ID)
	if len(runs) != 0 || len(arts) != 0 {
		t.Fatalf("reset must clear runs and restore points: %d runs, %d points", len(runs), len(arts))
	}
	if p, _ := mem.GetBackupPolicy(ctx, cluster.ID, pol.ID); p == nil {
		t.Fatal("the policy must stay")
	}
	if tg, _ := mem.GetBackupTarget(ctx, cluster.ID, tgt.ID); tg == nil {
		t.Fatal("the target must stay")
	}
	fb.mu.Lock()
	reset := fb.reset
	fb.mu.Unlock()
	if !reset {
		t.Fatal("the agent must be asked to empty the repository")
	}
	if s.backupsPaused() != "" {
		t.Fatal("backups must run again after a reset")
	}
}
