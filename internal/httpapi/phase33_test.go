package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/no-dal/ndl-ce/internal/appdb"
	"github.com/no-dal/ndl-ce/internal/qemu"
	"github.com/no-dal/ndl-ce/internal/storage"
)

func TestPhase33RestoreSourceDownOntoControl(t *testing.T) {
	s, mem, token := testServer(t)
	cluster, _ := mem.GetCluster(context.Background())
	control := seedNode(t, mem, cluster.ID, debianInv(), false)
	worker := appdb.Node{ID: uuid.NewString(), ClusterID: cluster.ID, Name: "box-a", Role: "worker", Hostname: "box-a"}
	if err := mem.UpsertNode(context.Background(), worker); err != nil {
		t.Fatal(err)
	}
	poolID, netID := seedCompute(t, mem, cluster.ID, control.ID)
	vm := &fakeVM{}
	bk := &fakeBackup{}
	s.Storage = fakeStorage{vol: storage.CreateVolumeResult{Handle: storage.VolumeHandle{
		BackendType: storage.BackendDirectory, BackendRef: "volumes/vm-disk/boot.qcow2",
		Kind: storage.KindBlock, Class: storage.ClassVMDisk, Format: storage.FormatQCOW2,
	}}}
	s.VM = vm
	s.Backup = bk
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)

	body := `{"name":"lost","kind":"vm","pool_id":"` + poolID + `","network_id":"` + netID + `","cpus":1,"memory_bytes":268435456}`
	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/workloads", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ := ts.Client().Do(req)
	if res.StatusCode != http.StatusCreated {
		b, _ := io.ReadAll(res.Body)
		t.Fatalf("create %d %s", res.StatusCode, b)
	}
	var created map[string]any
	_ = json.NewDecoder(res.Body).Decode(&created)
	_ = res.Body.Close()
	vmID := created["id"].(string)

	dir := t.TempDir()
	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/backups/targets", strings.NewReader(`{"name":"local-disk","kind":"local","locator":"`+dir+`","password":"secret-pass"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(req)
	if res.StatusCode != http.StatusCreated {
		b, _ := io.ReadAll(res.Body)
		t.Fatalf("target %d %s", res.StatusCode, b)
	}
	var tgt map[string]any
	_ = json.NewDecoder(res.Body).Decode(&tgt)
	_ = res.Body.Close()
	targetID := tgt["id"].(string)

	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/backups/run", strings.NewReader(`{"workload_id":"`+vmID+`","target_id":"`+targetID+`"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(req)
	if res.StatusCode != http.StatusAccepted {
		b, _ := io.ReadAll(res.Body)
		t.Fatalf("run %d %s", res.StatusCode, b)
	}
	_ = res.Body.Close()

	if _, err := mem.TransferWorkloadOwnership(context.Background(), cluster.ID, vmID, worker.ID, 0); err != nil {
		t.Fatal(err)
	}
	if err := mem.RevokeNode(context.Background(), cluster.ID, worker.ID, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}

	req, _ = http.NewRequest("GET", ts.URL+"/api/v1/backups/artifacts", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(req)
	var arts map[string]any
	_ = json.NewDecoder(res.Body).Decode(&arts)
	_ = res.Body.Close()
	art := arts["items"].([]any)[0].(map[string]any)
	artID := art["id"].(string)
	if art["locality"] != "local" {
		t.Fatalf("locality %+v", art)
	}

	startsBefore := 0
	for _, a := range vm.actions {
		if a == "start" {
			startsBefore++
		}
	}
	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/backups/artifacts/"+artID+"/restore", strings.NewReader(`{"mode":"new","target_node_id":"`+control.ID+`"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(req)
	raw, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusAccepted {
		t.Fatalf("restore %d %s", res.StatusCode, raw)
	}
	var restored map[string]any
	_ = json.Unmarshal(raw, &restored)
	newID, _ := restored["restored_workload_id"].(string)
	got, _ := mem.GetWorkload(context.Background(), cluster.ID, newID)
	if got == nil || got.NodeID != control.ID {
		t.Fatalf("restored onto control %+v", got)
	}
	if got.Status != qemu.StatusRunning {
		t.Fatalf("control restore must start locally %s", got.Status)
	}
	startsAfter := 0
	for _, a := range vm.actions {
		if a == "start" {
			startsAfter++
		}
	}
	if startsAfter <= startsBefore {
		t.Fatalf("control dest must start the restored guest: %v", vm.actions)
	}
}

func TestPhase33RestoreOntoWorkerDoesNotStartLocalCopy(t *testing.T) {
	s, mem, token := testServer(t)
	cluster, _ := mem.GetCluster(context.Background())
	control := seedNode(t, mem, cluster.ID, debianInv(), false)
	worker := appdb.Node{ID: uuid.NewString(), ClusterID: cluster.ID, Name: "box-b", Role: "worker"}
	if err := mem.UpsertNode(context.Background(), worker); err != nil {
		t.Fatal(err)
	}
	poolID, netID := seedCompute(t, mem, cluster.ID, control.ID)
	vm := &fakeVM{}
	bk := &fakeBackup{}
	s.Storage = fakeStorage{vol: storage.CreateVolumeResult{Handle: storage.VolumeHandle{
		BackendType: storage.BackendDirectory, BackendRef: "volumes/vm-disk/boot.qcow2",
		Kind: storage.KindBlock, Class: storage.ClassVMDisk, Format: storage.FormatQCOW2,
	}}}
	s.VM = vm
	s.Backup = bk
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)

	body := `{"name":"keep","kind":"vm","pool_id":"` + poolID + `","network_id":"` + netID + `","cpus":1,"memory_bytes":268435456}`
	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/workloads", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ := ts.Client().Do(req)
	if res.StatusCode != http.StatusCreated {
		b, _ := io.ReadAll(res.Body)
		t.Fatalf("create %d %s", res.StatusCode, b)
	}
	var created map[string]any
	_ = json.NewDecoder(res.Body).Decode(&created)
	_ = res.Body.Close()
	vmID := created["id"].(string)

	dir := t.TempDir()
	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/backups/targets", strings.NewReader(`{"name":"local-disk","kind":"local","locator":"`+dir+`"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(req)
	if res.StatusCode != http.StatusCreated {
		b, _ := io.ReadAll(res.Body)
		t.Fatalf("target %d %s", res.StatusCode, b)
	}
	var tgt map[string]any
	_ = json.NewDecoder(res.Body).Decode(&tgt)
	_ = res.Body.Close()

	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/backups/run", strings.NewReader(`{"workload_id":"`+vmID+`","target_id":"`+tgt["id"].(string)+`"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(req)
	if res.StatusCode != http.StatusAccepted {
		b, _ := io.ReadAll(res.Body)
		t.Fatalf("run %d %s", res.StatusCode, b)
	}
	_ = res.Body.Close()

	req, _ = http.NewRequest("GET", ts.URL+"/api/v1/backups/artifacts", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(req)
	var arts map[string]any
	_ = json.NewDecoder(res.Body).Decode(&arts)
	_ = res.Body.Close()
	artID := arts["items"].([]any)[0].(map[string]any)["id"].(string)

	copiesBefore := len(bk.copies)
	startsBefore := 0
	for _, a := range vm.actions {
		if a == "start" {
			startsBefore++
		}
	}
	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/backups/artifacts/"+artID+"/restore", strings.NewReader(`{"mode":"new","target_node_id":"`+worker.ID+`"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(req)
	raw, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusAccepted {
		t.Fatalf("restore worker %d %s", res.StatusCode, raw)
	}
	var restored map[string]any
	_ = json.Unmarshal(raw, &restored)
	newID, _ := restored["restored_workload_id"].(string)
	got, _ := mem.GetWorkload(context.Background(), cluster.ID, newID)
	if got == nil || got.NodeID != worker.ID {
		t.Fatalf("must record dest worker %+v", got)
	}
	if got.Status != "unavailable" || !strings.Contains(got.Reason, "dest agent is not connected") {
		t.Fatalf("worker restore must not start locally %+v", got)
	}
	startsAfter := 0
	for _, a := range vm.actions {
		if a == "start" {
			startsAfter++
		}
	}
	if startsAfter != startsBefore {
		t.Fatalf("must not start a second copy on the control unix agent: %v", vm.actions)
	}
	if len(bk.copies) != copiesBefore {
		t.Fatalf("must not copy dest disks onto the control node: %v", bk.copies)
	}

	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/backups/artifacts/"+artID+"/restore", strings.NewReader(`{"mode":"replace","target_node_id":"`+worker.ID+`"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Nodal-Confirm", "restore")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(req)
	raw, _ = io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("replace other node %d %s", res.StatusCode, raw)
	}

	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/backups/artifacts/"+artID+"/restore", strings.NewReader(`{"mode":"new","target_node_id":"`+uuid.NewString()+`"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(req)
	if res.StatusCode != http.StatusNotFound {
		b, _ := io.ReadAll(res.Body)
		t.Fatalf("unknown dest %d %s", res.StatusCode, b)
	}
	_ = res.Body.Close()
}

func TestPhase33DRExportOmitsCredentials(t *testing.T) {
	s, mem, token := testServer(t)
	cluster, _ := mem.GetCluster(context.Background())
	nodeID := uuid.NewString()
	_ = mem.UpsertNode(context.Background(), appdb.Node{ID: nodeID, ClusterID: cluster.ID, Name: "local"})
	if err := mem.CreateBackupTarget(context.Background(), appdb.BackupTarget{
		ID: uuid.NewString(), ClusterID: cluster.ID, Name: "r2", Kind: "r2",
		Locator: "s3://ndl-backups/prefix", Endpoint: "https://account.r2.cloudflarestorage.com",
		Bucket: "ndl-backups", Prefix: "node-a", Status: appdb.BackupAvailable,
	}, "secret-pass", "enc-key"); err != nil {
		t.Fatal(err)
	}
	runID := uuid.NewString()
	if err := mem.CreateBackupRun(context.Background(), appdb.BackupRun{
		ID: runID, ClusterID: cluster.ID, TargetID: "tgt", WorkloadID: "wl-1", Status: appdb.BackupSucceeded,
	}); err != nil {
		t.Fatal(err)
	}
	if err := mem.CreateBackupArtifact(context.Background(), appdb.BackupArtifact{
		ID: uuid.NewString(), ClusterID: cluster.ID, RunID: runID, WorkloadID: "wl-1",
		ChecksumSHA256: "abc", SizeBytes: 4, Locator: "s3://ndl-backups/obj-1", Format: "qcow2",
		ObjectKey: "obj-1", Encrypted: true,
	}); err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)
	req, _ := http.NewRequest("GET", ts.URL+"/api/v1/backups/dr-export", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ := ts.Client().Do(req)
	raw, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("dr-export %d %s", res.StatusCode, raw)
	}
	if strings.Contains(string(raw), "secret-pass") || strings.Contains(string(raw), `"password"`) || strings.Contains(string(raw), "enc-key") || strings.Contains(string(raw), "encryption_key") {
		t.Fatalf("credentials leaked: %s", raw)
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	if out["cluster_id"] != cluster.ID {
		t.Fatalf("cluster %+v", out)
	}
	arts, _ := out["artifacts"].([]any)
	if len(arts) != 1 {
		t.Fatalf("artifacts %+v", out)
	}
	art := arts[0].(map[string]any)
	if art["locality"] != "object" || art["object_key"] != "obj-1" || art["pull_url"] != "s3://ndl-backups/obj-1" {
		t.Fatalf("object locality %+v", art)
	}
}
