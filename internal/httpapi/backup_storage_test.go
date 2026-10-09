package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/no-dal/ndl-ce/internal/appdb"
	"github.com/no-dal/ndl-ce/internal/backup"
	"github.com/no-dal/ndl-ce/internal/backuphost"
	"github.com/no-dal/ndl-ce/internal/backupscope"
	"github.com/no-dal/ndl-ce/internal/qemu"
	"github.com/no-dal/ndl-ce/internal/storage"
)

func TestRetainBackupIDsUsesRotationNotConsecutiveDays(t *testing.T) {
	// Nightly artifacts ending Thursday 8 October 2026. Keep 1/1/1 used to
	// keep 8, 7 and 6 October, so a weekly or monthly point never existed.
	base := time.Date(2026, 10, 8, 19, 30, 0, 0, time.UTC)
	var arts []appdb.BackupArtifact
	for i := 0; i < 40; i++ {
		arts = append(arts, appdb.BackupArtifact{ID: "a" + itoa(i), CreatedAt: base.AddDate(0, 0, -i)})
	}
	keep := retainBackupIDs(arts, 1, 1, 1)
	for _, id := range []string{"a0", "a4", "a8"} {
		if _, ok := keep[id]; !ok {
			t.Fatalf("expected %s kept, kept %v", id, keep)
		}
	}
	if len(keep) != 3 {
		t.Fatalf("kept %v", keep)
	}
	// Three legacy whole-disk copies in one week (rsdw in R2): only the
	// newest is kept, instead of all three.
	week := []appdb.BackupArtifact{
		{ID: "n", CreatedAt: time.Date(2026, 10, 4, 23, 54, 0, 0, time.UTC)},
		{ID: "m", CreatedAt: time.Date(2026, 10, 4, 0, 32, 0, 0, time.UTC)},
		{ID: "o", CreatedAt: time.Date(2026, 10, 3, 1, 54, 0, 0, time.UTC)},
	}
	keep = retainBackupIDs(week, 1, 1, 1)
	if _, ok := keep["n"]; !ok || len(keep) != 1 {
		t.Fatalf("kept %v, want only the newest", keep)
	}
}

// expireBackup records expire requests and fails them while failing is set.
type expireBackup struct {
	fakeBackup
	failing  bool
	expires  []backuphost.Request
	gcCalls  int
	points   []backuphost.PointView
	lastGCOK bool
}

func (e *expireBackup) CopyBackup(ctx context.Context, action, src, dest string) (storage.CopyResult, error) {
	switch action {
	case qemu.BackupV2Expire:
		var req backuphost.Request
		_ = json.Unmarshal([]byte(dest), &req)
		e.expires = append(e.expires, req)
		if e.failing {
			return storage.CopyResult{}, errors.New("agent unreachable")
		}
		return storage.CopyResult{Format: backup.Format, Extra: "{}"}, nil
	case qemu.BackupV2GC:
		e.gcCalls++
		extra, _ := json.Marshal(backuphost.Result{GC: &backuphost.GCReport{Reason: "test", Local: backup.GCStats{BytesFreed: 1 << 30}}})
		return storage.CopyResult{Format: backup.Format, Extra: string(extra)}, nil
	case qemu.BackupV2Status, qemu.BackupV2Workspace:
		extra, _ := json.Marshal(backuphost.Result{Points: e.points})
		return storage.CopyResult{Format: backup.Format, Extra: string(extra)}, nil
	}
	return e.fakeBackup.CopyBackup(ctx, action, src, dest)
}

func seedV2Artifacts(t *testing.T, mem *appdb.Memory, clusterID, workloadID, targetID string, times []time.Time) []string {
	t.Helper()
	ctx := context.Background()
	var ids []string
	for _, at := range times {
		runID := uuid.NewString()
		if err := mem.CreateBackupRun(ctx, appdb.BackupRun{
			ID: runID, ClusterID: clusterID, TargetID: targetID, WorkloadID: workloadID,
			Status: appdb.BackupSucceeded, StartedAt: at,
		}); err != nil {
			t.Fatal(err)
		}
		id := uuid.NewString()
		if err := mem.CreateBackupArtifact(ctx, appdb.BackupArtifact{
			ID: id, ClusterID: clusterID, RunID: runID, WorkloadID: workloadID, Format: backup.Format,
			BackupID: "bk-" + id, Namespace: "ns", CreatedAt: at, Locator: "ndl-cab://x",
		}); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	return ids
}

func TestPruneKeepsArtifactRowsWhenExpireFails(t *testing.T) {
	s, mem, _ := testServer(t)
	ctx := context.Background()
	cluster, _ := mem.GetCluster(ctx)
	targetID := uuid.NewString()
	if err := mem.CreateBackupTarget(ctx, appdb.BackupTarget{ID: targetID, ClusterID: cluster.ID, Name: "r2", Kind: "r2", Bucket: "ndl-ce", Status: appdb.BackupAvailable}, "secret", ""); err != nil {
		t.Fatal(err)
	}
	wl := uuid.NewString()
	base := time.Date(2026, 10, 8, 19, 0, 0, 0, time.UTC)
	seedV2Artifacts(t, mem, cluster.ID, wl, targetID, []time.Time{base, base.AddDate(0, 0, -1), base.AddDate(0, 0, -2)})
	fake := &expireBackup{failing: true}
	s.Backup = fake
	pol := appdb.BackupPolicy{KeepDaily: 1, KeepWeekly: 1, KeepMonthly: 1}

	res := s.pruneBackupArtifacts(ctx, cluster.ID, wl, targetID, pol)
	if res.Failed != 2 || res.Expired != 0 {
		t.Fatalf("prune result %+v", res)
	}
	arts, _ := mem.ListBackupArtifactsForWorkload(ctx, cluster.ID, wl, targetID)
	if len(arts) != 3 {
		t.Fatalf("rows must stay when their data was not removed, have %d", len(arts))
	}
	failed := 0
	for _, ev := range s.recentStorageEvents() {
		if !ev.OK && ev.Kind == "retention" {
			failed++
		}
	}
	if failed != 2 {
		t.Fatalf("each failed removal must be recorded, got %d", failed)
	}

	fake.failing = false
	fake.expires = nil
	res = s.pruneBackupArtifacts(ctx, cluster.ID, wl, targetID, pol)
	if res.Expired != 2 || res.V2 != 2 {
		t.Fatalf("retry result %+v", res)
	}
	arts, _ = mem.ListBackupArtifactsForWorkload(ctx, cluster.ID, wl, targetID)
	if len(arts) != 1 || !arts[0].CreatedAt.Equal(base) {
		t.Fatalf("only the newest artifact may remain: %+v", arts)
	}
	for _, req := range fake.expires {
		if !req.DeferGC || req.Target.ID != targetID || req.Target.Bucket != "ndl-ce" {
			t.Fatalf("expire must name the target so the remote copy is removed, and defer GC: %+v", req)
		}
	}
}

func TestBackupMaintenanceRecordsOutcome(t *testing.T) {
	s, _, _ := testServer(t)
	fake := &expireBackup{}
	s.Backup = fake
	rep, err := s.runBackupMaintenance(context.Background(), "test")
	if err != nil || rep == nil || fake.gcCalls != 1 {
		t.Fatalf("maintenance %+v %v calls=%d", rep, err, fake.gcCalls)
	}
	evs := s.recentStorageEvents()
	if len(evs) == 0 || !evs[0].OK || !strings.Contains(evs[0].Message, "1.0 GiB") {
		t.Fatalf("events %+v", evs)
	}
}

func TestStoredStatsCapScopeDetail(t *testing.T) {
	var out backuphost.Result
	out.BackupID = "b"
	for i := 0; i < 50000; i++ {
		p := "/var/lib/app/file-" + itoa(i)
		out.Blueprint.Includes = append(out.Blueprint.Includes, p)
		out.Preview.Includes = append(out.Preview.Includes, p)
		out.Preview.Items = append(out.Preview.Items, backupscope.Item{ID: itoa(i), Paths: []string{p}, Bytes: int64(i)})
	}
	stats, bp := v2StatsJSON(out)
	if len(stats) > 256<<10 || len(bp) > 64<<10 {
		t.Fatalf("stored stats %d bytes, blueprint %d bytes", len(stats), len(bp))
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(stats), &m); err != nil {
		t.Fatal(err)
	}
	cut, ok := m["scope_truncated"].(map[string]any)
	if !ok || cut["includes"].(float64) != 50000 || cut["items"].(float64) != 50000 {
		t.Fatalf("truncation must be recorded: %v", m["scope_truncated"])
	}
	var back backuphost.Result
	_ = json.Unmarshal([]byte(stats), &back)
	if back.BackupID != "b" || len(back.Preview.Items) != maxStoredScopeEntries || back.Preview.Items[0].Bytes != 49999 {
		t.Fatalf("the largest items are kept: %d items", len(back.Preview.Items))
	}
}

func TestCompactStoredBackupStatsShrinksExistingRows(t *testing.T) {
	s, mem, _ := testServer(t)
	ctx := context.Background()
	cluster, _ := mem.GetCluster(ctx)
	var out backuphost.Result
	for i := 0; i < 60000; i++ {
		out.Blueprint.Includes = append(out.Blueprint.Includes, "/srv/data/file-"+itoa(i))
	}
	raw, _ := json.Marshal(out)
	bp, _ := json.Marshal(out.Blueprint)
	id := uuid.NewString()
	_ = mem.CreateBackupArtifact(ctx, appdb.BackupArtifact{ID: id, ClusterID: cluster.ID, Format: backup.Format, StatsJSON: string(raw), BlueprintJSON: string(bp)})
	if n := s.CompactStoredBackupStats(ctx); n != 1 {
		t.Fatalf("compacted %d rows", n)
	}
	a, _ := mem.GetBackupArtifact(ctx, cluster.ID, id)
	if len(a.StatsJSON)+len(a.BlueprintJSON) > 128<<10 {
		t.Fatalf("row still %d bytes", len(a.StatsJSON)+len(a.BlueprintJSON))
	}
	if n := s.CompactStoredBackupStats(ctx); n != 0 {
		t.Fatal("compaction must be idempotent")
	}
}

func TestPreferredDefaultPoolAvoidsRootFilesystem(t *testing.T) {
	big, small := int64(3900<<30), int64(280<<30)
	pools := []appdb.StoragePool{
		{ID: "local", Name: "local", Status: storage.StatusWarning, Warnings: []string{storage.WarnRootFilesystem}, UsableBytes: &small},
		{ID: "down", Name: "down", Status: storage.StatusUnavailable, UsableBytes: &big},
		{ID: "zfs", Name: "storage", Status: storage.StatusAvailable, UsableBytes: &big},
	}
	if got := preferredDefaultPool(pools); got == nil || got.ID != "zfs" {
		t.Fatalf("expected the separate pool, got %+v", got)
	}
	if got := preferredDefaultPool(pools[:1]); got == nil || got.ID != "local" {
		t.Fatalf("a root pool is still used when it is the only one, got %+v", got)
	}
}

func TestBackupStorageReportFlagsUntrackedRestorePoints(t *testing.T) {
	s, mem, token := testServer(t)
	ctx := context.Background()
	cluster, _ := mem.GetCluster(ctx)
	_ = cluster
	s.Backup = &expireBackup{points: []backuphost.PointView{{
		BackupID: "lost", Namespace: "ns", WorkloadID: "w", LocalComplete: true, Recovered: true,
	}}}
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)
	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/v1/backups/storage", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status %d", res.StatusCode)
	}
	var body storageReport
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if len(body.Unmanaged) != 1 || body.Unmanaged[0]["backup_id"] != "lost" {
		t.Fatalf("unmanaged %+v", body.Unmanaged)
	}
	found := false
	for _, w := range body.Warnings {
		if strings.Contains(w, "no backup record") {
			found = true
		}
	}
	if !found {
		t.Fatalf("warnings %v", body.Warnings)
	}
}

func TestSweepStaleTempDirsOnlyRemovesOldControlStaging(t *testing.T) {
	dir := t.TempDir()
	old := time.Now().Add(-2 * time.Hour)
	mk := func(name string, when time.Time) string {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(p, 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(p, "disk.qcow2"), make([]byte, 1024), 0o640); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(p, when, when); err != nil {
			t.Fatal(err)
		}
		return p
	}
	stale := mk("ndl-backup-123", old)
	staleRestore := mk("ndl-restore-file-9", old)
	fresh := mk("ndl-backup-456", time.Now())
	other := mk("something-else", old)
	n, b := SweepStaleTempDirs(dir)
	if n != 2 || b != 2048 {
		t.Fatalf("removed %d dirs, %d bytes", n, b)
	}
	for _, p := range []string{stale, staleRestore} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Fatalf("%s must be removed", p)
		}
	}
	for _, p := range []string{fresh, other} {
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("%s must be kept", p)
		}
	}
}
