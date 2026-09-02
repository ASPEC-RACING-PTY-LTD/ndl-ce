package httpapi

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/no-dal/ndl-ce/internal/appdb"
	"github.com/no-dal/ndl-ce/internal/objstore"
	"github.com/no-dal/ndl-ce/internal/rbac"
	"github.com/no-dal/ndl-ce/internal/secutil"
	"github.com/no-dal/ndl-ce/internal/storage"
)

type fakeObject struct {
	eng *objstore.Engine
	mem *objstore.MemoryTransport
}

func newFakeObject() *fakeObject {
	mem := objstore.NewMemoryTransport()
	return &fakeObject{mem: mem, eng: &objstore.Engine{Transport: mem}}
}

func (f *fakeObject) ObjectBackup(ctx context.Context, req objstore.Request) (objstore.Result, error) {
	if req.Action == objstore.ActionPut && req.SourcePath != "" {
		if _, err := os.Stat(req.SourcePath); err != nil {
			_ = os.WriteFile(req.SourcePath, []byte("qcow-fixture-bytes"), 0o600)
		}
	}
	return f.eng.Do(ctx, req)
}

func TestPhase23ObjectTargetEncryptsAndRestores(t *testing.T) {
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
	fo := newFakeObject()
	s.Object = fo
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close)
	cookie := claimAdmin(t, ts, token)

	body := `{"name":"web","kind":"vm","pool_id":"` + poolID + `","network_id":"` + netID + `","cpus":1,"memory_bytes":268435456}`
	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/workloads", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("workload %d %s", res.StatusCode, raw)
	}
	var wl map[string]any
	_ = json.Unmarshal(raw, &wl)
	wlID, _ := wl["id"].(string)

	tgtBody, _ := json.Marshal(map[string]any{
		"name": "r2", "kind": "r2", "endpoint": "https://account.r2.cloudflarestorage.com",
		"bucket": "ndl-backups", "prefix": "node1", "username": "akid", "password": "secret",
		"no_check_bucket": true,
	})
	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/backups/targets", strings.NewReader(string(tgtBody)))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, err = ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ = io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("target %d %s", res.StatusCode, raw)
	}
	if strings.Contains(string(raw), "secret") || strings.Contains(string(raw), `"password"`) {
		t.Fatalf("secret leaked: %s", raw)
	}
	var tgt map[string]any
	_ = json.Unmarshal(raw, &tgt)
	if tgt["status"] == "available" {
		t.Fatal("no_check_bucket must not invent available")
	}
	if tgt["has_encryption_key"] != true {
		t.Fatalf("encryption flag %s", raw)
	}
	if strings.Contains(string(raw), `"encryption_key"`) {
		t.Fatalf("encryption key leaked: %s", raw)
	}
	tgtID, _ := tgt["id"].(string)
	_, enc, err := mem.BackupCredentials(context.Background(), cluster.ID, tgtID)
	if err != nil || enc == "" {
		t.Fatal("encryption key must be stored in secrets")
	}

	runBody, _ := json.Marshal(map[string]any{"workload_id": wlID, "target_id": tgtID})
	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/backups/run", strings.NewReader(string(runBody)))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, err = ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ = io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusAccepted {
		t.Fatalf("run %d %s", res.StatusCode, raw)
	}
	var run map[string]any
	_ = json.Unmarshal(raw, &run)
	if run["status"] != appdb.BackupSucceeded {
		t.Fatalf("run status %s", raw)
	}
	if run["transferred_bytes"] == nil {
		t.Fatalf("transferred_bytes missing %s", raw)
	}

	arts, _ := mem.ListBackupArtifacts(context.Background(), cluster.ID)
	if len(arts) != 1 || !arts[0].Encrypted || !strings.HasPrefix(arts[0].Locator, "s3://") {
		t.Fatalf("artifact %+v", arts)
	}
	cipher := fo.mem.Ciphertext("ndl-backups", arts[0].ObjectKey)
	if !bytes.HasPrefix(cipher, []byte(objstore.Magic)) {
		t.Fatal("bucket object must be NDLE ciphertext")
	}
	if bytes.Contains(cipher, []byte("qcow")) {
		t.Fatal("plaintext must not appear in the bucket")
	}

	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/backups/artifacts/"+arts[0].ID+"/restore", strings.NewReader(`{"mode":"new"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, err = ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ = io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusAccepted {
		t.Fatalf("restore %d %s", res.StatusCode, raw)
	}
	var restored map[string]any
	_ = json.Unmarshal(raw, &restored)
	if restored["restored_workload_id"] == nil || restored["restored_workload_id"] == wlID {
		t.Fatalf("restore must mint a new UUID: %s", raw)
	}

	req, _ = http.NewRequest("GET", ts.URL+"/api/v1/backups/targets", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, err = ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	listed, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if strings.Contains(string(listed), `"password"`) || strings.Contains(string(listed), `"encryption_key"`) || strings.Contains(string(listed), "secret") {
		t.Fatalf("list leaked secrets: %s", listed)
	}
}

func TestPhase23ObjectTargetViewerDenied(t *testing.T) {
	s, mem, token := testServer(t)
	cluster, _ := mem.GetCluster(context.Background())
	_ = mem.UpsertNode(context.Background(), appdb.Node{ID: uuid.NewString(), ClusterID: cluster.ID, Name: "local"})
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close)
	_ = claimAdmin(t, ts, token)
	view := appdb.User{ID: uuid.NewString(), ClusterID: cluster.ID, Username: "view"}
	_ = mem.CreateUser(context.Background(), view)
	_ = mem.BindRole(context.Background(), cluster.ID, view.ID, rbac.Viewer)
	plain := "ndl_view_backup"
	_ = mem.CreateToken(context.Background(), appdb.APIToken{
		ID: uuid.NewString(), ClusterID: cluster.ID, UserID: view.ID, Name: "v",
		TokenHash: hashToken(plain), Prefix: "ndl_vb",
	})
	body := `{"name":"r2","kind":"r2","endpoint":"https://account.r2.cloudflarestorage.com","bucket":"b","username":"a","password":"p","no_check_bucket":true}`
	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/backups/targets", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+plain)
	res, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("viewer %d", res.StatusCode)
	}
}

func TestPhase23MinIOHTTPFixtureAllowed(t *testing.T) {
	s, _, token := testServer(t)
	s.Object = newFakeObject()
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close)
	cookie := claimAdmin(t, ts, token)
	body := `{"name":"minio","kind":"minio","endpoint":"http://127.0.0.1:9000","bucket":"ndl","username":"minio","password":"minio123","no_check_bucket":true}`
	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/backups/targets", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusCreated {
		b, _ := io.ReadAll(res.Body)
		t.Fatalf("minio %d %s", res.StatusCode, b)
	}
}

func TestPhase23R2HTTPEndpointRejected(t *testing.T) {
	s, _, token := testServer(t)
	s.Object = newFakeObject()
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close)
	cookie := claimAdmin(t, ts, token)
	body := `{"name":"r2","kind":"r2","endpoint":"http://example.invalid","bucket":"ndl","username":"akid","password":"secret","no_check_bucket":true}`
	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/backups/targets", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusBadRequest {
		b, _ := io.ReadAll(res.Body)
		t.Fatalf("http r2 %d %s", res.StatusCode, b)
	}
}

type sizedZFS struct {
	fakeZFS
}

func (f *sizedZFS) ZFSPool(ctx context.Context, op storage.ZFSOp) (storage.ZFSResult, error) {
	res, err := f.fakeZFS.ZFSPool(ctx, op)
	if err != nil {
		return res, err
	}
	if op.Action == "send" && op.DestPath != "" {
		n := 32 << 10
		if op.FromSnap != "" {
			n = 1024
		}
		buf := make([]byte, n)
		if _, err := rand.Read(buf); err != nil {
			return res, err
		}
		_ = os.MkdirAll(filepath.Dir(op.DestPath), 0o750)
		_ = os.WriteFile(op.DestPath, buf, 0o600)
	}
	return res, nil
}

func TestPhase23ZFSIncrementalTransfersLess(t *testing.T) {
	s, mem, token := testServer(t)
	cluster, _ := mem.GetCluster(context.Background())
	node := seedNode(t, mem, cluster.ID, debianInv(), false)
	caps, _ := json.Marshal(storage.ZFSCapabilities())
	pool := appdb.StoragePool{
		ID: uuid.NewString(), ClusterID: cluster.ID, NodeID: node.ID, Name: "tank",
		BackendType: storage.BackendZFS, Status: storage.StatusAvailable, RootPath: storage.ZFSMountRoot + "/1",
		Capabilities: caps,
	}
	_ = mem.CreateStoragePool(context.Background(), pool)
	_ = mem.UpsertZFSPool(context.Background(), appdb.ZFSPool{PoolID: pool.ID, ZPoolGUID: "1", ZPoolName: "tank"})
	vol := appdb.Volume{
		ID: uuid.NewString(), ClusterID: cluster.ID, NodeID: node.ID, PoolID: pool.ID,
		Class: storage.ClassVMDisk, Kind: storage.KindBlock, Format: storage.FormatZvol, SizeBytes: 1 << 30,
		Status: storage.StatusAvailable, BackendType: storage.BackendZFS, BackendRef: "/dev/zvol/tank/" + uuid.NewString(),
	}
	_ = mem.CreateVolume(context.Background(), vol)
	wl := appdb.Workload{ID: uuid.NewString(), ClusterID: cluster.ID, Name: "vm", Kind: "vm", Status: "stopped"}
	_ = mem.CreateWorkload(context.Background(), wl)
	_ = mem.CreateWorkloadDisk(context.Background(), appdb.WorkloadDisk{ID: uuid.NewString(), ClusterID: cluster.ID, WorkloadID: wl.ID, VolumeID: vol.ID, Role: "boot"})
	zfs := &sizedZFS{}
	s.ZFS = zfs
	s.Backup = &fakeBackup{}
	s.Object = newFakeObject()
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close)
	cookie := claimAdmin(t, ts, token)

	tgtBody, _ := json.Marshal(map[string]any{
		"name": "r2", "kind": "r2", "endpoint": "https://account.r2.cloudflarestorage.com",
		"bucket": "ndl-backups", "username": "akid", "password": "secret", "no_check_bucket": true,
	})
	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/backups/targets", strings.NewReader(string(tgtBody)))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("target %d %s", res.StatusCode, raw)
	}
	var tgt map[string]any
	_ = json.Unmarshal(raw, &tgt)
	tgtID, _ := tgt["id"].(string)

	runOnce := func() map[string]any {
		runBody, _ := json.Marshal(map[string]any{"workload_id": wl.ID, "target_id": tgtID})
		req, _ := http.NewRequest("POST", ts.URL+"/api/v1/backups/run", strings.NewReader(string(runBody)))
		req.Header.Set("Content-Type", "application/json")
		req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
		res, err := ts.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := io.ReadAll(res.Body)
		_ = res.Body.Close()
		if res.StatusCode != http.StatusAccepted {
			t.Fatalf("run %d %s", res.StatusCode, raw)
		}
		var run map[string]any
		_ = json.Unmarshal(raw, &run)
		if run["status"] != appdb.BackupSucceeded {
			t.Fatalf("run status %s", raw)
		}
		return run
	}
	first := runOnce()
	second := runOnce()
	var fromSnap string
	for _, op := range zfs.calls {
		if op.Action == "send" && op.FromSnap != "" {
			fromSnap = op.FromSnap
		}
	}
	if fromSnap == "" {
		t.Fatal("second ZFS send must use send -i FromSnap")
	}
	firstBytes, _ := first["transferred_bytes"].(float64)
	secondBytes, _ := second["transferred_bytes"].(float64)
	if secondBytes >= firstBytes || secondBytes == 0 {
		t.Fatalf("incremental should transfer less: first=%v second=%v", first["transferred_bytes"], second["transferred_bytes"])
	}
	if second["incremental"] != true {
		t.Fatalf("second run must be incremental: %v", second)
	}
}

func hashToken(plain string) string {
	return secutil.HashSHA256(plain)
}
