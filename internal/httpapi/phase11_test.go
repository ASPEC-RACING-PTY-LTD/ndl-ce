package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/no-dal/ndl-ce/internal/appdb"
	"github.com/no-dal/ndl-ce/internal/auth"
	"github.com/no-dal/ndl-ce/internal/lxc"
	"github.com/no-dal/ndl-ce/internal/migration"
	"github.com/no-dal/ndl-ce/internal/qemu"
	"github.com/no-dal/ndl-ce/internal/rbac"
	"github.com/no-dal/ndl-ce/internal/storage"
	"github.com/no-dal/ndl-ce/internal/vmspec"
)

type fakeBackup struct {
	copies   [][3]string
	converts [][2]string
	res      storage.CopyResult
	err      error
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
	if dest != "" {
		if err := os.MkdirAll(filepath.Dir(dest), 0o750); err == nil {
			_ = os.WriteFile(dest, []byte("qcow"), 0o640)
		}
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

func (f *fakeBackup) ConvertImport(_ context.Context, req qemu.ConvertRequest) error {
	f.converts = append(f.converts, [2]string{req.SourcePath, req.DestPath})
	if f.err != nil {
		return f.err
	}
	body, err := os.ReadFile(req.SourcePath)
	if err != nil {
		body = []byte("converted")
	}
	if err := os.MkdirAll(filepath.Dir(req.DestPath), 0o750); err != nil {
		return err
	}
	return os.WriteFile(req.DestPath, body, 0o640)
}

type skipConvertBackup struct{ *fakeBackup }

func (skipConvertBackup) ConvertImport(context.Context, qemu.ConvertRequest) error {
	return nil
}

func (f *fakeBackup) ExtractArchive(_ context.Context, src, dest string) error {
	if f.err != nil {
		return f.err
	}
	if err := os.MkdirAll(dest, 0o750); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	return migration.ExtractTar(in, dest, 1<<30)
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
	storedRun, err := mem.GetBackupRun(context.Background(), cluster.ID, run["id"].(string))
	if err != nil || storedRun == nil || storedRun.Status != appdb.BackupSucceeded {
		t.Fatalf("run row %+v %v", storedRun, err)
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
	storedRestore, err := mem.GetBackupRun(context.Background(), cluster.ID, restored["id"].(string))
	if err != nil || storedRestore == nil || storedRestore.Status != appdb.BackupSucceeded || storedRestore.RestoredWorkloadID != newID {
		t.Fatalf("restore run row %+v %v", storedRestore, err)
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

func TestPhase11BackupTargetCreateFailsClosedForUntypedLocator(t *testing.T) {
	s, mem, token := testServer(t)
	s.Backup = &fakeBackup{}
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)
	cluster, _ := mem.GetCluster(t.Context())

	garbage, _ := http.NewRequest("POST", ts.URL+"/api/v1/backups/targets", strings.NewReader(`{"name":"bad","kind":"nfs","locator":"garbage"}`))
	garbage.Header.Set("Content-Type", "application/json")
	garbage.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ := ts.Client().Do(garbage)
	body, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusBadRequest || !strings.Contains(string(body), "nfs locator must be server:/export") {
		t.Fatalf("garbage nfs %d %s", res.StatusCode, body)
	}

	escape, _ := http.NewRequest("POST", ts.URL+"/api/v1/backups/targets", strings.NewReader(`{"name":"bad","kind":"nfs","locator":"../etc"}`))
	escape.Header.Set("Content-Type", "application/json")
	escape.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(escape)
	body, _ = io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusBadRequest || !strings.Contains(string(body), "locator must be an absolute path without traversal") {
		t.Fatalf("escape nfs %d %s", res.StatusCode, body)
	}

	smbBad, _ := http.NewRequest("POST", ts.URL+"/api/v1/backups/targets", strings.NewReader(`{"name":"bad","kind":"smb","locator":"not-unc"}`))
	smbBad.Header.Set("Content-Type", "application/json")
	smbBad.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(smbBad)
	body, _ = io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusBadRequest || !strings.Contains(string(body), "smb locator must be //server/share") {
		t.Fatalf("garbage smb %d %s", res.StatusCode, body)
	}

	items, err := mem.ListBackupTargets(t.Context(), cluster.ID)
	if err != nil || len(items) != 0 {
		t.Fatalf("GET must not list an untyped backup locator: %+v %v", items, err)
	}

	unc, _ := http.NewRequest("POST", ts.URL+"/api/v1/backups/targets", strings.NewReader(`{"name":"share","kind":"smb","locator":"//files.example/iso"}`))
	unc.Header.Set("Content-Type", "application/json")
	unc.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(unc)
	body, _ = io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusCreated || !strings.Contains(string(body), "//files.example/iso") {
		t.Fatalf("smb unc %d %s", res.StatusCode, body)
	}
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

func TestBackupOverlayAfterFlattenDoesNotInheritStaleParent(t *testing.T) {
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

	body := `{"name":"web","kind":"vm","pool_id":"` + poolID + `","network_id":"` + netID + `"}`
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

	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/workloads/"+vmID+"/snapshots", strings.NewReader(`{"name":"before-flat"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(req)
	raw, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("snap create %d %s", res.StatusCode, raw)
	}
	var first map[string]any
	if err := json.Unmarshal(raw, &first); err != nil {
		t.Fatal(err)
	}

	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/workloads/"+vmID+"/snapshots/flatten", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Nodal-Confirm", "flatten")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(req)
	raw, _ = io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("flatten %d %s", res.StatusCode, raw)
	}

	dir := t.TempDir()
	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/backups/targets", strings.NewReader(`{"name":"local","kind":"local","locator":"`+dir+`"}`))
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
	raw, _ = io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusAccepted {
		t.Fatalf("backup run %d %s", res.StatusCode, raw)
	}

	req, _ = http.NewRequest("GET", ts.URL+"/api/v1/workloads/"+vmID+"/snapshots", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(req)
	raw, _ = io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("snapshots %d %s", res.StatusCode, raw)
	}
	var listed struct {
		Items []map[string]any `json:"items"`
	}
	if err := json.Unmarshal(raw, &listed); err != nil {
		t.Fatal(err)
	}
	var backup map[string]any
	for _, item := range listed.Items {
		if item["purpose_tag"] == "ndl-backup" {
			backup = item
			break
		}
	}
	if backup == nil {
		t.Fatalf("backup snapshot missing %s", raw)
	}
	if backup["parent_id"] != "" && backup["parent_id"] != nil {
		t.Fatalf("backup overlay after flatten must not inherit leftover parent %s", raw)
	}
	if backup["id"] == first["id"] {
		t.Fatalf("backup snapshot must be a new catalog row %s", raw)
	}
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

func TestBackupRunFailsClosedForExtraDataDisk(t *testing.T) {
	s, mem, token := testServer(t)
	cluster, _ := mem.GetCluster(context.Background())
	nodeID := uuid.NewString()
	_ = mem.UpsertNode(context.Background(), appdb.Node{ID: nodeID, ClusterID: cluster.ID, Name: "local"})
	poolID, netID := seedCompute(t, mem, cluster.ID, nodeID)
	extraVol := uuid.NewString()
	extraRef := "volumes/vm-disk/extra.qcow2"
	if err := mem.CreateVolume(context.Background(), appdb.Volume{
		ID: extraVol, ClusterID: cluster.ID, NodeID: nodeID, PoolID: poolID,
		Class: storage.ClassVMDisk, Kind: storage.KindBlock, Format: storage.FormatQCOW2,
		Status: storage.StatusAvailable, BackendType: storage.BackendDirectory,
		BackendRef: extraRef, SizeBytes: 1 << 30,
	}); err != nil {
		t.Fatal(err)
	}
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
	body := `{"name":"dual-disk","kind":"vm","pool_id":"` + poolID + `","network_id":"` + netID + `","spec":{"disks":[{"role":"boot"},{"role":"data","volume_id":"` + extraVol + `","slot":1}]}}`
	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/workloads", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ := ts.Client().Do(req)
	raw, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("create %d %s", res.StatusCode, raw)
	}
	var vm map[string]any
	if err := json.Unmarshal(raw, &vm); err != nil {
		t.Fatal(err)
	}
	vmID := vm["id"].(string)
	disks, _ := mem.ListWorkloadDisks(context.Background(), cluster.ID, vmID)
	if len(disks) < 2 {
		t.Fatalf("extra data disk create must record boot and data: %+v", disks)
	}
	bootVol := ""
	for _, d := range disks {
		if d.Role == vmspec.DiskRoleBoot {
			bootVol = d.VolumeID
		}
	}
	if bootVol == "" {
		t.Fatal("boot volume missing")
	}
	beforeWL, _ := mem.GetWorkload(context.Background(), cluster.ID, vmID)
	beforeBoot, _ := mem.GetVolume(context.Background(), cluster.ID, bootVol)
	beforeExtra, _ := mem.GetVolume(context.Background(), cluster.ID, extraVol)
	if beforeWL == nil || beforeBoot == nil || beforeExtra == nil {
		t.Fatal("source rows missing")
	}
	dir := t.TempDir()
	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/backups/targets", strings.NewReader(`{"name":"local-disk","kind":"local","locator":"`+dir+`"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(req)
	raw, _ = io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("target %d %s", res.StatusCode, raw)
	}
	var tgt map[string]any
	if err := json.Unmarshal(raw, &tgt); err != nil {
		t.Fatal(err)
	}
	targetID := tgt["id"].(string)
	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/backups/run", strings.NewReader(`{"workload_id":"`+vmID+`","target_id":"`+targetID+`"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(req)
	raw, _ = io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("extra data disk backup %d %s", res.StatusCode, raw)
	}
	if !strings.Contains(string(raw), "backup of additional data disks is not implemented") {
		t.Fatalf("extra data disk backup body %s", raw)
	}
	for _, c := range bk.copies {
		if c[0] == qemu.BackupCopy {
			t.Fatalf("CopyBackup must not write a boot-only artifact: %+v", bk.copies)
		}
	}
	runs, _ := mem.ListBackupRuns(context.Background(), cluster.ID)
	if len(runs) != 0 {
		t.Fatalf("backup must not persist a run restore cannot apply: %+v", runs)
	}
	arts, _ := mem.ListBackupArtifacts(context.Background(), cluster.ID)
	if len(arts) != 0 {
		t.Fatalf("backup must not persist a boot-only artifact: %+v", arts)
	}
	snaps, _ := mem.ListSnapshots(context.Background(), cluster.ID, vmID)
	if len(snaps) != 0 {
		t.Fatalf("backup must not snapshot the boot disk and drop extra disks: %+v", snaps)
	}
	gotWL, _ := mem.GetWorkload(context.Background(), cluster.ID, vmID)
	gotBoot, _ := mem.GetVolume(context.Background(), cluster.ID, bootVol)
	gotExtra, _ := mem.GetVolume(context.Background(), cluster.ID, extraVol)
	if gotWL == nil || gotWL.Status != beforeWL.Status || gotWL.NodeID != beforeWL.NodeID || string(gotWL.SpecJSON) != string(beforeWL.SpecJSON) {
		t.Fatalf("GET must keep the extra-disk VM untouched: before=%+v after=%+v", beforeWL, gotWL)
	}
	if gotBoot == nil || gotBoot.BackendRef != beforeBoot.BackendRef {
		t.Fatalf("boot volume locator must stay %s, got %+v", beforeBoot.BackendRef, gotBoot)
	}
	if gotExtra == nil || gotExtra.BackendRef != extraRef || gotExtra.Status != storage.StatusAvailable {
		t.Fatalf("extra volume must stay available at %s: %+v", extraRef, gotExtra)
	}
	gotDisks, _ := mem.ListWorkloadDisks(context.Background(), cluster.ID, vmID)
	if len(gotDisks) != len(disks) {
		t.Fatalf("disk catalog must stay %+v, got %+v", disks, gotDisks)
	}
	_, err := s.restoreNewVM(context.Background(), cluster.ID, *gotWL, appdb.BackupArtifact{ID: uuid.NewString()}, false, &appdb.Node{ID: nodeID, ClusterID: cluster.ID})
	if err == nil || !strings.Contains(err.Error(), "restore of additional data disks is not implemented") {
		t.Fatalf("restore of extra data disks must stay refused: %v", err)
	}
}

func TestNightlyPolicyTickFailsClosedForExtraDataDisk(t *testing.T) {
	s, mem, token := testServer(t)
	cluster, _ := mem.GetCluster(context.Background())
	nodeID := uuid.NewString()
	_ = mem.UpsertNode(context.Background(), appdb.Node{ID: nodeID, ClusterID: cluster.ID, Name: "local"})
	poolID, netID := seedCompute(t, mem, cluster.ID, nodeID)
	extraVol := uuid.NewString()
	if err := mem.CreateVolume(context.Background(), appdb.Volume{
		ID: extraVol, ClusterID: cluster.ID, NodeID: nodeID, PoolID: poolID,
		Class: storage.ClassVMDisk, Kind: storage.KindBlock, Format: storage.FormatQCOW2,
		Status: storage.StatusAvailable, BackendType: storage.BackendDirectory,
		BackendRef: "volumes/vm-disk/extra.qcow2", SizeBytes: 1 << 30,
	}); err != nil {
		t.Fatal(err)
	}
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
	body := `{"name":"night-extra","kind":"vm","pool_id":"` + poolID + `","network_id":"` + netID + `","spec":{"disks":[{"role":"boot"},{"role":"data","volume_id":"` + extraVol + `","slot":1}]}}`
	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/workloads", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ := ts.Client().Do(req)
	raw, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("create %d %s", res.StatusCode, raw)
	}
	var vm map[string]any
	if err := json.Unmarshal(raw, &vm); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/backups/targets", strings.NewReader(`{"name":"local","kind":"local","locator":"`+dir+`"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(req)
	raw, _ = io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("target %d %s", res.StatusCode, raw)
	}
	var tgt map[string]any
	if err := json.Unmarshal(raw, &tgt); err != nil {
		t.Fatal(err)
	}
	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/backups/policies", strings.NewReader(`{"name":"nightly","workload_id":"`+vm["id"].(string)+`","target_id":"`+tgt["id"].(string)+`","schedule":"nightly","keep_daily":7,"keep_weekly":4,"keep_monthly":3}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(req)
	raw, _ = io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("policy %d %s", res.StatusCode, raw)
	}
	s.TickNightlyBackups(context.Background())
	runs, _ := mem.ListBackupRuns(context.Background(), cluster.ID)
	if len(runs) != 0 {
		t.Fatalf("nightly extra-disk backup must not persist a run: %+v", runs)
	}
	arts, _ := mem.ListBackupArtifacts(context.Background(), cluster.ID)
	if len(arts) != 0 {
		t.Fatalf("nightly extra-disk backup must not persist an artifact: %+v", arts)
	}
	for _, c := range bk.copies {
		if c[0] == qemu.BackupCopy {
			t.Fatalf("nightly CopyBackup must not write a boot-only artifact: %+v", bk.copies)
		}
	}
}

func TestBackupRunFailsClosedForCatalogExtraDataDisk(t *testing.T) {
	s, mem, token := testServer(t)
	cluster, _ := mem.GetCluster(context.Background())
	nodeID := uuid.NewString()
	_ = mem.UpsertNode(context.Background(), appdb.Node{ID: nodeID, ClusterID: cluster.ID, Name: "local"})
	poolID, _ := seedCompute(t, mem, cluster.ID, nodeID)
	boot := appdb.Volume{
		ID: uuid.NewString(), ClusterID: cluster.ID, NodeID: nodeID, PoolID: poolID,
		Class: storage.ClassVMDisk, Kind: storage.KindBlock, Format: storage.FormatQCOW2,
		Status: storage.StatusAvailable, BackendType: storage.BackendDirectory,
		BackendRef: "volumes/vm-disk/boot.qcow2",
	}
	extra := appdb.Volume{
		ID: uuid.NewString(), ClusterID: cluster.ID, NodeID: nodeID, PoolID: poolID,
		Class: storage.ClassVMDisk, Kind: storage.KindBlock, Format: storage.FormatQCOW2,
		Status: storage.StatusAvailable, BackendType: storage.BackendDirectory,
		BackendRef: "volumes/vm-disk/extra.qcow2",
	}
	for _, vol := range []appdb.Volume{boot, extra} {
		if err := mem.CreateVolume(context.Background(), vol); err != nil {
			t.Fatal(err)
		}
	}
	wlID := uuid.NewString()
	spec := vmspec.Spec{
		Name: "catalog-extra", CPUs: 1, MemoryBytes: 128 << 20,
		Disks: []vmspec.Disk{{Role: vmspec.DiskRoleBoot, VolumeID: boot.ID, Format: storage.FormatQCOW2}},
	}
	if err := mem.CreateWorkload(context.Background(), appdb.Workload{
		ID: wlID, ClusterID: cluster.ID, NodeID: nodeID, OwnerNodeID: nodeID, DesiredNodeID: nodeID,
		Name: spec.Name, Kind: vmspec.KindVM, Status: qemu.StatusStopped,
		CPUs: spec.CPUs, MemoryBytes: spec.MemoryBytes, SpecJSON: vmspec.MustJSON(spec),
	}); err != nil {
		t.Fatal(err)
	}
	if err := mem.CreateWorkloadDisk(context.Background(), appdb.WorkloadDisk{
		ID: uuid.NewString(), ClusterID: cluster.ID, WorkloadID: wlID, VolumeID: boot.ID,
		Role: vmspec.DiskRoleBoot, Slot: 0, Format: storage.FormatQCOW2,
	}); err != nil {
		t.Fatal(err)
	}
	if err := mem.CreateWorkloadDisk(context.Background(), appdb.WorkloadDisk{
		ID: uuid.NewString(), ClusterID: cluster.ID, WorkloadID: wlID, VolumeID: extra.ID,
		Role: vmspec.DiskRoleData, Slot: 1, Format: storage.FormatQCOW2,
	}); err != nil {
		t.Fatal(err)
	}
	s.VM = &fakeVM{}
	bk := &fakeBackup{}
	s.Backup = bk
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)
	dir := t.TempDir()
	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/backups/targets", strings.NewReader(`{"name":"local-disk","kind":"local","locator":"`+dir+`"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ := ts.Client().Do(req)
	raw, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("target %d %s", res.StatusCode, raw)
	}
	var tgt map[string]any
	if err := json.Unmarshal(raw, &tgt); err != nil {
		t.Fatal(err)
	}
	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/backups/run", strings.NewReader(`{"workload_id":"`+wlID+`","target_id":"`+tgt["id"].(string)+`"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(req)
	raw, _ = io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusUnprocessableEntity || !strings.Contains(string(raw), "backup of additional data disks is not implemented") {
		t.Fatalf("catalog extra data disk backup %d %s", res.StatusCode, raw)
	}
	runs, _ := mem.ListBackupRuns(context.Background(), cluster.ID)
	if len(runs) != 0 {
		t.Fatalf("catalog extra disk must not persist a run: %+v", runs)
	}
	arts, _ := mem.ListBackupArtifacts(context.Background(), cluster.ID)
	if len(arts) != 0 {
		t.Fatalf("catalog extra disk must not persist an artifact: %+v", arts)
	}
	for _, c := range bk.copies {
		if c[0] == qemu.BackupCopy {
			t.Fatalf("CopyBackup must not write a boot-only artifact: %+v", bk.copies)
		}
	}
}

func TestRestoreNewVMExtraDataDiskIsUnprocessable(t *testing.T) {
	s, mem, _ := testServer(t)
	cluster, _ := mem.GetCluster(context.Background())
	nodeID := uuid.NewString()
	_ = mem.UpsertNode(context.Background(), appdb.Node{ID: nodeID, ClusterID: cluster.ID, Name: "local"})
	poolID, _ := seedCompute(t, mem, cluster.ID, nodeID)
	bootID := uuid.NewString()
	extra := uuid.NewString()
	for _, id := range []string{bootID, extra} {
		if err := mem.CreateVolume(context.Background(), appdb.Volume{
			ID: id, ClusterID: cluster.ID, NodeID: nodeID, PoolID: poolID,
			Class: storage.ClassVMDisk, Kind: storage.KindBlock, Format: storage.FormatQCOW2,
			Status: storage.StatusAvailable, BackendType: storage.BackendDirectory,
			BackendRef: "volumes/vm-disk/" + id + ".qcow2",
		}); err != nil {
			t.Fatal(err)
		}
	}
	wlID := uuid.NewString()
	spec := vmspec.Spec{
		Name: "web",
		Disks: []vmspec.Disk{
			{Role: vmspec.DiskRoleBoot, VolumeID: bootID},
			{Role: vmspec.DiskRoleData, VolumeID: extra},
		},
	}
	src := appdb.Workload{
		ID: wlID, ClusterID: cluster.ID, NodeID: nodeID, Name: "web", Kind: vmspec.KindVM,
		SpecJSON: vmspec.MustJSON(spec),
	}
	if err := mem.CreateWorkload(context.Background(), src); err != nil {
		t.Fatal(err)
	}
	if err := mem.CreateWorkloadDisk(context.Background(), appdb.WorkloadDisk{
		ID: uuid.NewString(), ClusterID: cluster.ID, WorkloadID: wlID, VolumeID: bootID, Role: vmspec.DiskRoleBoot,
	}); err != nil {
		t.Fatal(err)
	}
	dest := appdb.Node{ID: uuid.NewString(), ClusterID: cluster.ID, Name: "other", Role: "worker"}
	_, err := s.restoreNewVM(context.Background(), cluster.ID, src, appdb.BackupArtifact{ID: uuid.NewString()}, false, &dest)
	if err == nil || !strings.Contains(strings.ToLower(err.Error()), "disk") {
		t.Fatalf("extra data disk restore must fail closed: %v", err)
	}
}

func TestRestoreReplaceVMExtraDataDiskIsUnprocessable(t *testing.T) {
	s, mem, _ := testServer(t)
	cluster, _ := mem.GetCluster(context.Background())
	nodeID := uuid.NewString()
	_ = mem.UpsertNode(context.Background(), appdb.Node{ID: nodeID, ClusterID: cluster.ID, Name: "local"})
	poolID, _ := seedCompute(t, mem, cluster.ID, nodeID)
	bootID := uuid.NewString()
	extra := uuid.NewString()
	for _, id := range []string{bootID, extra} {
		if err := mem.CreateVolume(context.Background(), appdb.Volume{
			ID: id, ClusterID: cluster.ID, NodeID: nodeID, PoolID: poolID,
			Class: storage.ClassVMDisk, Kind: storage.KindBlock, Format: storage.FormatQCOW2,
			Status: storage.StatusAvailable, BackendType: storage.BackendDirectory,
			BackendRef: "volumes/vm-disk/" + id + ".qcow2",
		}); err != nil {
			t.Fatal(err)
		}
	}
	wlID := uuid.NewString()
	spec := vmspec.Spec{
		Name: "web",
		Disks: []vmspec.Disk{
			{Role: vmspec.DiskRoleBoot, VolumeID: bootID},
			{Role: vmspec.DiskRoleData, VolumeID: extra},
		},
	}
	src := appdb.Workload{
		ID: wlID, ClusterID: cluster.ID, NodeID: nodeID, Name: "web", Kind: vmspec.KindVM,
		SpecJSON: vmspec.MustJSON(spec),
	}
	if err := mem.CreateWorkload(context.Background(), src); err != nil {
		t.Fatal(err)
	}
	if err := mem.CreateWorkloadDisk(context.Background(), appdb.WorkloadDisk{
		ID: uuid.NewString(), ClusterID: cluster.ID, WorkloadID: wlID, VolumeID: bootID, Role: vmspec.DiskRoleBoot,
	}); err != nil {
		t.Fatal(err)
	}
	s.VM = &fakeVM{}
	s.Backup = &fakeBackup{}
	err := s.restoreReplaceVM(context.Background(), cluster.ID, src, appdb.BackupArtifact{ID: uuid.NewString()})
	if err == nil || !strings.Contains(strings.ToLower(err.Error()), "disk") {
		t.Fatalf("extra data disk replace must fail closed: %v", err)
	}
}

func TestRestoreRefusesSystemContainer(t *testing.T) {
	s, mem, token := testServer(t)
	cluster, _ := mem.GetCluster(context.Background())
	nodeID := uuid.NewString()
	_ = mem.UpsertNode(context.Background(), appdb.Node{ID: nodeID, ClusterID: cluster.ID, Name: "local"})
	wlID := uuid.NewString()
	if err := mem.CreateWorkload(context.Background(), appdb.Workload{
		ID: wlID, ClusterID: cluster.ID, NodeID: nodeID, Name: "ct", Kind: lxc.KindSystemContainer, Status: "stopped",
	}); err != nil {
		t.Fatal(err)
	}
	tgtID := uuid.NewString()
	if err := mem.CreateBackupTarget(context.Background(), appdb.BackupTarget{
		ID: tgtID, ClusterID: cluster.ID, Name: "local", Kind: appdb.BackupLocal, Locator: t.TempDir(), Status: appdb.BackupAvailable,
	}, "", ""); err != nil {
		t.Fatal(err)
	}
	runID := uuid.NewString()
	if err := mem.CreateBackupRun(context.Background(), appdb.BackupRun{
		ID: runID, ClusterID: cluster.ID, TargetID: tgtID, WorkloadID: wlID, Status: appdb.BackupSucceeded,
	}); err != nil {
		t.Fatal(err)
	}
	artID := uuid.NewString()
	if err := mem.CreateBackupArtifact(context.Background(), appdb.BackupArtifact{
		ID: artID, ClusterID: cluster.ID, RunID: runID, WorkloadID: wlID, Format: "qcow2", Locator: "volumes/vm-disk/boot.qcow2",
	}); err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)
	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/backups/artifacts/"+artID+"/restore", strings.NewReader(`{"mode":"new"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ := ts.Client().Do(req)
	raw, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusUnprocessableEntity || !strings.Contains(strings.ToLower(string(raw)), "system container") {
		t.Fatalf("ct restore new %d %s", res.StatusCode, raw)
	}
	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/backups/artifacts/"+artID+"/restore", strings.NewReader(`{"mode":"replace"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Nodal-Confirm", "restore")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(req)
	raw, _ = io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusUnprocessableEntity || !strings.Contains(strings.ToLower(string(raw)), "system container") {
		t.Fatalf("ct restore replace %d %s", res.StatusCode, raw)
	}
}

type failUpdateBackupRunStore struct {
	appdb.Store
}

func (f failUpdateBackupRunStore) UpdateBackupRun(context.Context, appdb.BackupRun) error {
	return errors.New("persist failed")
}

func TestBackupRunFailsClosedWhenRunPersistFails(t *testing.T) {
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

	body := `{"name":"web","kind":"vm","pool_id":"` + poolID + `","network_id":"` + netID + `","cpus":1,"memory_bytes":268435456}`
	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/workloads", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ := ts.Client().Do(req)
	raw, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("vm create %d %s", res.StatusCode, raw)
	}
	var vm map[string]any
	if err := json.Unmarshal(raw, &vm); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/backups/targets", strings.NewReader(`{"name":"local-disk","kind":"local","locator":"`+dir+`"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(req)
	raw, _ = io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("target %d %s", res.StatusCode, raw)
	}
	var tgt map[string]any
	if err := json.Unmarshal(raw, &tgt); err != nil {
		t.Fatal(err)
	}
	s.Store = failUpdateBackupRunStore{Store: mem}

	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/backups/run", strings.NewReader(`{"workload_id":"`+vm["id"].(string)+`","target_id":"`+tgt["id"].(string)+`"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(req)
	raw, _ = io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusInternalServerError {
		t.Fatalf("run persist %d %s", res.StatusCode, raw)
	}
	if !strings.Contains(string(raw), "could not record backup run") {
		t.Fatalf("run persist body %s", raw)
	}
}

func TestBackupRestoreFailsClosedWhenRunPersistFails(t *testing.T) {
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

	body := `{"name":"web","kind":"vm","pool_id":"` + poolID + `","network_id":"` + netID + `","cpus":1,"memory_bytes":268435456}`
	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/workloads", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ := ts.Client().Do(req)
	raw, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("vm create %d %s", res.StatusCode, raw)
	}
	var vm map[string]any
	if err := json.Unmarshal(raw, &vm); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/backups/targets", strings.NewReader(`{"name":"local-disk","kind":"local","locator":"`+dir+`"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(req)
	raw, _ = io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("target %d %s", res.StatusCode, raw)
	}
	var tgt map[string]any
	if err := json.Unmarshal(raw, &tgt); err != nil {
		t.Fatal(err)
	}
	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/backups/run", strings.NewReader(`{"workload_id":"`+vm["id"].(string)+`","target_id":"`+tgt["id"].(string)+`"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(req)
	raw, _ = io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusAccepted {
		t.Fatalf("run %d %s", res.StatusCode, raw)
	}
	arts, err := mem.ListBackupArtifacts(context.Background(), cluster.ID)
	if err != nil || len(arts) == 0 {
		t.Fatalf("artifacts %+v %v", arts, err)
	}
	s.Store = failUpdateBackupRunStore{Store: mem}

	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/backups/artifacts/"+arts[0].ID+"/restore", strings.NewReader(`{"mode":"new"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(req)
	raw, _ = io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusInternalServerError {
		t.Fatalf("restore persist %d %s", res.StatusCode, raw)
	}
	if !strings.Contains(string(raw), "could not record backup run") {
		t.Fatalf("restore persist body %s", raw)
	}
}

func seedVMRestoreArtifact(t *testing.T, mem *appdb.Memory, clusterID, wlID, locator string) string {
	t.Helper()
	tgtID := uuid.NewString()
	if err := mem.CreateBackupTarget(context.Background(), appdb.BackupTarget{
		ID: tgtID, ClusterID: clusterID, Name: "local", Kind: appdb.BackupLocal, Locator: t.TempDir(), Status: appdb.BackupAvailable,
	}, "", ""); err != nil {
		t.Fatal(err)
	}
	runID := uuid.NewString()
	if err := mem.CreateBackupRun(context.Background(), appdb.BackupRun{
		ID: runID, ClusterID: clusterID, TargetID: tgtID, WorkloadID: wlID, Status: appdb.BackupSucceeded,
	}); err != nil {
		t.Fatal(err)
	}
	artID := uuid.NewString()
	if err := mem.CreateBackupArtifact(context.Background(), appdb.BackupArtifact{
		ID: artID, ClusterID: clusterID, RunID: runID, WorkloadID: wlID, Format: "qcow2", Locator: locator,
	}); err != nil {
		t.Fatal(err)
	}
	return artID
}

func TestRestoreNewFailsClosedWhenDiskPersistFails(t *testing.T) {
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
	body := `{"name":"web","kind":"vm","pool_id":"` + poolID + `","network_id":"` + netID + `"}`
	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/workloads", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ := ts.Client().Do(req)
	raw, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("vm create %d %s", res.StatusCode, raw)
	}
	var vm map[string]any
	if err := json.Unmarshal(raw, &vm); err != nil {
		t.Fatal(err)
	}
	artID := seedVMRestoreArtifact(t, mem, cluster.ID, vm["id"].(string), "volumes/vm-disk/boot.qcow2")
	s.Store = failCreateWorkloadDiskStore{Store: mem}

	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/backups/artifacts/"+artID+"/restore", strings.NewReader(`{"mode":"new"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(req)
	raw, _ = io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusInternalServerError {
		t.Fatalf("disk persist %d %s", res.StatusCode, raw)
	}
	if !strings.Contains(string(raw), "could not record VM disk") {
		t.Fatalf("disk persist body %s", raw)
	}
}

func TestRestoreOrphanFailsClosedForUnavailableDestPool(t *testing.T) {
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
	body := `{"name":"web","kind":"vm","pool_id":"` + poolID + `","network_id":"` + netID + `"}`
	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/workloads", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ := ts.Client().Do(req)
	raw, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("vm create %d %s", res.StatusCode, raw)
	}
	var vm map[string]any
	if err := json.Unmarshal(raw, &vm); err != nil {
		t.Fatal(err)
	}
	id := vm["id"].(string)
	artID := seedVMRestoreArtifact(t, mem, cluster.ID, id, "volumes/vm-disk/boot.qcow2")
	if err := mem.DeleteWorkload(context.Background(), cluster.ID, id); err != nil {
		t.Fatal(err)
	}
	if err := mem.UpdateStoragePoolObserved(context.Background(), appdb.StoragePool{ID: poolID, Status: storage.StatusFailed}); err != nil {
		t.Fatal(err)
	}
	wlsBefore, _ := mem.ListWorkloads(context.Background(), cluster.ID)
	volsBefore, _ := mem.ListVolumes(context.Background(), cluster.ID, "")
	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/backups/artifacts/"+artID+"/restore", strings.NewReader(`{"mode":"new"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(req)
	raw, _ = io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusConflict {
		t.Fatalf("unavailable dest pool restore %d %s", res.StatusCode, raw)
	}
	if !strings.Contains(string(raw), "storage pool is unavailable") {
		t.Fatalf("unavailable dest pool restore body %s", raw)
	}
	wlsAfter, _ := mem.ListWorkloads(context.Background(), cluster.ID)
	if len(wlsAfter) != len(wlsBefore) {
		t.Fatalf("GET must not list a restore whose dest pool apply cannot allocate: %+v", wlsAfter)
	}
	volsAfter, _ := mem.ListVolumes(context.Background(), cluster.ID, "")
	if len(volsAfter) != len(volsBefore) {
		t.Fatalf("restore must not persist a volume apply cannot allocate: %d -> %d", len(volsBefore), len(volsAfter))
	}
}

func TestRestoreNewFailsClosedWhenNICPersistFails(t *testing.T) {
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
	body := `{"name":"web","kind":"vm","pool_id":"` + poolID + `","network_id":"` + netID + `"}`
	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/workloads", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ := ts.Client().Do(req)
	raw, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("vm create %d %s", res.StatusCode, raw)
	}
	var vm map[string]any
	if err := json.Unmarshal(raw, &vm); err != nil {
		t.Fatal(err)
	}
	artID := seedVMRestoreArtifact(t, mem, cluster.ID, vm["id"].(string), "volumes/vm-disk/boot.qcow2")
	s.Store = failCreateWorkloadNICStore{Store: mem}

	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/backups/artifacts/"+artID+"/restore", strings.NewReader(`{"mode":"new"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(req)
	raw, _ = io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusInternalServerError {
		t.Fatalf("nic persist %d %s", res.StatusCode, raw)
	}
	if !strings.Contains(string(raw), "could not record VM NIC") {
		t.Fatalf("nic persist body %s", raw)
	}
}
