package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/no-dal/ndl-ce/internal/appdb"
	"github.com/no-dal/ndl-ce/internal/backup"
	"github.com/no-dal/ndl-ce/internal/backuphost"
	"github.com/no-dal/ndl-ce/internal/qemu"
	"github.com/no-dal/ndl-ce/internal/storage"
)

// rootFSBackup reports every path as being on the host root disk.
type rootFSBackup struct{ expireBackup }

func (r *rootFSBackup) CopyBackup(ctx context.Context, action, src, dest string) (storage.CopyResult, error) {
	if action == qemu.BackupRootFS {
		return storage.CopyResult{Dest: dest, Size: 1}, nil
	}
	return r.expireBackup.CopyBackup(ctx, action, src, dest)
}

func seedPools(t *testing.T, mem *appdb.Memory, clusterID string) {
	t.Helper()
	small, big := int64(280<<30), int64(3700<<30)
	for _, p := range []appdb.StoragePool{
		{ID: "pool-local", ClusterID: clusterID, Name: "local", BackendType: storage.BackendDirectory, Status: storage.StatusWarning,
			RootPath: "/var/lib/ndl/storage/local", Warnings: []string{storage.WarnRootFilesystem}, UsableBytes: &small},
		{ID: "pool-zfs", ClusterID: clusterID, Name: "storage", BackendType: storage.BackendZFS, Status: storage.StatusAvailable,
			RootPath: "/var/lib/ndl/storage/zfs/123", UsableBytes: &big},
		{ID: "pool-lvm", ClusterID: clusterID, Name: "thin", BackendType: storage.BackendLVM, Status: storage.StatusAvailable, RootPath: "vg0"},
	} {
		if err := mem.CreateStoragePool(context.Background(), p); err != nil {
			t.Fatal(err)
		}
	}
}

func TestBackupLocationsRecommendSeparateDisk(t *testing.T) {
	s, mem, token := testServer(t)
	cluster, _ := mem.GetCluster(context.Background())
	seedPools(t, mem, cluster.ID)
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)
	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/v1/backups/locations", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var body struct {
		Items []backupLocation `json:"items"`
	}
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	by := map[string]backupLocation{}
	for _, l := range body.Items {
		by[l.PoolID] = l
	}
	if l := by["pool-zfs"]; !l.Recommended || !l.Usable || l.RepoPath != "/var/lib/ndl/storage/zfs/123/backup-repo" {
		t.Fatalf("zfs %+v", l)
	}
	if l := by["pool-local"]; l.Recommended || !l.RootFilesystem || !l.Usable {
		t.Fatalf("local %+v", l)
	}
	if l := by["pool-lvm"]; l.Usable || l.Reason == "" {
		t.Fatalf("lvm %+v", l)
	}
}

func TestLocalBackupPathChecks(t *testing.T) {
	s, mem, _ := testServer(t)
	ctx := context.Background()
	cluster, _ := mem.GetCluster(ctx)
	seedPools(t, mem, cluster.ID)
	_ = mem.CreateBackupTarget(ctx, appdb.BackupTarget{ID: "t1", ClusterID: cluster.ID, Name: "existing", Kind: appdb.BackupLocal, Locator: "/mnt/backups/a"}, "", "")
	s.Backup = &expireBackup{}
	for path, want := range map[string]string{
		"/var/lib/ndl/control/tmp/cert-g15-repo":       "reserved",
		"/var/lib/ndl/storage/local/volumes/vm-disk/x": "workload disks",
		"/var/lib/ndl/storage":                         "contains storage pool",
		"/mnt/backups/a/nested":                        "overlaps",
		"/mnt/backups":                                 "overlaps",
		"relative/path":                                "absolute",
	} {
		err := s.checkLocalBackupPath(ctx, cluster.ID, path, "", false)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("%s: want %q, got %v", path, want, err)
		}
	}
	if err := s.checkLocalBackupPath(ctx, cluster.ID, "/var/lib/ndl/storage/zfs/123/backups/r2", "", false); err != nil {
		t.Fatalf("a folder on the separate pool must be accepted: %v", err)
	}
	s.Backup = &rootFSBackup{}
	if err := s.checkLocalBackupPath(ctx, cluster.ID, "/srv/backups", "", false); err == nil || !strings.Contains(err.Error(), "root disk") {
		t.Fatalf("the root disk must need confirmation, got %v", err)
	}
	if err := s.checkLocalBackupPath(ctx, cluster.ID, "/srv/backups", "", true); err != nil {
		t.Fatalf("confirmed root disk must be accepted: %v", err)
	}
}

func TestCreateLocalTargetOnPool(t *testing.T) {
	s, mem, token := testServer(t)
	cluster, _ := mem.GetCluster(context.Background())
	seedPools(t, mem, cluster.ID)
	fake := &expireBackup{}
	s.Backup = fake
	fz := &fakeZFS{}
	s.ZFS = fz
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)
	post := func(body string) (int, map[string]any) {
		req, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/v1/backups/targets", strings.NewReader(body))
		req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
		req.Header.Set("Content-Type", "application/json")
		res, err := ts.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		var out map[string]any
		_ = json.NewDecoder(res.Body).Decode(&out)
		return res.StatusCode, out
	}
	code, out := post(`{"name":"Nightly Disk","kind":"local","pool_id":"pool-zfs"}`)
	if code != http.StatusCreated || out["locator"] != "/var/lib/ndl/storage/zfs/123/backups/nightly-disk" {
		t.Fatalf("create on pool: %d %v", code, out)
	}
	// A ZFS pool's own folder is not mounted: backups get their own dataset.
	if len(fz.calls) == 0 || fz.calls[len(fz.calls)-1].Action != "ensure-dataset" || fz.calls[len(fz.calls)-1].VolumeID != "ndl-backups" {
		t.Fatalf("a dedicated backup dataset must be prepared on a ZFS pool: %+v", fz.calls)
	}
	code, _ = post(`{"name":"bad","kind":"local","pool_id":"pool-lvm"}`)
	if code < 400 {
		t.Fatal("a block-only pool must be refused")
	}
	code, out = post(`{"name":"cert","kind":"local","locator":"/var/lib/ndl/control/tmp/cert-g15b-repo"}`)
	if code != http.StatusBadRequest {
		t.Fatalf("control state must be refused: %d %v", code, out)
	}
}

func TestProtectedBackupsSurviveRetention(t *testing.T) {
	s, mem, _ := testServer(t)
	ctx := context.Background()
	cluster, _ := mem.GetCluster(ctx)
	targetID := uuid.NewString()
	_ = mem.CreateBackupTarget(ctx, appdb.BackupTarget{ID: targetID, ClusterID: cluster.ID, Name: "local", Kind: appdb.BackupLocal, Locator: "/mnt/b"}, "", "")
	wl := uuid.NewString()
	base := time.Date(2026, 10, 8, 19, 0, 0, 0, time.UTC)
	ids := seedV2Artifacts(t, mem, cluster.ID, wl, targetID, []time.Time{base, base.AddDate(0, 0, -1), base.AddDate(0, 0, -2)})
	if err := mem.SetBackupArtifactProtected(ctx, cluster.ID, ids[2], true); err != nil {
		t.Fatal(err)
	}
	s.Backup = &expireBackup{}
	res := s.pruneBackupArtifacts(ctx, cluster.ID, wl, targetID, appdb.BackupPolicy{KeepDaily: 1})
	if res.Expired != 1 {
		t.Fatalf("result %+v", res)
	}
	arts, _ := mem.ListBackupArtifactsForWorkload(ctx, cluster.ID, wl, targetID)
	kept := map[string]bool{}
	for _, a := range arts {
		kept[a.ID] = true
	}
	if !kept[ids[0]] || !kept[ids[2]] || kept[ids[1]] {
		t.Fatalf("the newest and the protected backup must stay: %v", kept)
	}
}

func TestOffsiteRetentionRemovesOnlyRemoteCopies(t *testing.T) {
	s, mem, _ := testServer(t)
	ctx := context.Background()
	cluster, _ := mem.GetCluster(ctx)
	targetID := uuid.NewString()
	_ = mem.CreateBackupTarget(ctx, appdb.BackupTarget{ID: targetID, ClusterID: cluster.ID, Name: "r2", Kind: "r2", Bucket: "ndl-ce"}, "secret", "")
	polID := uuid.NewString()
	_ = mem.UpsertBackupPolicyOffsite(ctx, cluster.ID, polID, appdb.BackupOffsite{KeepDaily: 1})
	wl := uuid.NewString()
	base := time.Date(2026, 10, 8, 19, 0, 0, 0, time.UTC)
	ids := seedV2Artifacts(t, mem, cluster.ID, wl, targetID, []time.Time{base, base.AddDate(0, 0, -1), base.AddDate(0, 0, -2)})
	for _, id := range ids {
		a, _ := mem.GetBackupArtifact(ctx, cluster.ID, id)
		a.RemoteState = string(backup.RemoteProtected)
		_ = mem.UpdateBackupArtifact(ctx, *a)
	}
	fake := &expireBackup{}
	s.Backup = fake
	res := s.pruneBackupArtifacts(ctx, cluster.ID, wl, targetID, appdb.BackupPolicy{ID: polID, KeepDaily: 3})
	if res.Expired != 0 || res.Failed != 0 {
		t.Fatalf("local retention keeps all three: %+v", res)
	}
	remoteOnly := 0
	for _, req := range fake.expires {
		if !req.RemoteOnly {
			t.Fatalf("offsite retention must never delete local copies: %+v", req)
		}
		remoteOnly++
	}
	if remoteOnly != 2 {
		t.Fatalf("two older restore points lose their remote copy, got %d", remoteOnly)
	}
	newest, _ := mem.GetBackupArtifact(ctx, cluster.ID, ids[0])
	older, _ := mem.GetBackupArtifact(ctx, cluster.ID, ids[1])
	if newest.RemoteState != string(backup.RemoteProtected) || older.RemoteState != string(backup.RemoteNone) {
		t.Fatalf("remote states %q %q", newest.RemoteState, older.RemoteState)
	}
	_ = backuphost.ActionExpire
}

// wipeBackup answers a wipe and reports which restore points are local.
type wipeBackup struct {
	expireBackup
	wiped int
}

func (w *wipeBackup) CopyBackup(ctx context.Context, action, src, dest string) (storage.CopyResult, error) {
	if action == qemu.BackupV2WipeRemote {
		w.wiped++
		extra, _ := json.Marshal(backuphost.Result{
			Points: w.points, Wipe: &backuphost.WipeResult{ObjectsDeleted: 9, BytesDeleted: 35 << 30},
		})
		return storage.CopyResult{Extra: string(extra)}, nil
	}
	return w.expireBackup.CopyBackup(ctx, action, src, dest)
}

func TestWipeTargetNeedsNameAndResetsRecords(t *testing.T) {
	s, mem, token := testServer(t)
	ctx := context.Background()
	cluster, _ := mem.GetCluster(ctx)
	targetID := uuid.NewString()
	_ = mem.CreateBackupTarget(ctx, appdb.BackupTarget{ID: targetID, ClusterID: cluster.ID, Name: "R2", Kind: "r2", Bucket: "ndl-ce"}, "secret", "")
	wl := uuid.NewString()
	now := time.Date(2026, 10, 9, 19, 0, 0, 0, time.UTC)
	ids := seedV2Artifacts(t, mem, cluster.ID, wl, targetID, []time.Time{now, now.AddDate(0, 0, -1)})
	local, _ := mem.GetBackupArtifact(ctx, cluster.ID, ids[0])
	local.RemoteState = string(backup.RemoteProtected)
	_ = mem.UpdateBackupArtifact(ctx, *local)
	runID := uuid.NewString()
	_ = mem.CreateBackupRun(ctx, appdb.BackupRun{ID: runID, ClusterID: cluster.ID, TargetID: targetID, WorkloadID: wl, Status: appdb.BackupSucceeded, StartedAt: now})
	_ = mem.CreateBackupArtifact(ctx, appdb.BackupArtifact{ID: uuid.NewString(), ClusterID: cluster.ID, RunID: runID, WorkloadID: wl, Format: "qcow2", ObjectKey: "backups/rsdw/x", Locator: "s3://ndl-ce/backups/rsdw/x", CreatedAt: now})
	fake := &wipeBackup{}
	fake.points = []backuphost.PointView{{BackupID: local.BackupID, Namespace: "ns"}}
	s.Backup = fake
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)
	wipe := func(name string, confirm bool) int {
		req, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/v1/backups/targets/"+targetID+"/wipe?wait=true", strings.NewReader(`{"confirm_name":"`+name+`"}`))
		req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
		if confirm {
			req.Header.Set(confirmHeader, wipeTargetConfirm)
		}
		res, err := ts.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		return res.StatusCode
	}
	if code := wipe("R2", false); code != http.StatusConflict || fake.wiped != 0 {
		t.Fatalf("missing confirmation must be refused: %d", code)
	}
	if code := wipe("r2", true); code != http.StatusConflict || fake.wiped != 0 {
		t.Fatalf("a wrong name must be refused: %d", code)
	}
	if code := wipe("R2", true); code != http.StatusOK || fake.wiped != 1 {
		t.Fatalf("wipe %d", code)
	}
	arts, _ := mem.ListBackupArtifacts(ctx, cluster.ID)
	if len(arts) != 1 || arts[0].ID != ids[0] || arts[0].RemoteState != string(backup.RemoteNone) {
		t.Fatalf("only the locally held backup stays, as local-only: %+v", arts)
	}
}
