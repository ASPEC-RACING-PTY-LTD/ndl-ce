package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/no-dal/ndl-ce/internal/appdb"
	"github.com/no-dal/ndl-ce/internal/storage"
)

func TestPausingAPolicyStopsOnlyItsBackups(t *testing.T) {
	s, _, _ := testServer(t)
	mine, untrackMine, err := s.trackBackup(context.Background(), "wl-1", "p1")
	if err != nil {
		t.Fatal(err)
	}
	other, untrackOther, err := s.trackBackup(context.Background(), "wl-2", "p2")
	if err != nil {
		t.Fatal(err)
	}
	defer untrackOther()
	go func() {
		<-mine.Done()
		untrackMine()
	}()
	if n := s.stopPolicyBackups("p1"); n != 1 {
		t.Fatalf("one backup of p1 must be stopped, got %d", n)
	}
	if mine.Err() == nil || other.Err() != nil {
		t.Fatalf("only p1's backup may be cancelled: p1 %v, p2 %v", mine.Err(), other.Err())
	}
	if !s.policyStopped("p1") || s.policyStopped("p2") {
		t.Fatal("p1 must be marked stopped, p2 not")
	}
}

func TestPausedPolicyQueuesRunNowAndClearQueueDropsIt(t *testing.T) {
	s, mem, _ := testServer(t)
	ctx := context.Background()
	cluster, _ := mem.GetCluster(ctx)
	pol := appdb.BackupPolicy{
		ID: uuid.NewString(), ClusterID: cluster.ID, Name: "p", Scope: appdb.BackupScopeAll,
		TargetID: uuid.NewString(), Schedule: appdb.BackupNightly, Paused: true, CreatedAt: s.now(),
	}
	if err := mem.CreateBackupPolicy(ctx, pol); err != nil {
		t.Fatal(err)
	}
	if _, err := s.executePolicyBackups(ctx, cluster.ID, pol.ID); !errors.Is(err, errPolicyPaused) {
		t.Fatalf("a paused policy must not run: %v", err)
	}
	if err := mem.SetBackupPolicyRunQueued(ctx, cluster.ID, pol.ID, true); err != nil {
		t.Fatal(err)
	}
	stopped := s.recordPolicyPaused(ctx, cluster.ID, pol.ID, pol.TargetID, uuid.NewString())
	got, _ := mem.GetBackupPolicy(ctx, cluster.ID, pol.ID)
	if n := s.queuedRunCount(ctx, cluster.ID, *got); n != 2 {
		t.Fatalf("a queued Run now and one stopped backup wait: got %d", n)
	}
	// Editing the policy keeps its queue.
	got.Name = "renamed"
	if err := mem.UpdateBackupPolicy(ctx, *got); err != nil {
		t.Fatal(err)
	}
	if again, _ := mem.GetBackupPolicy(ctx, cluster.ID, pol.ID); !again.RunQueued {
		t.Fatal("an edit must not drop the queued run")
	}
	// Clear queue, as its handler does.
	_ = mem.SetBackupPolicyRunQueued(ctx, cluster.ID, pol.ID, false)
	for _, r := range s.pausedRuns(ctx, cluster.ID, pol.ID) {
		r.Error = policyClearedError
		_ = mem.UpdateBackupRun(ctx, r)
	}
	got, _ = mem.GetBackupPolicy(ctx, cluster.ID, pol.ID)
	if n := s.queuedRunCount(ctx, cluster.ID, *got); n != 0 {
		t.Fatalf("the queue must be empty after clearing, got %d", n)
	}
	if run, _ := mem.GetBackupRun(ctx, cluster.ID, stopped.ID); run.Error != policyClearedError {
		t.Fatalf("the stopped run must be marked cleared: %+v", run)
	}
}

func TestResumeRunsStoppedBackupsOnce(t *testing.T) {
	s, mem, _ := testServer(t)
	ctx := context.Background()
	cluster, _ := mem.GetCluster(ctx)
	pol := appdb.BackupPolicy{
		ID: uuid.NewString(), ClusterID: cluster.ID, Name: "p", Scope: appdb.BackupScopeAll,
		TargetID: uuid.NewString(), Schedule: appdb.BackupNightly, CreatedAt: s.now(),
	}
	if err := mem.CreateBackupPolicy(ctx, pol); err != nil {
		t.Fatal(err)
	}
	s.stopPolicyBackups(pol.ID)
	stopped := s.recordPolicyPaused(ctx, cluster.ID, pol.ID, pol.TargetID, uuid.NewString())
	s.resumePolicy(ctx, cluster.ID, pol.ID)
	if s.policyStopped(pol.ID) {
		t.Fatal("resume must lift the stop")
	}
	if run, _ := mem.GetBackupRun(ctx, cluster.ID, stopped.ID); run.Error != policyResumedError {
		t.Fatalf("the stopped run must be marked resumed so it runs once: %+v", run)
	}
	if n := len(s.pausedRuns(ctx, cluster.ID, pol.ID)); n != 0 {
		t.Fatalf("nothing may wait after resume, got %d", n)
	}
}

func TestPauseForceStopsARunningPolicyAndRunNowOnlyQueues(t *testing.T) {
	s, mem, token := testServer(t)
	cluster, _ := mem.GetCluster(context.Background())
	nodeID := uuid.NewString()
	_ = mem.UpsertNode(context.Background(), appdb.Node{ID: nodeID, ClusterID: cluster.ID, Name: "local"})
	poolID, netID := seedCompute(t, mem, cluster.ID, nodeID)
	s.Storage = fakeStorage{vol: storage.CreateVolumeResult{Handle: storage.VolumeHandle{
		BackendType: storage.BackendDirectory, BackendRef: "volumes/vm-disk/boot.qcow2",
		Kind: storage.KindBlock, Class: storage.ClassVMDisk, Format: storage.FormatQCOW2,
	}}}
	s.VM = &fakeVM{}
	gate := &gateBackup{started: make(chan struct{}), release: make(chan struct{})}
	s.Backup = gate
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)
	call := func(method, path, body string) (int, map[string]any) {
		req, _ := http.NewRequest(method, ts.URL+path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
		res, err := ts.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		var out map[string]any
		_ = json.NewDecoder(res.Body).Decode(&out)
		return res.StatusCode, out
	}
	_, vm := call("POST", "/api/v1/workloads", `{"name":"pz","kind":"vm","pool_id":"`+poolID+`","network_id":"`+netID+`"}`)
	_, tgt := call("POST", "/api/v1/backups/targets", `{"name":"local","kind":"local","locator":"`+t.TempDir()+`"}`)
	polBody := `{"name":"pz","scope":"selected","workload_ids":["` + vm["id"].(string) + `"],"target_id":"` + tgt["id"].(string) + `","schedule":"nightly","keep_daily":1,"keep_weekly":0,"keep_monthly":0`
	code, pol := call("POST", "/api/v1/backups/policies", polBody+`}`)
	if code != http.StatusCreated {
		t.Fatalf("policy %d %v", code, pol)
	}
	id := pol["id"].(string)

	first := make(chan int, 1)
	go func() {
		code, _ := call("POST", "/api/v1/backups/policies/"+id+"/run", "{}")
		first <- code
	}()
	select {
	case <-gate.started:
	case <-time.After(5 * time.Second):
		t.Fatal("the run did not start")
	}
	// Pausing stops the running capture by force.
	if code, _ := call("PATCH", "/api/v1/backups/policies/"+id, polBody+`,"paused":true}`); code != http.StatusOK {
		t.Fatalf("pause %d", code)
	}
	select {
	case <-first:
	case <-time.After(10 * time.Second):
		t.Fatal("pausing must stop the running backup")
	}
	runs, _ := mem.ListBackupRuns(context.Background(), cluster.ID)
	if len(runs) != 1 || runs[0].Status != appdb.BackupInterrupted || runs[0].Error != policyPausedError {
		t.Fatalf("the stopped backup must wait for resume: %+v", runs)
	}
	// Run now while paused only queues.
	code, out := call("POST", "/api/v1/backups/policies/"+id+"/run", "{}")
	if code != http.StatusAccepted || out["queued"] != true {
		t.Fatalf("run now while paused must queue: %d %v", code, out)
	}
	if runs, _ := mem.ListBackupRuns(context.Background(), cluster.ID); len(runs) != 1 {
		t.Fatalf("nothing may run while paused: %+v", runs)
	}
	_, list := call("GET", "/api/v1/backups/policies", "")
	if q := list["items"].([]any)[0].(map[string]any)["queued_runs"]; q != float64(2) {
		t.Fatalf("queued Run now and the stopped backup must show: %v", q)
	}
	code, out = call("POST", "/api/v1/backups/policies/"+id+"/queue/clear", "{}")
	if code != http.StatusOK || out["cleared"] != float64(2) {
		t.Fatalf("clear queue %d %v", code, out)
	}
	_, list = call("GET", "/api/v1/backups/policies", "")
	if q := list["items"].([]any)[0].(map[string]any)["queued_runs"]; q != float64(0) {
		t.Fatalf("queue must be empty: %v", q)
	}
}
