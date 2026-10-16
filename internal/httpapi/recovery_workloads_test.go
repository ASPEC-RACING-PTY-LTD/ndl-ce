package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/no-dal/ndl-ce/internal/appdb"
	"github.com/no-dal/ndl-ce/internal/backup"
	"github.com/no-dal/ndl-ce/internal/backuphost"
	"github.com/no-dal/ndl-ce/internal/lxc"
	"github.com/no-dal/ndl-ce/internal/qemu"
	"github.com/no-dal/ndl-ce/internal/storage"
)

type stepLog struct {
	mu    sync.Mutex
	steps []string
}

func (l *stepLog) add(s string) {
	l.mu.Lock()
	l.steps = append(l.steps, s)
	l.mu.Unlock()
}

func (l *stepLog) all() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.steps...)
}

type orderWorkloads struct {
	*fakeWorkloads
	log  *stepLog
	spec lxc.Spec
}

func (f *orderWorkloads) CreateCT(ctx context.Context, spec lxc.Spec) (lxc.Result, error) {
	f.log.add("create")
	f.spec = spec
	return f.fakeWorkloads.CreateCT(ctx, spec)
}

func (f *orderWorkloads) LifecycleCT(ctx context.Context, req lxc.LifecycleRequest) (lxc.Result, error) {
	f.log.add("ct:" + req.Action)
	return f.fakeWorkloads.LifecycleCT(ctx, req)
}

type orderBackup struct {
	*fakeBackup
	log       *stepLog
	attention bool
	req       backuphost.Request
}

func (f *orderBackup) CopyBackup(ctx context.Context, action, src, dest string) (storage.CopyResult, error) {
	switch action {
	case qemu.BackupV2Restore:
		f.log.add("restore")
		return storage.CopyResult{Extra: "{}"}, nil
	case qemu.BackupV2Rehydrate:
		f.log.add("rehydrate")
		_ = json.Unmarshal([]byte(dest), &f.req)
		rh := &backuphost.RehydrateResult{Repos: []backuphost.RepoRestore{{Path: "/srv/app", Status: backuphost.RepoCloned}}}
		if f.attention && len(f.req.UseTags) == 0 {
			rh.NeedsAttention = true
			rh.Images = []backuphost.ImageRestore{{Ref: "redis:7", Status: backuphost.ImageNeedsTagApproval}}
		}
		extra, _ := json.Marshal(backuphost.Result{Rehydrate: rh})
		return storage.CopyResult{Extra: string(extra)}, nil
	}
	return f.fakeBackup.CopyBackup(ctx, action, src, dest)
}

func TestRecoveredWorkloadsComeBackOnTheirBaseImageThenData(t *testing.T) {
	s, mem, token := testServer(t)
	ctx := context.Background()
	cluster, _ := mem.GetCluster(ctx)
	log := &stepLog{}
	s.Workloads = &orderWorkloads{fakeWorkloads: &fakeWorkloads{}, log: log}
	agent := &orderBackup{fakeBackup: &fakeBackup{}, log: log, attention: true}
	s.Backup = agent
	s.Storage = fakeStorage{vol: storage.CreateVolumeResult{}}
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)

	node := uuid.NewString()
	_ = mem.UpsertNode(ctx, appdb.Node{ID: node, ClusterID: cluster.ID, Name: "local"})
	poolID, netID := seedCompute(t, mem, cluster.ID, node)
	poolRoot := t.TempDir()
	if _, err := mem.RemapRecoveredHost(ctx, cluster.ID, appdb.HostRemap{Node: appdb.Node{ID: node, Name: "local"}, Pools: map[string]string{poolID: poolRoot}}); err != nil {
		t.Fatal(err)
	}
	wl := appdb.Workload{ID: uuid.NewString(), ClusterID: cluster.ID, NodeID: node, Name: "app", Kind: lxc.KindSystemContainer, DesiredPower: "stopped", ImagePin: "debian/13/amd64/default", CPUs: 2, MemoryBytes: 1 << 30}
	_ = mem.CreateWorkload(ctx, wl)
	volID := uuid.NewString()
	_ = mem.CreateVolume(ctx, appdb.Volume{ID: volID, ClusterID: cluster.ID, NodeID: node, PoolID: poolID, Class: storage.ClassContainerRoot, Kind: storage.KindFilesystem,
		Format: storage.FormatDirectory, Status: storage.StatusUnavailable, BackendType: storage.BackendDirectory, BackendRef: "volumes/container-root/" + volID})
	_ = mem.CreateWorkloadDisk(ctx, appdb.WorkloadDisk{ID: uuid.NewString(), ClusterID: cluster.ID, WorkloadID: wl.ID, VolumeID: volID, Role: "root"})
	_ = mem.CreateWorkloadNIC(ctx, appdb.WorkloadNIC{ID: uuid.NewString(), ClusterID: cluster.ID, WorkloadID: wl.ID, NetworkID: netID})
	tgt := uuid.NewString()
	_ = mem.CreateBackupTarget(ctx, appdb.BackupTarget{ID: tgt, ClusterID: cluster.ID, Name: "r2", Kind: "r2", Bucket: "b", CreatedAt: time.Now()}, "k", "")
	run := appdb.BackupRun{ID: uuid.NewString(), ClusterID: cluster.ID, TargetID: tgt, WorkloadID: wl.ID, Status: appdb.BackupSucceeded, StartedAt: time.Now()}
	_ = mem.CreateBackupRun(ctx, run)
	bp, _ := json.Marshal(backup.Blueprint{CaptureMode: "smart", Repos: []backup.GitRepo{{Path: "/srv/app", Commit: "abc", Unpushed: true, UnpushedCommits: 2, Changed: []string{"x.go"}}}})
	_ = mem.CreateBackupArtifact(ctx, appdb.BackupArtifact{ID: uuid.NewString(), ClusterID: cluster.ID, RunID: run.ID, WorkloadID: wl.ID, Format: backup.Format,
		BackupID: "bk", Namespace: "ns", RemoteState: string(backup.RemoteProtected), CaptureMode: "smart", BlueprintJSON: string(bp), CreatedAt: time.Now()})
	st := s.loadPlatformState(ctx, cluster.ID)
	st.Recovery = &recoveryState{ConnectionVerified: true, Power: map[string]string{wl.ID: "running"}}
	s.savePlatformState(ctx, cluster.ID, st)

	_, rec := jsonCall(t, ts, cookie, "GET", "/api/v1/recovery", "")
	items := rec["workloads"].([]any)
	lost := items[0].(map[string]any)["not_backed_up"].([]any)
	if len(lost) != 1 || lost[0].(map[string]any)["unpushed_commits"] != float64(2) {
		t.Fatalf("what will not come back is shown before restoring: %v", items[0])
	}
	wait := func(want string) map[string]any {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			_, rec := jsonCall(t, ts, cookie, "GET", "/api/v1/recovery", "")
			r := rec["workloads"].([]any)[0].(map[string]any)["restore"].(map[string]any)
			if r["status"] == want {
				return r
			}
			time.Sleep(20 * time.Millisecond)
		}
		t.Fatalf("restore never became %s", want)
		return nil
	}
	if code, _ := jsonCall(t, ts, cookie, "POST", "/api/v1/recovery/workloads/all/restore", "{}"); code != http.StatusAccepted {
		t.Fatalf("restore all %d", code)
	}
	wait(restoreNeedsAttention)
	got := log.all()
	want := []string{"create", "restore", "ct:start", "rehydrate"}
	if len(got) != len(want) {
		t.Fatalf("steps %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("Smart restores go on the base image: %v", got)
		}
	}
	ow := s.Workloads.(*orderWorkloads)
	if ow.spec.SkipImage || ow.spec.RootfsPath != filepath.Join(poolRoot, "volumes/container-root", volID) || ow.spec.BridgeName == "" || ow.spec.CPUs != 2 {
		t.Fatalf("spec %+v", ow.spec)
	}

	// The Owner approves the tag: only rehydration runs again.
	if code, _ := jsonCall(t, ts, cookie, "POST", "/api/v1/recovery/workloads/"+wl.ID+"/restore", `{"use_tags":["redis:7"]}`); code != http.StatusAccepted {
		t.Fatal("retry")
	}
	wait(restoreDone)
	got = log.all()[4:]
	if len(got) != 2 || got[0] != "ct:start" || got[1] != "rehydrate" || agent.req.UseTags[0] != "redis:7" {
		t.Fatalf("resume runs rehydration only: %v", got)
	}
	cur, _ := mem.GetWorkload(ctx, cluster.ID, wl.ID)
	if cur.DesiredPower != "running" {
		t.Fatalf("power comes back: %s", cur.DesiredPower)
	}
}
