package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/no-dal/ndl-ce/internal/appdb"
	"github.com/no-dal/ndl-ce/internal/backup"
	"github.com/no-dal/ndl-ce/internal/backuphost"
	"github.com/no-dal/ndl-ce/internal/platformbackup"
	"github.com/no-dal/ndl-ce/internal/qemu"
	"github.com/no-dal/ndl-ce/internal/storage"
)

// platformAgent answers the repository key and remote checks; points listed
// in missing are absent from the bucket.
type platformAgent struct {
	*fakeBackup
	mu      sync.Mutex
	missing map[string]bool
	deep    []bool
}

func (f *platformAgent) CopyBackup(ctx context.Context, action, src, dest string) (storage.CopyResult, error) {
	var req backuphost.Request
	_ = json.Unmarshal([]byte(dest), &req)
	switch action {
	case qemu.BackupV2Key:
		extra, _ := json.Marshal(backuphost.Result{Key: "00112233"})
		return storage.CopyResult{Extra: string(extra)}, nil
	case qemu.BackupV2RemoteCheck:
		f.mu.Lock()
		defer f.mu.Unlock()
		f.deep = append(f.deep, req.Deep)
		var out backuphost.Result
		for _, c := range req.Checks {
			ok := !f.missing[c.BackupID]
			out.RemoteChecks = append(out.RemoteChecks, backup.RemoteCheck{Namespace: c.Namespace, BackupID: c.BackupID, Present: ok, Deep: ok && req.Deep, Error: map[bool]string{false: "missing manifest"}[ok]})
		}
		extra, _ := json.Marshal(out)
		return storage.CopyResult{Extra: string(extra)}, nil
	}
	return f.fakeBackup.CopyBackup(ctx, action, src, dest)
}

func TestPlatformBackupIsUnlockedOnlyByOwnerPasswords(t *testing.T) {
	s, mem, token := testServer(t)
	ctx := context.Background()
	cluster, _ := mem.GetCluster(ctx)
	bucket := platformbackup.NewMemoryStore()
	s.Platform.Store = func(appdb.BackupTarget) platformbackup.Store { return bucket }
	s.Platform.Dump = func(context.Context) ([]byte, error) { return []byte("PGDMP database"), nil }
	s.Platform.ConfigDir = t.TempDir()
	if err := os.WriteFile(filepath.Join(s.Platform.ConfigDir, "control.env"), []byte("NODAL_DSN=x"), 0o600); err != nil {
		t.Fatal(err)
	}
	agent := &platformAgent{fakeBackup: &fakeBackup{}, missing: map[string]bool{}}
	s.Backup = agent
	if err := mem.CreateBackupTarget(ctx, appdb.BackupTarget{ID: uuid.NewString(), ClusterID: cluster.ID, Name: "r2", Kind: "r2", Bucket: "b", Endpoint: "https://x.r2.cloudflarestorage.com", CreatedAt: time.Now()}, "secret", ""); err != nil {
		t.Fatal(err)
	}
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

	// Claiming setup made the key and published the Owner's envelope.
	_, st := call("GET", "/api/v1/platform-backup", "")
	if ready, _ := st["owners_ready"].([]any); len(ready) != 1 {
		t.Fatalf("the Owner who set up must be ready: %v", st)
	}
	ptr, _ := platformbackup.LoadPointer(ctx, bucket)
	if ptr == nil || ptr.Envelope == "" {
		t.Fatal("envelope must be published")
	}
	for k, v := range bucket.Objs {
		if strings.Contains(string(v), "admin") || strings.Contains(string(v), "PGDMP") {
			t.Fatalf("%s holds plaintext", k)
		}
	}

	// A covered workload with a protected remote restore point.
	wl := appdb.Workload{ID: uuid.NewString(), ClusterID: cluster.ID, Name: "app", Kind: "system-container"}
	_ = mem.CreateWorkload(ctx, wl)
	targets, _ := mem.ListBackupTargets(ctx, cluster.ID)
	_ = mem.CreateBackupPolicy(ctx, appdb.BackupPolicy{ID: uuid.NewString(), ClusterID: cluster.ID, Name: "p", Scope: appdb.BackupScopeAll, TargetID: targets[0].ID, Schedule: appdb.BackupNightly})
	run := appdb.BackupRun{ID: uuid.NewString(), ClusterID: cluster.ID, TargetID: targets[0].ID, WorkloadID: wl.ID, Status: appdb.BackupSucceeded, StartedAt: time.Now()}
	_ = mem.CreateBackupRun(ctx, run)
	_ = mem.CreateBackupArtifact(ctx, appdb.BackupArtifact{ID: uuid.NewString(), ClusterID: cluster.ID, RunID: run.ID, WorkloadID: wl.ID, Format: backup.Format, BackupID: "bk1", Namespace: "app-ns", RemoteState: string(backup.RemoteProtected), CreatedAt: time.Now()})

	code, st := call("POST", "/api/v1/platform-backup/run", "")
	if code != http.StatusOK {
		t.Fatalf("run %d %v", code, st)
	}
	health := st["health"].(map[string]any)
	if health["total"] != float64(1) || health["recoverable"] != float64(1) {
		t.Fatalf("1 of 1 recoverable: %v", health)
	}
	if !agent.deep[len(agent.deep)-1] {
		t.Fatal("the first check reads restore points back deeply")
	}

	// Recovery reads it back with only the bucket and the Owner's password.
	loc, err := platformbackup.Locate(ctx, bucket)
	if err != nil {
		t.Fatal(err)
	}
	env, _ := platformbackup.LoadEnvelope(ctx, bucket, loc.Envelope)
	if _, err := env.Unlock("admin", "wrong-password"); err == nil {
		t.Fatal("a wrong password must not unlock")
	}
	key, err := env.Unlock("admin", "correct-horse")
	if err != nil {
		t.Fatal(err)
	}
	plain, err := platformbackup.FetchSnapshot(ctx, bucket, key, *loc.Latest(env.KeyID))
	if err != nil {
		t.Fatal(err)
	}
	man, files, err := platformbackup.ReadArchive(plain)
	if err != nil {
		t.Fatal(err)
	}
	if string(files[platformbackup.DatabaseFile]) != "PGDMP database" || string(files[platformbackup.RepoKeyFile]) != "00112233" || string(files["etc/ndl/control.env"]) != "NODAL_DSN=x" {
		t.Fatalf("archive files %v", files)
	}
	if man.SchemaLevel != schemaLevel() || len(man.Workloads) != 1 || !man.Workloads[0].Protected || man.Workloads[0].Points[0].BackupID != "bk1" {
		t.Fatalf("manifest %+v", man)
	}

	// A restore point gone from the bucket is reported and alerted.
	agent.missing["bk1"] = true
	_, st = call("POST", "/api/v1/platform-backup/verify", `{"deep":false}`)
	if h := st["health"].(map[string]any); h["missing"] != float64(1) || h["recoverable"] != float64(0) {
		t.Fatalf("missing point %v", h)
	}
	events, _ := mem.ListEvents(ctx, cluster.ID, 100)
	alerted := false
	for _, e := range events {
		alerted = alerted || e.Type == "platform.recovery.degraded"
	}
	if !alerted {
		t.Fatal("losing a recoverable workload must alert")
	}

	// Rotation needs the password, and the old key's snapshot goes once the
	// new one is verified.
	if code, _ := call("POST", "/api/v1/platform-backup/rotate", `{"password":"nope"}`); code != http.StatusUnauthorized {
		t.Fatalf("rotate with a wrong password %d", code)
	}
	if code, st := call("POST", "/api/v1/platform-backup/rotate", `{"password":"correct-horse"}`); code != http.StatusOK {
		t.Fatalf("rotate %d %v", code, st)
	}
	ptr, _ = platformbackup.LoadPointer(ctx, bucket)
	newEnv, _ := platformbackup.LoadEnvelope(ctx, bucket, ptr.Envelope)
	if newEnv.KeyID == env.KeyID || len(ptr.Snapshots) != 1 || ptr.Snapshots[0].KeyID != newEnv.KeyID {
		t.Fatalf("rotation %+v", ptr)
	}
	if _, err := newEnv.Unlock("admin", "correct-horse"); err != nil {
		t.Fatal(err)
	}
	if ptr.PreviousEnvelope != loc.Envelope {
		t.Fatal("the last working envelope is kept as the fallback")
	}
}

func TestPasswordChangeLeavesNoEnvelopeWithTheOldPassword(t *testing.T) {
	s, mem, token := testServer(t)
	ctx := context.Background()
	cluster, _ := mem.GetCluster(ctx)
	bucket := platformbackup.NewMemoryStore()
	s.Platform.Store = func(appdb.BackupTarget) platformbackup.Store { return bucket }
	_ = mem.CreateBackupTarget(ctx, appdb.BackupTarget{ID: uuid.NewString(), ClusterID: cluster.ID, Name: "r2", Kind: "r2", Bucket: "b", CreatedAt: time.Now()}, "secret", "")
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	claimAdmin(t, ts, token)
	user, _ := mem.GetUserByName(ctx, cluster.ID, "admin")
	s.platformOwnerSignedIn(cluster.ID, *user, "a-new-password")
	ptr, _ := platformbackup.LoadPointer(ctx, bucket)
	for _, key := range []string{ptr.Envelope, ptr.PreviousEnvelope} {
		env, err := platformbackup.LoadEnvelope(ctx, bucket, key)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := env.Unlock("admin", "correct-horse"); err == nil {
			t.Fatalf("%s still opens with the old password", key)
		}
		if _, err := env.Unlock("admin", "a-new-password"); err != nil {
			t.Fatalf("%s: %v", key, err)
		}
	}
}
