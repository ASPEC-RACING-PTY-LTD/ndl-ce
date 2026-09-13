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
	"sync"
	"testing"
	"time"

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
	if action == qemu.BackupStatFS {
		return storage.CopyResult{Dest: dest, Size: 1 << 40, Format: "statfs"}, nil
	}
	if action == qemu.BackupRmTree {
		if dest != "" {
			_ = os.RemoveAll(dest)
		}
		return storage.CopyResult{Dest: dest, Format: "directory"}, nil
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
	if action == qemu.BackupExtractRoot {
		if dest != "" {
			_ = os.MkdirAll(dest, 0o755)
		}
		return storage.CopyResult{Dest: dest, Format: "directory"}, nil
	}
	if action == qemu.BackupWrite {
		if dest != "" {
			_ = os.MkdirAll(filepath.Dir(dest), 0o750)
			body := []byte("{}")
			if src != "" {
				if b, err := os.ReadFile(src); err == nil {
					body = b
				}
			}
			_ = os.WriteFile(dest, body, 0o600)
		}
		return storage.CopyResult{Dest: dest, Format: "json"}, nil
	}
	if strings.HasPrefix(action, qemu.BackupSyncTree) {
		if dest != "" {
			_ = os.MkdirAll(dest, 0o750)
		}
		return storage.CopyResult{Dest: dest, Format: "directory"}, nil
	}
	if action == qemu.BackupV2Capture || action == qemu.BackupV2Preview || action == qemu.BackupV2Status || action == qemu.BackupV2Restore || action == qemu.BackupV2Workspace || action == qemu.BackupV2Enqueue || action == qemu.BackupV2Expire {
		var req struct {
			WorkloadName string `json:"workload_name"`
			WorkloadID   string `json:"workload_id"`
			CaptureMode  string `json:"capture_mode"`
			Namespace    string `json:"namespace"`
			BackupID     string `json:"backup_id"`
			Dest         string `json:"dest"`
		}
		_ = json.Unmarshal([]byte(dest), &req)
		if action == qemu.BackupV2Restore {
			target := dest
			if req.Dest != "" {
				target = req.Dest
			}
			if src != "" {
				target = src
			}
			if target != "" && !strings.HasPrefix(strings.TrimSpace(target), "{") {
				_ = os.MkdirAll(target, 0o755)
			}
			extra, _ := json.Marshal(map[string]any{"backup_id": req.BackupID, "namespace": req.Namespace, "locator": target, "capture_mode": firstNonEmpty(req.CaptureMode, "smart")})
			return storage.CopyResult{Dest: target, Format: "ndl-cab", Extra: string(extra)}, nil
		}
		if action == qemu.BackupV2Preview || action == qemu.BackupV2Status || action == qemu.BackupV2Workspace {
			extra, _ := json.Marshal(map[string]any{
				"capture_mode": firstNonEmpty(req.CaptureMode, "smart"),
				"preview": map[string]any{
					"workload_id": req.WorkloadID, "workload_name": req.WorkloadName, "mode": firstNonEmpty(req.CaptureMode, "smart"),
					"items":           []map[string]any{{"id": "db:postgresql", "kind": "database", "label": "PostgreSQL", "paths": []string{"/var/lib/postgresql"}, "bytes": 4, "selected": true, "default_on": true}},
					"protected_bytes": 4, "excluded_bytes": 0, "full_bytes": 4,
				},
				"workspace": map[string]any{"root": "/var/lib/ndl/backup-repo", "repo_bytes": 0, "pending_uploads": 0},
			})
			return storage.CopyResult{Format: "ndl-cab", Extra: string(extra)}, nil
		}
		ns := req.WorkloadName
		if ns == "" {
			ns = "workload"
		}
		if req.WorkloadID != "" {
			ns = ns + "-" + strings.ReplaceAll(req.WorkloadID, "-", "")[:8]
		}
		extra, _ := json.Marshal(map[string]any{
			"backup_id": "11111111-1111-4111-8111-111111111111",
			"namespace": ns, "workload_id": req.WorkloadID, "workload_name": req.WorkloadName,
			"local_complete": true, "remote": "queued", "capture_mode": firstNonEmpty(req.CaptureMode, "smart"),
			"logical_bytes": 4, "physical_new_data": 4, "chunks_new": 1, "chunks_reused": 0,
			"locator":   "ndl-cab://backups/" + ns + "/11111111-1111-4111-8111-111111111111",
			"blueprint": map[string]any{"kind": "ndl-backup-blueprint", "name": req.WorkloadName, "capture_mode": firstNonEmpty(req.CaptureMode, "smart")},
		})
		return storage.CopyResult{Dest: "ndl-cab://backups/" + ns + "/", SHA256: "11111111-1111-4111-8111-111111111111", Size: 4, Format: "ndl-cab", Extra: string(extra)}, nil
	}
	if action == qemu.BackupPack {
		if dest != "" {
			_ = os.MkdirAll(dest, 0o750)
			_ = os.WriteFile(filepath.Join(dest, "manifest.json"), []byte(`{"version":1,"format":"ndlb"}`), 0o640)
			if src != "" {
				if b, err := os.ReadFile(filepath.Join(src, "config.json")); err == nil {
					_ = os.WriteFile(filepath.Join(dest, "config.json"), b, 0o600)
				}
			}
			_ = os.MkdirAll(filepath.Join(dest, "chunks"), 0o750)
			_ = os.WriteFile(filepath.Join(dest, "chunks", "000000"), []byte("qcow"), 0o640)
		}
		return storage.CopyResult{Dest: dest, SHA256: "abc123", Size: 4, Format: "ndlb"}, nil
	}
	if action == qemu.BackupUnpack {
		if dest != "" {
			_ = os.MkdirAll(filepath.Dir(dest), 0o750)
			_ = os.WriteFile(dest, []byte("qcow"), 0o640)
			if src != "" {
				if b, err := os.ReadFile(filepath.Join(src, "config.json")); err == nil {
					_ = os.WriteFile(dest+".ndl-meta.json", b, 0o600)
				}
			}
		}
		return storage.CopyResult{Dest: dest, SHA256: "abc123", Size: 4, Format: "tar"}, nil
	}
	format := "qcow2"
	if strings.HasPrefix(action, qemu.BackupArchive) {
		format = "tar.zst"
	}
	if dest != "" {
		if err := os.MkdirAll(filepath.Dir(dest), 0o750); err == nil {
			_ = os.WriteFile(dest, []byte("qcow"), 0o640)
		}
	}
	res := f.res
	if res.SHA256 == "" {
		res = storage.CopyResult{Dest: dest, SHA256: "abc123", Size: 4, Format: format}
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
	if !strings.Contains(strings.ToLower(string(b)), "unavailable") {
		t.Fatalf("unavailable NFS target: %s", b)
	}
	if strings.Contains(strings.ToLower(string(b)), "zfs") {
		t.Fatalf("directory CT must not require ZFS: %s", b)
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
	if len(runs) != 0 {
		t.Fatalf("first nightly tick must arm the schedule, not copy: %+v", runs)
	}
	pols, _ := mem.ListBackupPolicies(context.Background(), cluster.ID)
	if len(pols) != 1 {
		t.Fatalf("policy %+v", pols)
	}
	_ = mem.UpdateBackupPolicyLastRun(context.Background(), cluster.ID, pols[0].ID, time.Now().Add(-24*time.Hour))
	s.TickNightlyBackups(context.Background())
	runs, _ = mem.ListBackupRuns(context.Background(), cluster.ID)
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
	if res.StatusCode != http.StatusAccepted {
		t.Fatalf("extra data disk backup %d %s", res.StatusCode, raw)
	}
	var runOut map[string]any
	if err := json.Unmarshal(raw, &runOut); err != nil {
		t.Fatal(err)
	}
	plan, _ := runOut["plan"].(map[string]any)
	skipped, _ := plan["skipped"].([]any)
	if len(skipped) < 1 {
		t.Fatalf("extra disk must be skipped in plan %+v", runOut)
	}
	copied := false
	for _, c := range bk.copies {
		if c[0] == qemu.BackupCopy {
			copied = true
		}
	}
	if !copied {
		t.Fatalf("boot disk must still be copied: %+v", bk.copies)
	}
	runs, _ := mem.ListBackupRuns(context.Background(), cluster.ID)
	if len(runs) != 1 || runs[0].Status != appdb.BackupSucceededWithWarnings {
		t.Fatalf("backup must persist a run: %+v", runs)
	}
	arts, _ := mem.ListBackupArtifacts(context.Background(), cluster.ID)
	if len(arts) != 1 {
		t.Fatalf("backup must persist the boot artifact: %+v", arts)
	}
	gotWL, _ := mem.GetWorkload(context.Background(), cluster.ID, vmID)
	gotBoot, _ := mem.GetVolume(context.Background(), cluster.ID, bootVol)
	gotExtra, _ := mem.GetVolume(context.Background(), cluster.ID, extraVol)
	if gotWL == nil || gotWL.Status != beforeWL.Status || gotWL.NodeID != beforeWL.NodeID || string(gotWL.SpecJSON) != string(beforeWL.SpecJSON) {
		t.Fatalf("GET must keep the extra-disk VM untouched: before=%+v after=%+v", beforeWL, gotWL)
	}
	if gotExtra == nil || gotExtra.BackendRef != extraRef || gotExtra.Status != storage.StatusAvailable {
		t.Fatalf("extra volume must stay available at %s: %+v", extraRef, gotExtra)
	}
	gotDisks, _ := mem.ListWorkloadDisks(context.Background(), cluster.ID, vmID)
	if len(gotDisks) != len(disks) {
		t.Fatalf("disk catalog must stay %+v, got %+v", disks, gotDisks)
	}
	_ = gotBoot
	err := s.restoreReplaceVM(context.Background(), cluster.ID, *gotWL, appdb.BackupArtifact{ID: uuid.NewString()})
	if err == nil || !strings.Contains(strings.ToLower(err.Error()), "disk") {
		t.Fatalf("restore replace of extra data disks must stay refused: %v", err)
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
	pols, _ := mem.ListBackupPolicies(context.Background(), cluster.ID)
	if len(pols) != 1 {
		t.Fatalf("policy %+v", pols)
	}
	_ = mem.UpdateBackupPolicyLastRun(context.Background(), cluster.ID, pols[0].ID, time.Now().Add(-24*time.Hour))
	s.TickNightlyBackups(context.Background())
	runs, _ := mem.ListBackupRuns(context.Background(), cluster.ID)
	if len(runs) != 1 || runs[0].Status != appdb.BackupSucceededWithWarnings {
		t.Fatalf("nightly extra-disk backup %+v", runs)
	}
	arts, _ := mem.ListBackupArtifacts(context.Background(), cluster.ID)
	if len(arts) != 1 {
		t.Fatalf("nightly extra-disk backup must persist the boot artifact: %+v", arts)
	}
	plan := parseBackupPlan(runs[0].PlanJSON)
	if plan == nil || len(plan.Skipped) < 1 {
		t.Fatalf("nightly plan must skip extra disks: %+v", runs[0])
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
	if res.StatusCode != http.StatusAccepted {
		t.Fatalf("catalog extra data disk backup %d %s", res.StatusCode, raw)
	}
	runs, _ := mem.ListBackupRuns(context.Background(), cluster.ID)
	if len(runs) != 1 || runs[0].Status != appdb.BackupSucceededWithWarnings {
		t.Fatalf("catalog extra disk must persist a run: %+v", runs)
	}
	arts, _ := mem.ListBackupArtifacts(context.Background(), cluster.ID)
	if len(arts) != 1 {
		t.Fatalf("catalog extra disk must persist an artifact: %+v", arts)
	}
	plan := parseBackupPlan(runs[0].PlanJSON)
	if plan == nil || len(plan.Skipped) < 1 {
		t.Fatalf("catalog extra disk must be skipped in plan: %+v", runs[0])
	}
}

func TestRestoreNewVMSkipsExtraDataDisks(t *testing.T) {
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
	newID, err := s.restoreNewVM(context.Background(), cluster.ID, src, appdb.BackupArtifact{ID: uuid.NewString()}, false, &dest)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := mem.GetWorkload(context.Background(), cluster.ID, newID)
	if got == nil {
		t.Fatal("restored workload missing")
	}
	out, err := vmspec.Parse(got.SpecJSON)
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range out.Disks {
		if d.Role == vmspec.DiskRoleData {
			t.Fatalf("restore-as-new must not attach skipped extra disks: %+v", out.Disks)
		}
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

func TestBackupPolicyAllScopeDefaultAndSelectedMulti(t *testing.T) {
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
	createVM := func(name string) string {
		t.Helper()
		body := `{"name":"` + name + `","kind":"vm","pool_id":"` + poolID + `","network_id":"` + netID + `"}`
		req, _ := http.NewRequest("POST", ts.URL+"/api/v1/workloads", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
		res, err := ts.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		var vm map[string]any
		_ = json.NewDecoder(res.Body).Decode(&vm)
		_ = res.Body.Close()
		if res.StatusCode != http.StatusCreated {
			t.Fatalf("vm %s %d", name, res.StatusCode)
		}
		return vm["id"].(string)
	}
	a := createVM("policy-a")
	b := createVM("policy-b")
	dir := t.TempDir()
	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/backups/targets", strings.NewReader(`{"name":"local","kind":"local","locator":"`+dir+`"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ := ts.Client().Do(req)
	var tgt map[string]any
	_ = json.NewDecoder(res.Body).Decode(&tgt)
	_ = res.Body.Close()
	targetID := tgt["id"].(string)

	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/backups/policies", strings.NewReader(`{"name":"fleet","target_id":"`+targetID+`","schedule":"nightly","keep_daily":1,"keep_weekly":0,"keep_monthly":0}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(req)
	var allPol map[string]any
	_ = json.NewDecoder(res.Body).Decode(&allPol)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("omitted scope must require selected workloads, got %d %+v", res.StatusCode, allPol)
	}

	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/backups/policies", strings.NewReader(`{"name":"fleet","scope":"all","target_id":"`+targetID+`","schedule":"nightly","keep_daily":1,"keep_weekly":0,"keep_monthly":0}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(req)
	allPol = map[string]any{}
	_ = json.NewDecoder(res.Body).Decode(&allPol)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("explicit all policy %d %+v", res.StatusCode, allPol)
	}
	if allPol["scope"] != "all" {
		t.Fatalf("explicit all scope %+v", allPol["scope"])
	}
	if allPol["capture_mode"] != "smart" {
		t.Fatalf("default capture mode %+v", allPol["capture_mode"])
	}
	if _, ok := allPol["workload_id"]; ok {
		t.Fatalf("all policy must not encode a fake workload id %+v", allPol)
	}

	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/backups/policies", strings.NewReader(`{"name":"subset","scope":"selected","workload_ids":["`+a+`","`+b+`"],"target_id":"`+targetID+`","schedule":"nightly","keep_daily":1,"keep_weekly":0,"keep_monthly":0}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(req)
	raw, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("selected policy %d %s", res.StatusCode, raw)
	}
	var sel map[string]any
	if err := json.Unmarshal(raw, &sel); err != nil {
		t.Fatal(err)
	}
	ids, _ := sel["workload_ids"].([]any)
	if sel["scope"] != "selected" || len(ids) != 2 {
		t.Fatalf("selected %+v", sel)
	}

	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/backups/policies/"+sel["id"].(string)+"/run", strings.NewReader("{}"))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(req)
	var runOut map[string]any
	_ = json.NewDecoder(res.Body).Decode(&runOut)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusAccepted {
		t.Fatalf("run selected %d", res.StatusCode)
	}
	items, _ := runOut["items"].([]any)
	if len(items) != 2 {
		t.Fatalf("selected run items %+v", runOut)
	}

	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/backups/policies/"+allPol["id"].(string)+"/run", strings.NewReader("{}"))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(req)
	_ = json.NewDecoder(res.Body).Decode(&runOut)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusAccepted {
		t.Fatalf("run all %d", res.StatusCode)
	}
	items, _ = runOut["items"].([]any)
	if len(items) != 2 {
		t.Fatalf("all run should cover both VMs %+v", runOut)
	}

	c := createVM("policy-c")
	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/backups/policies/"+allPol["id"].(string)+"/run", strings.NewReader("{}"))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(req)
	_ = json.NewDecoder(res.Body).Decode(&runOut)
	_ = res.Body.Close()
	items, _ = runOut["items"].([]any)
	if len(items) != 3 {
		t.Fatalf("all scope must include future workload %s items=%d", c, len(items))
	}

	req, _ = http.NewRequest("PATCH", ts.URL+"/api/v1/backups/policies/"+sel["id"].(string), strings.NewReader(`{"name":"subset-renamed","scope":"selected","workload_ids":["`+a+`"],"target_id":"`+targetID+`","schedule":"nightly","keep_daily":2,"keep_weekly":0,"keep_monthly":0}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(req)
	var patched map[string]any
	_ = json.NewDecoder(res.Body).Decode(&patched)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK || patched["name"] != "subset-renamed" {
		t.Fatalf("patch %+v %d", patched, res.StatusCode)
	}

	req, _ = http.NewRequest("DELETE", ts.URL+"/api/v1/backups/policies/"+sel["id"].(string), nil)
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(req)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusNoContent {
		t.Fatalf("delete %d", res.StatusCode)
	}
	got, _ := mem.GetBackupPolicy(context.Background(), cluster.ID, sel["id"].(string))
	if got != nil {
		t.Fatalf("deleted policy still present")
	}
}

func TestBackupScopePreviewUsesV2AndDoesNotCapture(t *testing.T) {
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
	fb := &fakeBackup{}
	s.Backup = fb
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)
	ctBody := `{"name":"preview-ct","kind":"system-container","image_pin":"alpine/3.21/amd64/default","pool_id":"` + poolID + `","network_id":"` + netID + `","desired_power":"stopped"}`
	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/workloads", strings.NewReader(ctBody))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ := ts.Client().Do(req)
	var ct map[string]any
	_ = json.NewDecoder(res.Body).Decode(&ct)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("ct %d", res.StatusCode)
	}
	body := `{"workload_ids":["` + ct["id"].(string) + `"],"capture_mode":"smart"}`
	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/backups/scope-preview", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(req)
	raw, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("preview %d %s", res.StatusCode, raw)
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	if out["capture_mode"] != "smart" {
		t.Fatalf("preview mode %+v", out)
	}
	for _, c := range fb.copies {
		if c[0] == qemu.BackupV2Capture || strings.HasPrefix(c[0], qemu.BackupArchive) || c[0] == qemu.BackupPack {
			t.Fatalf("preview must not capture: %+v", fb.copies)
		}
	}
	saw := false
	for _, c := range fb.copies {
		if c[0] == qemu.BackupV2Preview {
			saw = true
		}
	}
	if !saw {
		t.Fatalf("expected v2-preview: %+v", fb.copies)
	}
}

func TestBackupPolicyAllScopeBacksUpDirectoryContainers(t *testing.T) {
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
	_ = res.Body.Close()
	dir := t.TempDir()
	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/backups/targets", strings.NewReader(`{"name":"local","kind":"local","locator":"`+dir+`"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(req)
	var tgt map[string]any
	_ = json.NewDecoder(res.Body).Decode(&tgt)
	_ = res.Body.Close()
	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/backups/policies", strings.NewReader(`{"name":"fleet","scope":"all","target_id":"`+tgt["id"].(string)+`","schedule":"nightly","keep_daily":1,"keep_weekly":0,"keep_monthly":0}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(req)
	var pol map[string]any
	raw, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("policy %d %s", res.StatusCode, raw)
	}
	if err := json.Unmarshal(raw, &pol); err != nil {
		t.Fatal(err)
	}
	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/backups/policies/"+pol["id"].(string)+"/run", strings.NewReader("{}"))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(req)
	body, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusAccepted {
		t.Fatalf("run %d %s", res.StatusCode, body)
	}
	var runOut map[string]any
	if err := json.Unmarshal(body, &runOut); err != nil {
		t.Fatal(err)
	}
	items, _ := runOut["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("all scope must backup directory CT %+v", runOut)
	}
	runs, _ := mem.ListBackupRuns(context.Background(), cluster.ID)
	if len(runs) != 1 || runs[0].Status != appdb.BackupSucceeded {
		t.Fatalf("directory CT run %+v", runs)
	}
	arts, _ := mem.ListBackupArtifacts(context.Background(), cluster.ID)
	if len(arts) != 1 || arts[0].Format != "ndl-cab" {
		t.Fatalf("directory CT artifact %+v", arts)
	}
	if !strings.Contains(arts[0].Locator, "backups/alpine-a") {
		t.Fatalf("directory locator must be human-readable: %s", arts[0].Locator)
	}
	plan := parseBackupPlan(runs[0].PlanJSON)
	if plan == nil || plan.Method != appdb.BackupMethodContentAddressed {
		t.Fatalf("content-addressed plan %+v", runs[0].PlanJSON)
	}
	if plan.Consistency != appdb.BackupConsistencyLiveCopy && plan.Consistency != appdb.BackupConsistencyStopped {
		t.Fatalf("directory plan must not use the freezer: %+v", plan)
	}
	if strings.Contains(strings.ToLower(plan.Warning), "frozen") {
		t.Fatalf("directory plan must not describe a freeze: %+v", plan)
	}
}

func TestBackupDirectoryContainerRestoreAsNew(t *testing.T) {
	s, mem, token := testServer(t)
	cluster, _ := mem.GetCluster(context.Background())
	nodeID := uuid.NewString()
	_ = mem.UpsertNode(context.Background(), appdb.Node{ID: nodeID, ClusterID: cluster.ID, Name: "local"})
	poolID, netID := seedCompute(t, mem, cluster.ID, nodeID)
	s.Storage = fakeStorage{vol: storage.CreateVolumeResult{Handle: storage.VolumeHandle{
		BackendType: storage.BackendDirectory, BackendRef: "volumes/container-root/x",
		Kind: storage.KindFilesystem, Class: storage.ClassContainerRoot, Format: storage.FormatDirectory,
	}}}
	fw := &fakeWorkloads{}
	s.Workloads = fw
	s.VM = &fakeVM{}
	fb := &fakeBackup{}
	s.Backup = fb
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)
	ctBody := `{"name":"ndl-backup-ct-test","kind":"system-container","image_pin":"alpine/3.21/amd64/default","pool_id":"` + poolID + `","network_id":"` + netID + `","desired_power":"stopped"}`
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
	dir := t.TempDir()
	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/backups/targets", strings.NewReader(`{"name":"local","kind":"local","locator":"`+dir+`"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(req)
	var tgt map[string]any
	_ = json.NewDecoder(res.Body).Decode(&tgt)
	_ = res.Body.Close()
	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/backups/run", strings.NewReader(`{"workload_id":"`+ct["id"].(string)+`","target_id":"`+tgt["id"].(string)+`"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(req)
	raw, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusAccepted {
		t.Fatalf("backup %d %s", res.StatusCode, raw)
	}
	captured := false
	for _, c := range fb.copies {
		if c[0] == qemu.BackupV2Capture {
			captured = true
		}
		if c[0] == qemu.BackupPack || strings.HasPrefix(c[0], qemu.BackupSyncTree) || strings.HasPrefix(c[0], qemu.BackupArchive) {
			t.Fatalf("new Directory CT backups must not use tar/pack: %+v", fb.copies)
		}
	}
	if !captured {
		t.Fatalf("Directory CT backup must use Backup Engine V2 capture: %+v", fb.copies)
	}
	arts, _ := mem.ListBackupArtifacts(context.Background(), cluster.ID)
	if len(arts) != 1 {
		t.Fatalf("artifact %+v", arts)
	}
	createsBefore := fw.creates
	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/backups/artifacts/"+arts[0].ID+"/restore", strings.NewReader(`{"mode":"new"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(req)
	raw, _ = io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusAccepted {
		t.Fatalf("restore %d %s", res.StatusCode, raw)
	}
	if fw.creates <= createsBefore {
		t.Fatal("restore-as-new must CreateCT")
	}
	if !fw.lastSpec.SkipImage {
		t.Fatalf("restore must extract the archive, not reinstall the image: %+v", fw.lastSpec)
	}
	if !fw.lastSpec.NoStart {
		t.Fatalf("restore of a stopped container must not start: %+v", fw.lastSpec)
	}
}

func TestAdhocBackupHonorsCaptureMode(t *testing.T) {
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
	fb := &fakeBackup{}
	s.Backup = fb
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)
	ctBody := `{"name":"adhoc-full","kind":"system-container","image_pin":"alpine/3.21/amd64/default","pool_id":"` + poolID + `","network_id":"` + netID + `","desired_power":"stopped"}`
	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/workloads", strings.NewReader(ctBody))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ := ts.Client().Do(req)
	var ct map[string]any
	_ = json.NewDecoder(res.Body).Decode(&ct)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("ct %d", res.StatusCode)
	}
	dir := t.TempDir()
	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/backups/targets", strings.NewReader(`{"name":"local","kind":"local","locator":"`+dir+`"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(req)
	var tgt map[string]any
	_ = json.NewDecoder(res.Body).Decode(&tgt)
	_ = res.Body.Close()
	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/backups/run", strings.NewReader(`{"workload_id":"`+ct["id"].(string)+`","target_id":"`+tgt["id"].(string)+`","capture_mode":"full"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(req)
	raw, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusAccepted {
		t.Fatalf("adhoc full %d %s", res.StatusCode, raw)
	}
	arts, _ := mem.ListBackupArtifacts(context.Background(), cluster.ID)
	if len(arts) != 1 || arts[0].CaptureMode != appdb.BackupCaptureFull {
		t.Fatalf("adhoc full artifact %+v", arts)
	}
	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/backups/run", strings.NewReader(`{"workload_id":"`+ct["id"].(string)+`","target_id":"`+tgt["id"].(string)+`","capture_mode":"bogus"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(req)
	raw, _ = io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusBadRequest || !strings.Contains(string(raw), "capture_mode") {
		t.Fatalf("bogus capture_mode %d %s", res.StatusCode, raw)
	}
}

type gateBackup struct {
	fakeBackup
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

func (g *gateBackup) CopyBackup(ctx context.Context, action, src, dest string) (storage.CopyResult, error) {
	if action == qemu.BackupCopy {
		g.once.Do(func() { close(g.started) })
		select {
		case <-g.release:
		case <-ctx.Done():
			return storage.CopyResult{}, ctx.Err()
		}
	}
	return g.fakeBackup.CopyBackup(ctx, action, src, dest)
}

type clockBackup struct {
	fakeBackup
	advance func()
}

func (c *clockBackup) CopyBackup(ctx context.Context, action, src, dest string) (storage.CopyResult, error) {
	res, err := c.fakeBackup.CopyBackup(ctx, action, src, dest)
	if action == qemu.BackupCopy && c.advance != nil {
		c.advance()
	}
	return res, err
}

func TestBackupPolicyRunRejectsSecondExecution(t *testing.T) {
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
	body := `{"name":"lock","kind":"vm","pool_id":"` + poolID + `","network_id":"` + netID + `"}`
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
	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/backups/policies", strings.NewReader(`{"name":"lock","scope":"selected","workload_ids":["`+vm["id"].(string)+`"],"target_id":"`+tgt["id"].(string)+`","schedule":"nightly","keep_daily":1,"keep_weekly":0,"keep_monthly":0}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(req)
	var pol map[string]any
	_ = json.NewDecoder(res.Body).Decode(&pol)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("policy %d", res.StatusCode)
	}

	first := make(chan int, 1)
	go func() {
		req, _ := http.NewRequest("POST", ts.URL+"/api/v1/backups/policies/"+pol["id"].(string)+"/run", strings.NewReader("{}"))
		req.Header.Set("Content-Type", "application/json")
		req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
		res, err := ts.Client().Do(req)
		if err != nil {
			first <- 0
			return
		}
		_ = res.Body.Close()
		first <- res.StatusCode
	}()
	select {
	case <-gate.started:
	case <-time.After(5 * time.Second):
		t.Fatal("first run did not start")
	}
	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/backups/policies/"+pol["id"].(string)+"/run", strings.NewReader("{}"))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(req)
	raw, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusConflict {
		t.Fatalf("second run %d %s", res.StatusCode, raw)
	}
	if !strings.Contains(string(raw), "already running") {
		t.Fatalf("second run body %s", raw)
	}
	close(gate.release)
	if code := <-first; code != http.StatusAccepted {
		t.Fatalf("first run %d", code)
	}
}

func TestBackupPolicyLastRunAtIsPolicyStart(t *testing.T) {
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
	start := time.Date(2026, 9, 12, 1, 0, 0, 0, time.UTC)
	now := start
	s.Now = func() time.Time { return now }
	s.Backup = &clockBackup{advance: func() { now = now.Add(2 * time.Hour) }}
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)
	createVM := func(name string) string {
		t.Helper()
		body := `{"name":"` + name + `","kind":"vm","pool_id":"` + poolID + `","network_id":"` + netID + `"}`
		req, _ := http.NewRequest("POST", ts.URL+"/api/v1/workloads", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
		res, _ := ts.Client().Do(req)
		var vm map[string]any
		_ = json.NewDecoder(res.Body).Decode(&vm)
		_ = res.Body.Close()
		if res.StatusCode != http.StatusCreated {
			t.Fatalf("vm %s %d", name, res.StatusCode)
		}
		return vm["id"].(string)
	}
	a := createVM("sched-a")
	b := createVM("sched-b")
	dir := t.TempDir()
	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/backups/targets", strings.NewReader(`{"name":"local","kind":"local","locator":"`+dir+`"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ := ts.Client().Do(req)
	var tgt map[string]any
	_ = json.NewDecoder(res.Body).Decode(&tgt)
	_ = res.Body.Close()
	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/backups/policies", strings.NewReader(`{"name":"sched","scope":"selected","workload_ids":["`+a+`","`+b+`"],"target_id":"`+tgt["id"].(string)+`","schedule":"nightly","keep_daily":1,"keep_weekly":0,"keep_monthly":0}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(req)
	var pol map[string]any
	_ = json.NewDecoder(res.Body).Decode(&pol)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("policy %d", res.StatusCode)
	}
	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/backups/policies/"+pol["id"].(string)+"/run", strings.NewReader("{}"))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(req)
	raw, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusAccepted {
		t.Fatalf("run %d %s", res.StatusCode, raw)
	}
	got, _ := mem.GetBackupPolicy(context.Background(), cluster.ID, pol["id"].(string))
	if got == nil || got.LastRunAt == nil || !got.LastRunAt.Equal(start) {
		t.Fatalf("last_run_at must stay at policy start %s, got %+v", start, got)
	}
	if !now.After(start.Add(3 * time.Hour)) {
		t.Fatalf("clock must have advanced across workloads, now=%s", now)
	}
}

func TestReconcileInterruptedBackupRuns(t *testing.T) {
	s, mem, _ := testServer(t)
	cluster, _ := mem.GetCluster(context.Background())
	now := time.Date(2026, 9, 12, 8, 0, 0, 0, time.UTC)
	s.Now = func() time.Time { return now }
	run := appdb.BackupRun{
		ID: uuid.NewString(), ClusterID: cluster.ID, PolicyID: uuid.NewString(),
		TargetID: uuid.NewString(), WorkloadID: uuid.NewString(),
		Status: appdb.BackupRunning, StartedAt: now.Add(-time.Hour),
	}
	if err := mem.CreateBackupRun(context.Background(), run); err != nil {
		t.Fatal(err)
	}
	done := appdb.BackupRun{
		ID: uuid.NewString(), ClusterID: cluster.ID,
		TargetID: run.TargetID, WorkloadID: uuid.NewString(),
		Status: appdb.BackupSucceeded, StartedAt: now.Add(-2 * time.Hour),
	}
	if err := mem.CreateBackupRun(context.Background(), done); err != nil {
		t.Fatal(err)
	}
	if n := s.ReconcileInterruptedBackupRuns(context.Background()); n != 1 {
		t.Fatalf("reconciled %d", n)
	}
	got, _ := mem.GetBackupRun(context.Background(), cluster.ID, run.ID)
	if got == nil || got.Status != appdb.BackupInterrupted || got.Error != backupInterruptedError || got.FinishedAt == nil {
		t.Fatalf("run %+v", got)
	}
	kept, _ := mem.GetBackupRun(context.Background(), cluster.ID, done.ID)
	if kept == nil || kept.Status != appdb.BackupSucceeded {
		t.Fatalf("succeeded run must stay %+v", kept)
	}
}
