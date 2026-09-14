package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/no-dal/ndl-ce/internal/appdb"
	"github.com/no-dal/ndl-ce/internal/backup"
	"github.com/no-dal/ndl-ce/internal/backuphost"
	"github.com/no-dal/ndl-ce/internal/qemu"
	"github.com/no-dal/ndl-ce/internal/storage"
)

type statusPointsBackup struct {
	fakeBackup
	points []backuphost.PointView
}

func (s *statusPointsBackup) CopyBackup(ctx context.Context, action, src, dest string) (storage.CopyResult, error) {
	if action == qemu.BackupV2Status || action == qemu.BackupV2Workspace {
		extra, _ := json.Marshal(backuphost.Result{Points: s.points})
		return storage.CopyResult{Format: "ndl-cab", Extra: string(extra)}, nil
	}
	return s.fakeBackup.CopyBackup(ctx, action, src, dest)
}

func TestRestorePointsPickUpProtectedAfterAsyncUpload(t *testing.T) {
	s, mem, token := testServer(t)
	cluster, _ := mem.GetCluster(context.Background())
	wl := uuid.NewString()
	backupID := "cab-" + uuid.NewString()
	artID := uuid.NewString()
	_ = mem.CreateBackupArtifact(context.Background(), appdb.BackupArtifact{
		ID: artID, ClusterID: cluster.ID, WorkloadID: wl, BackupID: backupID,
		RemoteState: string(backup.RemoteQueued), LocalComplete: true, Format: "ndl-cab",
	})
	_ = mem.UpsertBackupRestorePoint(context.Background(), appdb.BackupRestorePoint{
		ID: uuid.NewString(), ClusterID: cluster.ID, ArtifactID: artID, WorkloadID: wl,
		BackupID: backupID, Namespace: "ns", LocalComplete: true, RemoteState: string(backup.RemoteQueued),
	})
	s.Backup = &statusPointsBackup{points: []backuphost.PointView{{
		BackupID: backupID, Namespace: "ns", WorkloadID: wl, LocalComplete: true, Remote: backup.RemoteProtected,
	}}}
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)
	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/v1/backups/restore-points", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status %d", res.StatusCode)
	}
	var body struct {
		Items []map[string]any `json:"items"`
	}
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, item := range body.Items {
		if item["backup_id"] == backupID {
			found = true
			if item["remote_state"] != string(backup.RemoteProtected) {
				t.Fatalf("restore point stayed %v after upload completed", item["remote_state"])
			}
		}
	}
	if !found {
		t.Fatal("restore point missing")
	}
}
