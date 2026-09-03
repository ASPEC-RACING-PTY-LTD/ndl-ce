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
	"time"

	"github.com/google/uuid"
	"github.com/no-dal/ndl-ce/internal/appdb"
	"github.com/no-dal/ndl-ce/internal/auth"
	"github.com/no-dal/ndl-ce/internal/migration"
	"github.com/no-dal/ndl-ce/internal/ndnet"
	"github.com/no-dal/ndl-ce/internal/rbac"
	"github.com/no-dal/ndl-ce/internal/storage"
)

func TestMigrationViewerDeniedAndSecretsRedacted(t *testing.T) {
	s, mem, token := testServer(t)
	cluster, _ := mem.GetCluster(context.Background())
	_ = mem.UpsertNode(context.Background(), appdb.Node{ID: uuid.NewString(), ClusterID: cluster.ID, Name: "local"})
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	admin := claimAdmin(t, ts, token)
	hash, _ := auth.HashPassword("password1")
	viewer := appdb.User{ID: uuid.NewString(), ClusterID: cluster.ID, Username: "view", PasswordHash: hash}
	_ = mem.CreateUser(context.Background(), viewer)
	_ = mem.BindRole(context.Background(), cluster.ID, viewer.ID, rbac.Viewer)
	viewCookie := loginAs(t, ts, "view", "password1")

	req, _ := http.NewRequest("GET", ts.URL+"/api/v1/migration/adapters", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: viewCookie})
	res, _ := ts.Client().Do(req)
	raw, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK || !strings.Contains(string(raw), `"proxmox"`) {
		t.Fatalf("viewer adapters %d %s", res.StatusCode, raw)
	}

	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/migration/sources", strings.NewReader(`{"adapter":"proxmox","endpoint":"https://pve.example:8006","token":"SECRET-TOKEN-VALUE"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: viewCookie})
	res, _ = ts.Client().Do(req)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("viewer create source %d", res.StatusCode)
	}

	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/migration/sources", strings.NewReader(`{"adapter":"proxmox","endpoint":"https://pve.example:8006","token":"SECRET-TOKEN-VALUE"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: admin})
	res, _ = ts.Client().Do(req)
	body, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("create source %d %s", res.StatusCode, body)
	}
	if strings.Contains(string(body), "SECRET-TOKEN-VALUE") {
		t.Fatalf("token leaked %s", body)
	}

	req, _ = http.NewRequest("GET", ts.URL+"/api/v1/migration/sources", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: admin})
	res, _ = ts.Client().Do(req)
	body, _ = io.ReadAll(res.Body)
	_ = res.Body.Close()
	if strings.Contains(string(body), "SECRET-TOKEN-VALUE") {
		t.Fatalf("list leaked token %s", body)
	}

	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/migration/jobs", strings.NewReader(`{"adapter":"disk","mode":"disk","path":"/tmp/x.qcow2","delete_source":true}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: admin})
	res, _ = ts.Client().Do(req)
	body, _ = io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusBadRequest || !strings.Contains(string(body), "source destruction") {
		t.Fatalf("delete_source %d %s", res.StatusCode, body)
	}
}

func TestMigrationSourceEndpointRefusesCredentials(t *testing.T) {
	s, mem, token := testServer(t)
	cluster, _ := mem.GetCluster(context.Background())
	_ = mem.UpsertNode(context.Background(), appdb.Node{ID: uuid.NewString(), ClusterID: cluster.ID, Name: "local"})
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	admin := claimAdmin(t, ts, token)
	for _, body := range []string{
		`{"adapter":"proxmox","endpoint":"https://user:SECRET-TOKEN-VALUE@pve.example:8006","token":"SECRET-TOKEN-VALUE"}`,
		`{"adapter":"proxmox","endpoint":"file:///etc/passwd","token":"SECRET-TOKEN-VALUE"}`,
	} {
		req, _ := http.NewRequest("POST", ts.URL+"/api/v1/migration/sources", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.AddCookie(&http.Cookie{Name: sessionCookie, Value: admin})
		res, _ := ts.Client().Do(req)
		raw, _ := io.ReadAll(res.Body)
		_ = res.Body.Close()
		if res.StatusCode != http.StatusBadRequest {
			t.Fatalf("status %d %s for %s", res.StatusCode, raw, body)
		}
		if strings.Contains(string(raw), "SECRET-TOKEN-VALUE") {
			t.Fatalf("secret echoed %s", raw)
		}
	}
}

func TestMigrationImportPathMustStayJailed(t *testing.T) {
	_, _, ts, cookie, _, poolID, netID := phase18Ready(t)
	post := func(body string) (int, string) {
		t.Helper()
		req, _ := http.NewRequest("POST", ts.URL+"/api/v1/migration/import/disk", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
		res, err := ts.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := io.ReadAll(res.Body)
		_ = res.Body.Close()
		return res.StatusCode, string(raw)
	}
	base := `,"mode":"disk","name":"imported-guest","kind":"vm","cpus":2,"memory_bytes":536870912,"firmware":"bios","pool_id":"` + poolID + `","network_id":"` + netID + `"}`
	code, raw := post(`{"path":"/etc/passwd"` + base)
	if code != http.StatusBadRequest || !strings.Contains(raw, "image path") {
		t.Fatalf("passwd path %d %s", code, raw)
	}
	code, raw = post(`{"xml_path":"/etc/ndl/host.key"` + base)
	if code != http.StatusBadRequest || !strings.Contains(raw, "image path") {
		t.Fatalf("host.key xml_path %d %s", code, raw)
	}
	tmp := filepath.Join("/tmp", "ndl-mig-src-"+uuid.NewString()+".qcow2")
	if err := os.WriteFile(tmp, []byte("qcow-data"), 0o640); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(tmp) })
	code, raw = post(`{"path":"` + tmp + `"` + base)
	if code != http.StatusAccepted {
		t.Fatalf("jailed tmp import %d %s", code, raw)
	}
	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/migration/jobs", strings.NewReader(`{"adapter":"disk","mode":"disk","path":"/tmp/x.qcow2","delete_source":true}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ := ts.Client().Do(req)
	body, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusBadRequest || !strings.Contains(string(body), "source destruction") {
		t.Fatalf("delete_source %d %s", res.StatusCode, body)
	}
}

func TestMigrationOfflineRunningAndLiveAck(t *testing.T) {
	s, mem, token := testServer(t)
	cluster, _ := mem.GetCluster(context.Background())
	_ = mem.UpsertNode(context.Background(), appdb.Node{ID: uuid.NewString(), ClusterID: cluster.ID, Name: "local"})
	poolID, netID := seedCompute(t, mem, cluster.ID, memNodeID(t, mem, cluster.ID))
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)

	pve := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch {
		case strings.HasSuffix(r.URL.Path, "/nodes"):
			_, _ = w.Write([]byte(`{"data":[{"node":"pve1"}]}`))
		case strings.Contains(r.URL.Path, "/qemu") && !strings.Contains(r.URL.Path, "/config") && !strings.Contains(r.URL.Path, "/snapshot"):
			_, _ = w.Write([]byte(`{"data":[{"vmid":100,"name":"win","status":"running","cpus":2,"maxmem":4294967296,"maxdisk":10737418240}]}`))
		case strings.Contains(r.URL.Path, "/lxc") && !strings.Contains(r.URL.Path, "/config"):
			_, _ = w.Write([]byte(`{"data":[]}`))
		case strings.Contains(r.URL.Path, "/storage") && strings.Contains(r.URL.Path, "/content"):
			_, _ = w.Write([]byte(`{"data":[]}`))
		case strings.Contains(r.URL.Path, "/storage"):
			_, _ = w.Write([]byte(`{"data":[{"storage":"local","type":"dir"}]}`))
		case strings.Contains(r.URL.Path, "/config"):
			_, _ = w.Write([]byte(`{"data":{"name":"win","cores":2,"memory":4096,"scsi0":"local:100/vm-100-disk-0.qcow2,size=10G","net0":"virtio=AA:BB:CC:DD:EE:FF,bridge=vmbr0"}}`))
		default:
			_, _ = w.Write([]byte(`{"data":[]}`))
		}
	}))
	defer pve.Close()

	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/migration/sources", strings.NewReader(`{"adapter":"proxmox","endpoint":"`+pve.URL+`","token":"user@pam!tok=secret","insecure":true}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ := ts.Client().Do(req)
	var src map[string]any
	_ = json.NewDecoder(res.Body).Decode(&src)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("source %d", res.StatusCode)
	}
	id, _ := src["id"].(string)

	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/migration/sources/"+id+"/discover", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(req)
	raw, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK || !strings.Contains(string(raw), "win") {
		t.Fatalf("discover %d %s", res.StatusCode, raw)
	}

	planBody := `{"source_id":"` + id + `","selected":["pve1/100"],"modes":{"pve1/100":"offline"},"mapping":{"storage":{"local":"` + poolID + `"},"network":{"vmbr0":"` + netID + `"}}}`
	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/migration/jobs", strings.NewReader(planBody))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(req)
	raw, _ = io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusUnprocessableEntity || !strings.Contains(string(raw), "stopped") {
		t.Fatalf("offline running %d %s", res.StatusCode, raw)
	}

	planBody = `{"source_id":"` + id + `","selected":["pve1/100"],"modes":{"pve1/100":"live"},"mapping":{"storage":{"local":"` + poolID + `"},"network":{"vmbr0":"` + netID + `"}}}`
	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/migration/jobs", strings.NewReader(planBody))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(req)
	raw, _ = io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusUnprocessableEntity || !strings.Contains(strings.ToLower(string(raw)), "acknowledgement") && !strings.Contains(strings.ToLower(string(raw)), "unavailable") {
		t.Fatalf("live %d %s", res.StatusCode, raw)
	}
}

func TestMigrationDiskImportAndCancelLeavesSource(t *testing.T) {
	s, mem, ts, cookie, clusterID, poolID, netID := phase18Ready(t)
	_ = s
	src := filepath.Join(t.TempDir(), "guest.qcow2")
	if err := os.WriteFile(src, []byte("qcow-data"), 0o640); err != nil {
		t.Fatal(err)
	}
	// ValidateHostPath requires /tmp prefix.
	tmp := filepath.Join("/tmp", "ndl-mig-src-"+uuid.NewString()+".qcow2")
	if err := os.WriteFile(tmp, []byte("qcow-data"), 0o640); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(tmp) })

	body := `{"adapter":"disk","mode":"disk","path":"` + tmp + `","name":"imported-guest","kind":"vm","cpus":2,"memory_bytes":536870912,"firmware":"bios","pool_id":"` + poolID + `","network_id":"` + netID + `"}`
	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/migration/import/disk", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusAccepted {
		t.Fatalf("import %d %s", res.StatusCode, raw)
	}
	var job map[string]any
	_ = json.Unmarshal(raw, &job)
	id, _ := job["id"].(string)
	got := waitMigrationJob(t, ts, cookie, id)
	state, _ := got["state"].(string)
	if state != "succeeded" && state != "failed" {
		t.Fatalf("job %+v", got)
	}
	if _, err := os.Stat(tmp); err != nil {
		t.Fatal("source disk was changed or removed")
	}
	_ = clusterID
	_ = mem
	_ = storage.ClassVMDisk

	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/migration/jobs/"+id+"/cancel", strings.NewReader("{}"))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, err = ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ = io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK && res.StatusCode != http.StatusUnprocessableEntity && res.StatusCode != http.StatusNotFound {
		t.Fatalf("cancel %d %s", res.StatusCode, raw)
	}
	if _, err := os.Stat(tmp); err != nil {
		t.Fatal("cancel must not remove the source disk")
	}
}

func TestMigrationDiskImportFailsClosedWhenDiskPersistFails(t *testing.T) {
	s, mem, _, _, clusterID, poolID, netID := phase18Ready(t)
	tmp := filepath.Join(t.TempDir(), "guest.qcow2")
	if err := os.WriteFile(tmp, []byte("qcow-data"), 0o640); err != nil {
		t.Fatal(err)
	}
	s.Backup = skipConvertBackup{fakeBackup: s.Backup.(*fakeBackup)}
	s.Store = failCreateWorkloadDiskStore{Store: mem}
	_, err := s.adoptImportedDisks(context.Background(), clusterID, "imported-guest", []string{tmp}, poolID, netID, "bios", 2, 536870912, false, "")
	if err == nil || !strings.Contains(err.Error(), "could not record VM disk") {
		t.Fatalf("disk persist %v", err)
	}
}

func TestMigrationDiskImportFailsClosedWhenNICPersistFails(t *testing.T) {
	s, mem, _, _, clusterID, poolID, netID := phase18Ready(t)
	tmp := filepath.Join(t.TempDir(), "guest.qcow2")
	if err := os.WriteFile(tmp, []byte("qcow-data"), 0o640); err != nil {
		t.Fatal(err)
	}
	s.Backup = skipConvertBackup{fakeBackup: s.Backup.(*fakeBackup)}
	s.Store = failCreateWorkloadNICStore{Store: mem}
	_, err := s.adoptImportedDisks(context.Background(), clusterID, "imported-guest", []string{tmp}, poolID, netID, "bios", 2, 536870912, false, "")
	if err == nil || !strings.Contains(err.Error(), "could not record VM NIC") {
		t.Fatalf("nic persist %v", err)
	}
}

func TestMigrationDiskImportFailsClosedForUnavailableDestPool(t *testing.T) {
	s, mem, _, _, clusterID, _, netID := phase18Ready(t)
	tmp := filepath.Join(t.TempDir(), "guest.qcow2")
	if err := os.WriteFile(tmp, []byte("qcow-data"), 0o640); err != nil {
		t.Fatal(err)
	}
	s.Backup = skipConvertBackup{fakeBackup: s.Backup.(*fakeBackup)}
	offlinePool := uuid.NewString()
	if err := mem.CreateStoragePool(context.Background(), appdb.StoragePool{
		ID: offlinePool, ClusterID: clusterID, Name: "mig-import-offline",
		BackendType: storage.BackendDirectory, Status: storage.StatusUnavailable,
		RootPath: "/var/lib/ndl/storage/mig-import-offline",
	}); err != nil {
		t.Fatal(err)
	}
	volsBefore, _ := mem.ListVolumes(context.Background(), clusterID, "")
	wlsBefore, _ := mem.ListWorkloads(context.Background(), clusterID)
	_, err := s.adoptImportedDisks(context.Background(), clusterID, "imported-guest", []string{tmp}, offlinePool, netID, "bios", 2, 536870912, false, "")
	if err == nil || !strings.Contains(err.Error(), "storage pool is unavailable") {
		t.Fatalf("unavailable dest pool %v", err)
	}
	wlsAfter, _ := mem.ListWorkloads(context.Background(), clusterID)
	if len(wlsAfter) != len(wlsBefore) {
		t.Fatalf("GET must not list a VM whose import dest pool apply cannot allocate: %+v", wlsAfter)
	}
	volsAfter, _ := mem.ListVolumes(context.Background(), clusterID, "")
	if len(volsAfter) != len(volsBefore) {
		t.Fatalf("import must not persist a volume apply cannot allocate: %d -> %d", len(volsBefore), len(volsAfter))
	}
}

func TestMigrationDiskImportFailsClosedForUnavailableFallbackDestPool(t *testing.T) {
	s, mem, _, _, clusterID, poolID, netID := phase18Ready(t)
	tmp := filepath.Join(t.TempDir(), "guest.qcow2")
	if err := os.WriteFile(tmp, []byte("qcow-data"), 0o640); err != nil {
		t.Fatal(err)
	}
	s.Backup = skipConvertBackup{fakeBackup: s.Backup.(*fakeBackup)}
	if err := mem.UpdateStoragePoolObserved(context.Background(), appdb.StoragePool{ID: poolID, Status: storage.StatusFailed}); err != nil {
		t.Fatal(err)
	}
	volsBefore, _ := mem.ListVolumes(context.Background(), clusterID, "")
	wlsBefore, _ := mem.ListWorkloads(context.Background(), clusterID)
	_, err := s.adoptImportedDisks(context.Background(), clusterID, "imported-guest", []string{tmp}, "", netID, "bios", 2, 536870912, false, "")
	if err == nil || !strings.Contains(err.Error(), "storage pool is unavailable") {
		t.Fatalf("unavailable fallback dest pool %v", err)
	}
	wlsAfter, _ := mem.ListWorkloads(context.Background(), clusterID)
	if len(wlsAfter) != len(wlsBefore) {
		t.Fatalf("GET must not list a VM whose import fallback dest pool apply cannot allocate: %+v", wlsAfter)
	}
	volsAfter, _ := mem.ListVolumes(context.Background(), clusterID, "")
	if len(volsAfter) != len(volsBefore) {
		t.Fatalf("import must not persist a volume apply cannot allocate: %d -> %d", len(volsBefore), len(volsAfter))
	}
}

func TestMigrationDiskImportFailsClosedForUnavailableNetwork(t *testing.T) {
	s, mem, _, _, clusterID, poolID, _ := phase18Ready(t)
	tmp := filepath.Join(t.TempDir(), "guest.qcow2")
	if err := os.WriteFile(tmp, []byte("qcow-data"), 0o640); err != nil {
		t.Fatal(err)
	}
	s.Backup = skipConvertBackup{fakeBackup: s.Backup.(*fakeBackup)}
	offlineNet := uuid.NewString()
	if err := mem.CreateNetwork(context.Background(), appdb.Network{
		ID: offlineNet, ClusterID: clusterID, Name: "mig-import-offline",
		Kind: ndnet.KindIsolated, Status: ndnet.StatusUnavailable, BridgeName: "ndlcafe00bb",
	}); err != nil {
		t.Fatal(err)
	}
	volsBefore, _ := mem.ListVolumes(context.Background(), clusterID, "")
	wlsBefore, _ := mem.ListWorkloads(context.Background(), clusterID)
	_, err := s.adoptImportedDisks(context.Background(), clusterID, "imported-guest", []string{tmp}, poolID, offlineNet, "bios", 2, 536870912, false, "")
	if err == nil || !strings.Contains(err.Error(), "an available network is required") {
		t.Fatalf("unavailable network %v", err)
	}
	wlsAfter, _ := mem.ListWorkloads(context.Background(), clusterID)
	if len(wlsAfter) != len(wlsBefore) {
		t.Fatalf("GET must not list a VM whose import network apply cannot attach: %+v", wlsAfter)
	}
	volsAfter, _ := mem.ListVolumes(context.Background(), clusterID, "")
	if len(volsAfter) != len(volsBefore) {
		t.Fatalf("import must not persist a volume apply cannot attach: %d -> %d", len(volsBefore), len(volsAfter))
	}
}

type failUpdateMigrationStore struct {
	appdb.Store
}

func (f failUpdateMigrationStore) UpdateMigrationJob(ctx context.Context, job appdb.MigrationJob) error {
	return errors.New("persist failed")
}

func TestCancelMigrationJobFailsClosedWhenPersistFails(t *testing.T) {
	s, mem, token := testServer(t)
	cluster, _ := mem.GetCluster(context.Background())
	now := time.Now().UTC()
	job := appdb.MigrationJob{
		ID: uuid.NewString(), ClusterID: cluster.ID, Direction: "import", State: "running",
		Adapter: "qcow2", CreatedAt: now, UpdatedAt: now,
	}
	if err := mem.CreateMigrationJob(context.Background(), job); err != nil {
		t.Fatal(err)
	}
	s.Store = failUpdateMigrationStore{Store: mem}
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)
	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/migration/jobs/"+job.ID+"/cancel", strings.NewReader("{}"))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusInternalServerError {
		t.Fatalf("cancel persist %d %s", res.StatusCode, raw)
	}
	got, err := mem.GetMigrationJob(context.Background(), cluster.ID, job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != "running" {
		t.Fatalf("job state mutated without persist: %+v", got)
	}
}

func TestMigrationRejectsCleanupSourceAndRedactsEmptyCreds(t *testing.T) {
	s, mem, token := testServer(t)
	cluster, _ := mem.GetCluster(context.Background())
	_ = mem.UpsertNode(context.Background(), appdb.Node{ID: uuid.NewString(), ClusterID: cluster.ID, Name: "local"})
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	admin := claimAdmin(t, ts, token)

	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/migration/jobs", strings.NewReader(`{"adapter":"disk","mode":"disk","path":"/tmp/x.qcow2","cleanup_source":true}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: admin})
	res, _ := ts.Client().Do(req)
	body, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusBadRequest || !strings.Contains(string(body), "source destruction") {
		t.Fatalf("cleanup_source %d %s", res.StatusCode, body)
	}

	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/migration/sources", strings.NewReader(`{"adapter":"disk","label":"files"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: admin})
	res, _ = ts.Client().Do(req)
	body, _ = io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusCreated || !strings.Contains(string(body), `"has_credentials":false`) {
		t.Fatalf("empty creds %d %s", res.StatusCode, body)
	}
}

func TestMigrationBundleChecksumMismatch(t *testing.T) {
	s, _, token := testServer(t)
	_ = s
	dir := filepath.Join("/tmp", "ndl-bundle-"+uuid.NewString())
	m := migration.Manifest{
		SchemaVersion: migration.ManifestSchema, Kind: migration.KindVM,
		Identity: migration.Identity{Name: "round"}, VM: &migration.VMSection{CPUs: 1, MemoryBytes: 128 << 20, Disks: []migration.Disk{{Role: "boot", Format: "qcow2"}}},
	}
	disk := filepath.Join(dir, "src.qcow2")
	_ = os.MkdirAll(dir, 0o750)
	_ = os.WriteFile(disk, []byte("abc"), 0o640)
	if err := migration.WriteBundle(dir, m, map[string]string{"disks/boot.qcow2": disk}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "disks", "boot.qcow2"), []byte("tampered"), 0o640); err != nil {
		t.Fatal(err)
	}
	_, err := migration.ReadBundle(dir)
	if err == nil {
		t.Fatal("tampered bundle must fail checksum")
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	_ = token
}

func memNodeID(t *testing.T, mem *appdb.Memory, clusterID string) string {
	t.Helper()
	nodes, err := mem.ListClusterNodes(context.Background(), clusterID)
	if err != nil || len(nodes) == 0 {
		t.Fatal("node")
	}
	return nodes[0].ID
}

func waitMigrationJob(t *testing.T, ts *httptest.Server, cookie, id string) map[string]any {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	var last map[string]any
	for time.Now().Before(deadline) {
		req, _ := http.NewRequest("GET", ts.URL+"/api/v1/migration/jobs/"+id, nil)
		req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
		res, err := ts.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := io.ReadAll(res.Body)
		_ = res.Body.Close()
		last = map[string]any{}
		_ = json.Unmarshal(raw, &last)
		st, _ := last["state"].(string)
		if st != "running" && st != "canceling" && st != "" {
			return last
		}
		time.Sleep(20 * time.Millisecond)
	}
	return last
}

func TestPhase44DeleteMissingMigrationSourceFailsClosed(t *testing.T) {
	s, _, token := testServer(t)
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)
	missing := uuid.NewString()
	req, _ := http.NewRequest("DELETE", ts.URL+"/api/v1/migration/sources/"+missing, nil)
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ := ts.Client().Do(req)
	raw, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusNotFound {
		t.Fatalf("missing source delete %d %s", res.StatusCode, raw)
	}
	if strings.Contains(string(raw), `"ok":true`) {
		t.Fatalf("200 must not invent delete of a missing source: %s", raw)
	}
}

func TestMigrationJobsGETReturnsNewestFirst(t *testing.T) {
	s, mem, token := testServer(t)
	cluster, _ := mem.GetCluster(context.Background())
	_ = mem.UpsertNode(context.Background(), appdb.Node{ID: uuid.NewString(), ClusterID: cluster.ID, Name: "local"})
	older := appdb.MigrationJob{
		ID: uuid.NewString(), ClusterID: cluster.ID, Adapter: "disk", Direction: "import",
		State: "succeeded", CreatedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	}
	newer := appdb.MigrationJob{
		ID: uuid.NewString(), ClusterID: cluster.ID, Adapter: "disk", Direction: "export",
		State: "failed", CreatedAt: time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC),
	}
	if err := mem.CreateMigrationJob(context.Background(), older); err != nil {
		t.Fatal(err)
	}
	if err := mem.CreateMigrationJob(context.Background(), newer); err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	admin := claimAdmin(t, ts, token)
	req, _ := http.NewRequest("GET", ts.URL+"/api/v1/migration/jobs", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: admin})
	res, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("list jobs %d %s", res.StatusCode, raw)
	}
	var body struct {
		Items []map[string]any `json:"items"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Items) != 2 || body.Items[0]["id"] != newer.ID || body.Items[1]["id"] != older.ID {
		t.Fatalf("GET must list newest migration job first: %s", raw)
	}
}
