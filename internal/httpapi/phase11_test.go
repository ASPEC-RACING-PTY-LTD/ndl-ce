package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/no-dal/ndl-ce/internal/appdb"
	"github.com/no-dal/ndl-ce/internal/auth"
	"github.com/no-dal/ndl-ce/internal/qemu"
	"github.com/no-dal/ndl-ce/internal/rbac"
	"github.com/no-dal/ndl-ce/internal/storage"
)

type fakeBackup struct {
	copies [][3]string
	res    storage.CopyResult
	err    error
}

func (f *fakeBackup) CopyBackup(_ context.Context, action, src, dest string) (storage.CopyResult, error) {
	f.copies = append(f.copies, [3]string{action, src, dest})
	if f.err != nil {
		return storage.CopyResult{}, f.err
	}
	if action == qemu.BackupMkdir {
		if err := os.MkdirAll(dest, 0o750); err != nil {
			return storage.CopyResult{}, err
		}
		return storage.CopyResult{Dest: dest, Size: 1, Format: "directory"}, nil
	}
	if action == qemu.BackupStat {
		info, err := os.Stat(dest)
		if err != nil || !info.IsDir() {
			return storage.CopyResult{Dest: dest, Size: 0, Format: "directory"}, nil
		}
		return storage.CopyResult{Dest: dest, Size: 1, Format: "directory"}, nil
	}
	if action == qemu.BackupDelete {
		return storage.CopyResult{Dest: dest, Format: "qcow2"}, nil
	}
	res := f.res
	if res.SHA256 == "" {
		res = storage.CopyResult{Dest: dest, SHA256: "abc123", Size: 4, Format: "qcow2"}
	}
	if res.Dest == "" {
		res.Dest = dest
	}
	return res, nil
}

func TestBackupRunRestoreNewAndReplaceConfirm(t *testing.T) {
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
	bk := &fakeBackup{}
	s.Backup = bk
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)

	body := `{"name":"web","kind":"vm","pool_id":"` + poolID + `","network_id":"` + netID + `","cpus":1,"memory_bytes":268435456}`
	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/workloads", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ := ts.Client().Do(req)
	if res.StatusCode != http.StatusCreated {
		b, _ := io.ReadAll(res.Body)
		t.Fatalf("vm create %d %s", res.StatusCode, b)
	}
	var vm map[string]any
	_ = json.NewDecoder(res.Body).Decode(&vm)
	_ = res.Body.Close()
	vmID := vm["id"].(string)

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
	if _, ok := tgt["password"]; ok {
		t.Fatal("password must never be returned")
	}
	if tgt["status"] != "available" {
		t.Fatalf("local target %+v", tgt)
	}
	targetID := tgt["id"].(string)

	req, _ = http.NewRequest("GET", ts.URL+"/api/v1/backups/targets", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(req)
	raw, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if strings.Contains(string(raw), "secret-pass") {
		t.Fatal("password leaked in list")
	}

	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/backups/run", strings.NewReader(`{"workload_id":"`+vmID+`","target_id":"`+targetID+`"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(req)
	if res.StatusCode != http.StatusAccepted {
		b, _ := io.ReadAll(res.Body)
		t.Fatalf("run %d %s", res.StatusCode, b)
	}
	var run map[string]any
	_ = json.NewDecoder(res.Body).Decode(&run)
	_ = res.Body.Close()
	if run["status"] != "succeeded" {
		t.Fatalf("run %+v", run)
	}
	if run["snapshot_id"] == nil || run["snapshot_id"] == "" {
		t.Fatal("backup must snapshot then copy")
	}
	var copied bool
	var mkdir bool
	for _, c := range bk.copies {
		if c[0] == qemu.BackupMkdir {
			mkdir = true
		}
		if c[0] == qemu.BackupCopy {
			copied = true
			if strings.Contains(c[1], "--") {
				t.Fatalf("must convert the frozen parent, not the live overlay: %s", c[1])
			}
		}
	}
	if !mkdir {
		t.Fatal("local target mkdir must go through the typed agent")
	}
	if !copied {
		t.Fatal("backup must convert a frozen disk into the target")
	}

	req, _ = http.NewRequest("GET", ts.URL+"/api/v1/backups/artifacts", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(req)
	var arts map[string]any
	_ = json.NewDecoder(res.Body).Decode(&arts)
	_ = res.Body.Close()
	items := arts["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("artifacts %+v", arts)
	}
	art := items[0].(map[string]any)
	artID := art["id"].(string)
	if art["checksum_sha256"] == "" {
		t.Fatal("checksum required")
	}

	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/backups/artifacts/"+artID+"/restore", strings.NewReader(`{"mode":"new"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(req)
	if res.StatusCode != http.StatusAccepted {
		b, _ := io.ReadAll(res.Body)
		t.Fatalf("restore new %d %s", res.StatusCode, b)
	}
	var restored map[string]any
	_ = json.NewDecoder(res.Body).Decode(&restored)
	_ = res.Body.Close()
	newID, _ := restored["restored_workload_id"].(string)
	if newID == "" || newID == vmID {
		t.Fatalf("restore new must mint a new UUID: %+v", restored)
	}
	got, _ := mem.GetWorkload(context.Background(), cluster.ID, newID)
	if got == nil {
		t.Fatal("restored workload missing")
	}

	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/backups/artifacts/"+artID+"/restore", strings.NewReader(`{"mode":"replace"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(req)
	if res.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("replace without confirm %d", res.StatusCode)
	}
	_ = res.Body.Close()

	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/backups/artifacts/"+artID+"/restore", strings.NewReader(`{"mode":"replace"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Nodal-Confirm", "restore")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(req)
	if res.StatusCode != http.StatusAccepted {
		b, _ := io.ReadAll(res.Body)
		t.Fatalf("replace %d %s", res.StatusCode, b)
	}
	_ = res.Body.Close()
}

func TestBackupCTRefusedAndNFSUnavailable(t *testing.T) {
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
	s.VM = &fakeVM{}
	s.Backup = &fakeBackup{}
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)
	ctBody := `{"name":"alpine-a","kind":"system-container","image_pin":"alpine/3.21/amd64/default","pool_id":"` + poolID + `","network_id":"` + netID + `"}`
	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/workloads", strings.NewReader(ctBody))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ := ts.Client().Do(req)
	if res.StatusCode != http.StatusCreated {
		b, _ := io.ReadAll(res.Body)
		t.Fatalf("ct %d %s", res.StatusCode, b)
	}
	var ct map[string]any
	_ = json.NewDecoder(res.Body).Decode(&ct)
	_ = res.Body.Close()

	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/backups/targets", strings.NewReader(`{"name":"nas","kind":"nfs","locator":"nas.example:/export"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(req)
	if res.StatusCode != http.StatusCreated {
		b, _ := io.ReadAll(res.Body)
		t.Fatalf("nfs target %d %s", res.StatusCode, b)
	}
	var tgt map[string]any
	_ = json.NewDecoder(res.Body).Decode(&tgt)
	_ = res.Body.Close()
	if tgt["status"] != "unavailable" {
		t.Fatalf("nfs must be unavailable unless mounted: %+v", tgt)
	}

	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/backups/run", strings.NewReader(`{"workload_id":"`+ct["id"].(string)+`","target_id":"`+tgt["id"].(string)+`"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(req)
	if res.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("ct backup %d", res.StatusCode)
	}
	b, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if !strings.Contains(strings.ToLower(string(b)), "zfs") {
		t.Fatalf("honest CT reason: %s", b)
	}

	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/backups/targets", strings.NewReader(`{"name":"bad","kind":"nfs","locator":"/etc"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(req)
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("nfs /etc %d", res.StatusCode)
	}
	_ = res.Body.Close()
}

func TestBackupViewerCannotRestore(t *testing.T) {
	s, mem, token := testServer(t)
	cluster, _ := mem.GetCluster(context.Background())
	nodeID := uuid.NewString()
	_ = mem.UpsertNode(context.Background(), appdb.Node{ID: nodeID, ClusterID: cluster.ID, Name: "local"})
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	_ = claimAdmin(t, ts, token)
	hash, err := auth.HashPassword("password1")
	if err != nil {
		t.Fatal(err)
	}
	u := appdb.User{ID: uuid.NewString(), ClusterID: cluster.ID, Username: "view", PasswordHash: hash}
	_ = mem.CreateUser(context.Background(), u)
	_ = mem.BindRole(context.Background(), cluster.ID, u.ID, rbac.Viewer)
	login, _ := ts.Client().Post(ts.URL+"/api/v1/auth/login", "application/json", strings.NewReader(`{"username":"view","password":"password1"}`))
	var viewCookie string
	for _, c := range login.Cookies() {
		if c.Name == sessionCookie {
			viewCookie = c.Value
		}
	}
	_ = login.Body.Close()
	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/backups/run", strings.NewReader(`{"workload_id":"`+uuid.NewString()+`","target_id":"`+uuid.NewString()+`"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: viewCookie})
	res, _ := ts.Client().Do(req)
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("viewer run %d", res.StatusCode)
	}
	_ = res.Body.Close()
	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/backups/artifacts/"+uuid.NewString()+"/restore", strings.NewReader(`{"mode":"new"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: viewCookie})
	res, _ = ts.Client().Do(req)
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("viewer restore %d", res.StatusCode)
	}
	_ = res.Body.Close()
}

func TestBackupRetentionPrunesArtifactsNotLiveSnaps(t *testing.T) {
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
	s.Backup = &fakeBackup{}
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)
	body := `{"name":"keep","kind":"vm","pool_id":"` + poolID + `","network_id":"` + netID + `"}`
	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/workloads", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ := ts.Client().Do(req)
	var vm map[string]any
	_ = json.NewDecoder(res.Body).Decode(&vm)
	_ = res.Body.Close()
	vmID := vm["id"].(string)
	dir := t.TempDir()
	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/backups/targets", strings.NewReader(`{"name":"local","kind":"local","locator":"`+dir+`"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(req)
	var tgt map[string]any
	_ = json.NewDecoder(res.Body).Decode(&tgt)
	_ = res.Body.Close()
	targetID := tgt["id"].(string)
	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/backups/policies", strings.NewReader(`{"name":"nightly","workload_id":"`+vmID+`","target_id":"`+targetID+`","schedule":"nightly","keep_daily":1,"keep_weekly":0,"keep_monthly":0}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(req)
	if res.StatusCode != http.StatusCreated {
		b, _ := io.ReadAll(res.Body)
		t.Fatalf("policy %d %s", res.StatusCode, b)
	}
	var pol map[string]any
	_ = json.NewDecoder(res.Body).Decode(&pol)
	_ = res.Body.Close()
	policyID := pol["id"].(string)
	for i := 0; i < 2; i++ {
		req, _ = http.NewRequest("POST", ts.URL+"/api/v1/backups/run", strings.NewReader(`{"workload_id":"`+vmID+`","target_id":"`+targetID+`","policy_id":"`+policyID+`"}`))
		req.Header.Set("Content-Type", "application/json")
		req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
		res, _ = ts.Client().Do(req)
		if res.StatusCode != http.StatusAccepted {
			b, _ := io.ReadAll(res.Body)
			t.Fatalf("run %d %s", i, b)
		}
		_ = res.Body.Close()
	}
	arts, _ := mem.ListBackupArtifacts(context.Background(), cluster.ID)
	if len(arts) != 1 {
		t.Fatalf("retention should keep 1 artifact, got %d", len(arts))
	}
	snaps, _ := mem.ListSnapshots(context.Background(), cluster.ID, vmID)
	if len(snaps) < 2 {
		t.Fatalf("live overlay snaps must not be pruned, got %d", len(snaps))
	}
}

func TestNightlyPolicyTick(t *testing.T) {
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
	s.Backup = &fakeBackup{}
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)
	body := `{"name":"night","kind":"vm","pool_id":"` + poolID + `","network_id":"` + netID + `"}`
	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/workloads", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ := ts.Client().Do(req)
	var vm map[string]any
	_ = json.NewDecoder(res.Body).Decode(&vm)
	_ = res.Body.Close()
	dir := t.TempDir()
	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/backups/targets", strings.NewReader(`{"name":"local","kind":"local","locator":"`+dir+`"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(req)
	var tgt map[string]any
	_ = json.NewDecoder(res.Body).Decode(&tgt)
	_ = res.Body.Close()
	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/backups/policies", strings.NewReader(`{"name":"nightly","workload_id":"`+vm["id"].(string)+`","target_id":"`+tgt["id"].(string)+`","schedule":"nightly","keep_daily":7,"keep_weekly":4,"keep_monthly":3}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(req)
	if res.StatusCode != http.StatusCreated {
		b, _ := io.ReadAll(res.Body)
		t.Fatalf("policy %d %s", res.StatusCode, b)
	}
	_ = res.Body.Close()
	s.TickNightlyBackups(context.Background())
	runs, _ := mem.ListBackupRuns(context.Background(), cluster.ID)
	if len(runs) != 1 || runs[0].Status != appdb.BackupSucceeded {
		t.Fatalf("nightly %+v", runs)
	}
}
