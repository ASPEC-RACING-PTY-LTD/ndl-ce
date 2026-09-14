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
	"github.com/no-dal/ndl-ce/internal/vmspec"
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

	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/migration/sources", strings.NewReader(`{"adapter":"proxmox","endpoint":"https://pve.example:8006","token":"user@pam!tok=SECRET-TOKEN-VALUE"}`))
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
	if res.StatusCode != http.StatusBadRequest || !strings.Contains(string(body), "not the secret alone") {
		t.Fatalf("secret-only token %d %s", res.StatusCode, body)
	}

	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/migration/sources", strings.NewReader(`{"adapter":"proxmox","endpoint":"https://pve.example:8006","token":"user@pam!tok=SECRET-TOKEN-VALUE"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: admin})
	res, _ = ts.Client().Do(req)
	body, _ = io.ReadAll(res.Body)
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

func TestMigrationPlanAutoMapsSingleDest(t *testing.T) {
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
			_, _ = w.Write([]byte(`{"data":[{"vmid":100,"name":"web","status":"stopped","cpus":2,"maxmem":4294967296,"maxdisk":10737418240}]}`))
		case strings.Contains(r.URL.Path, "/lxc") && !strings.Contains(r.URL.Path, "/config"):
			_, _ = w.Write([]byte(`{"data":[]}`))
		case strings.Contains(r.URL.Path, "/storage") && strings.Contains(r.URL.Path, "/content"):
			_, _ = w.Write([]byte(`{"data":[]}`))
		case strings.Contains(r.URL.Path, "/storage"):
			_, _ = w.Write([]byte(`{"data":[{"storage":"local","type":"dir"}]}`))
		case strings.Contains(r.URL.Path, "/config"):
			_, _ = w.Write([]byte(`{"data":{"name":"web","cores":2,"memory":4096,"scsi0":"local:100/vm-100-disk-0.qcow2,size=10G","net0":"virtio=AA:BB:CC:DD:EE:FF,bridge=vmbr0"}}`))
		default:
			_, _ = w.Write([]byte(`{"data":[]}`))
		}
	}))
	defer pve.Close()

	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/migration/sources", strings.NewReader(`{"adapter":"proxmox","endpoint":"`+pve.URL+`","token":"user@pam!tok=SECRET-TOKEN-VALUE","insecure":true}`))
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
	if res.StatusCode != http.StatusOK || !strings.Contains(string(raw), "web") {
		t.Fatalf("discover %d %s", res.StatusCode, raw)
	}

	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/migration/plans", strings.NewReader(`{"source_id":"`+id+`","selected":["pve1/100"]}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(req)
	raw, _ = io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("auto plan %d %s", res.StatusCode, raw)
	}
	if !strings.Contains(string(raw), `"offline"`) {
		t.Fatalf("expected suggested offline mode %s", raw)
	}
	if !strings.Contains(string(raw), poolID) || !strings.Contains(string(raw), netID) {
		t.Fatalf("expected automatic dest mapping %s", raw)
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

type missDeleteMigrationSourceStore struct {
	appdb.Store
}

func (missDeleteMigrationSourceStore) DeleteMigrationSource(context.Context, string, string) error {
	return nil
}

func TestPhase44DeleteMigrationSourceFailsClosedWhenPersistMisses(t *testing.T) {
	s, mem, token := testServer(t)
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)
	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/migration/sources", strings.NewReader(`{"adapter":"proxmox","endpoint":"https://pve.example:8006","token":"user@pam!tok=SECRET-TOKEN-VALUE"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ := ts.Client().Do(req)
	raw, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("create source %d %s", res.StatusCode, raw)
	}
	var created map[string]any
	if err := json.Unmarshal(raw, &created); err != nil {
		t.Fatal(err)
	}
	id, _ := created["id"].(string)
	s.Store = missDeleteMigrationSourceStore{Store: mem}

	req, _ = http.NewRequest("DELETE", ts.URL+"/api/v1/migration/sources/"+id, nil)
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(req)
	raw, _ = io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusInternalServerError {
		t.Fatalf("delete persist miss %d %s", res.StatusCode, raw)
	}
	if !strings.Contains(string(raw), "could not record migration source") {
		t.Fatalf("delete persist miss body %s", raw)
	}

	s.Store = mem
	req, _ = http.NewRequest("GET", ts.URL+"/api/v1/migration/sources", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(req)
	raw, _ = io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("list sources %d %s", res.StatusCode, raw)
	}
	if !strings.Contains(string(raw), id) {
		t.Fatalf("GET /migration/sources must still list the source after persist miss: %s", raw)
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

func TestPhase44VMMigrationExportFailsClosedForExtraDataDisk(t *testing.T) {
	s, mem, ts, cookie, clusterID, poolID, netID := phase18Ready(t)
	fb := &fakeBackup{}
	s.Backup = fb
	created := createPhase18VM(t, ts, cookie, poolID, netID, "export-extra")
	srcID := created["id"].(string)
	wl, err := mem.GetWorkload(context.Background(), clusterID, srcID)
	if err != nil || wl == nil {
		t.Fatal(err)
	}
	spec, err := vmspec.Parse(wl.SpecJSON)
	if err != nil {
		t.Fatal(err)
	}
	extra := uuid.NewString()
	node, _ := mem.GetNode(context.Background(), clusterID)
	if err := mem.CreateVolume(context.Background(), appdb.Volume{
		ID: extra, ClusterID: clusterID, NodeID: node.ID, PoolID: poolID,
		Class: storage.ClassVMDisk, Kind: storage.KindBlock, Format: storage.FormatQCOW2,
		Status: storage.StatusAvailable, BackendType: storage.BackendDirectory, BackendRef: "volumes/vm-disk/extra.qcow2",
	}); err != nil {
		t.Fatal(err)
	}
	spec.Disks = append(spec.Disks, vmspec.Disk{Role: vmspec.DiskRoleData, VolumeID: extra})
	if err := mem.UpdateWorkloadSpec(context.Background(), appdb.Workload{
		ID: wl.ID, SpecJSON: vmspec.MustJSON(spec), Firmware: wl.Firmware, CPUs: wl.CPUs, MemoryBytes: wl.MemoryBytes,
	}); err != nil {
		t.Fatal(err)
	}
	if err := mem.CreateWorkloadDisk(context.Background(), appdb.WorkloadDisk{
		ID: uuid.NewString(), ClusterID: clusterID, WorkloadID: srcID, VolumeID: extra,
		Role: vmspec.DiskRoleData, Slot: 1, Format: storage.FormatQCOW2,
	}); err != nil {
		t.Fatal(err)
	}

	exp, _ := http.NewRequest("POST", ts.URL+"/api/v1/migration/jobs", strings.NewReader(`{"direction":"export","workload_id":"`+srcID+`"}`))
	exp.Header.Set("Content-Type", "application/json")
	exp.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	eres, err := ts.Client().Do(exp)
	if err != nil {
		t.Fatal(err)
	}
	eraw, _ := io.ReadAll(eres.Body)
	_ = eres.Body.Close()
	if eres.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("migration export %d %s", eres.StatusCode, eraw)
	}
	if !strings.Contains(strings.ToLower(string(eraw)), "disk") {
		t.Fatalf("migration export body %s", eraw)
	}
	if len(fb.converts) != 0 {
		t.Fatalf("ConvertImport must not copy boot and drop extra disks: %+v", fb.converts)
	}
	jobs, _ := mem.ListMigrationJobs(context.Background(), clusterID, 100)
	if len(jobs) != 0 {
		t.Fatalf("GET must not list a succeeded extra-disk export: %+v", jobs)
	}
}

func TestMigrationPlanLXCUsesTempVZdumpAndBlocksWithoutBackupStore(t *testing.T) {
	s, mem, token := testServer(t)
	cluster, _ := mem.GetCluster(context.Background())
	_ = mem.UpsertNode(context.Background(), appdb.Node{ID: uuid.NewString(), ClusterID: cluster.ID, Name: "local"})
	poolID, netID := seedCompute(t, mem, cluster.ID, memNodeID(t, mem, cluster.ID))
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)

	pveOK := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch {
		case strings.HasSuffix(r.URL.Path, "/nodes"):
			_, _ = w.Write([]byte(`{"data":[{"node":"pve1"}]}`))
		case strings.Contains(r.URL.Path, "/qemu") && !strings.Contains(r.URL.Path, "/config"):
			_, _ = w.Write([]byte(`{"data":[]}`))
		case strings.HasSuffix(r.URL.Path, "/lxc"):
			_, _ = w.Write([]byte(`{"data":[{"vmid":104,"name":"SoundDock","status":"running","cpus":4,"maxmem":8589934592,"maxdisk":128849018880}]}`))
		case strings.Contains(r.URL.Path, "/lxc/104/config"):
			_, _ = w.Write([]byte(`{"data":{"hostname":"SoundDock","cores":4,"memory":8192,"rootfs":"local-zfs:subvol-104-disk-0,size=120G","net0":"name=eth0,bridge=vmbr0"}}`))
		case strings.Contains(r.URL.Path, "/storage") && strings.Contains(r.URL.Path, "/content"):
			_, _ = w.Write([]byte(`{"data":[]}`))
		case strings.Contains(r.URL.Path, "/storage"):
			_, _ = w.Write([]byte(`{"data":[{"storage":"local-zfs","type":"zfspool","content":"rootdir,images"},{"storage":"local","type":"dir","content":"backup,iso,vztmpl"}]}`))
		default:
			_, _ = w.Write([]byte(`{"data":[]}`))
		}
	}))
	defer pveOK.Close()

	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/migration/sources", strings.NewReader(`{"adapter":"proxmox","endpoint":"`+pveOK.URL+`","token":"user@pam!tok=secret","insecure":true}`))
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

	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/migration/plans", strings.NewReader(`{"source_id":"`+id+`","selected":["pve1/104"]}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(req)
	raw, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("plan %d %s", res.StatusCode, raw)
	}
	if !strings.Contains(string(raw), `"backup"`) || !strings.Contains(string(raw), "temporary vzdump") {
		t.Fatalf("expected auto vzdump plan %s", raw)
	}
	if strings.Contains(string(raw), `"BLOCKED"`) {
		t.Fatalf("downloadable backup store must not block %s", raw)
	}
	_ = poolID
	_ = netID

	pveBlock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch {
		case strings.HasSuffix(r.URL.Path, "/nodes"):
			_, _ = w.Write([]byte(`{"data":[{"node":"pve1"}]}`))
		case strings.Contains(r.URL.Path, "/qemu") && !strings.Contains(r.URL.Path, "/config"):
			_, _ = w.Write([]byte(`{"data":[]}`))
		case strings.HasSuffix(r.URL.Path, "/lxc"):
			_, _ = w.Write([]byte(`{"data":[{"vmid":104,"name":"SoundDock","status":"running","cpus":4,"maxmem":8589934592,"maxdisk":128849018880}]}`))
		case strings.Contains(r.URL.Path, "/lxc/104/config"):
			_, _ = w.Write([]byte(`{"data":{"hostname":"SoundDock","cores":4,"memory":8192,"rootfs":"local-lvm:subvol-104-disk-0,size=120G","net0":"name=eth0,bridge=vmbr0"}}`))
		case strings.Contains(r.URL.Path, "/storage"):
			_, _ = w.Write([]byte(`{"data":[{"storage":"local-lvm","type":"lvmthin","content":"rootdir,images"}]}`))
		default:
			_, _ = w.Write([]byte(`{"data":[]}`))
		}
	}))
	defer pveBlock.Close()

	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/migration/sources", strings.NewReader(`{"adapter":"proxmox","endpoint":"`+pveBlock.URL+`","token":"user@pam!tok=secret","insecure":true}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(req)
	src = map[string]any{}
	_ = json.NewDecoder(res.Body).Decode(&src)
	_ = res.Body.Close()
	id, _ = src["id"].(string)

	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/migration/plans", strings.NewReader(`{"source_id":"`+id+`","selected":["pve1/104"]}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(req)
	raw, _ = io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("blocked plan %d %s", res.StatusCode, raw)
	}
	if !strings.Contains(string(raw), "BLOCKED") || !strings.Contains(string(raw), "lvmthin") {
		t.Fatalf("expected Review block %s", raw)
	}

	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/migration/jobs", strings.NewReader(`{"source_id":"`+id+`","selected":["pve1/104"]}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(req)
	raw, _ = io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusUnprocessableEntity || !strings.Contains(string(raw), "lvmthin") {
		t.Fatalf("start must fail in Review/preflight %d %s", res.StatusCode, raw)
	}
}

func TestMigrationPlanLXCUsesLocalHostWhenStopped(t *testing.T) {
	s, mem, token := testServer(t)
	cluster, _ := mem.GetCluster(context.Background())
	_ = mem.UpsertNode(context.Background(), appdb.Node{ID: uuid.NewString(), ClusterID: cluster.ID, Name: "local"})
	_, _ = seedCompute(t, mem, cluster.ID, memNodeID(t, mem, cluster.ID))
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)

	root := filepath.Join(t.TempDir(), "vz", "images", "104", "subvol-104-disk-0")
	for _, d := range []string{"sbin", "usr/bin", "bin", "etc"} {
		if err := os.MkdirAll(filepath.Join(root, filepath.FromSlash(d)), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "sbin", "init"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "etc", "hosts"), []byte("127.0.0.1 localhost\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	vz := filepath.Dir(filepath.Dir(filepath.Dir(root)))
	storageJSON, _ := json.Marshal(map[string]any{
		"data": []map[string]any{
			{"storage": "local", "type": "dir", "content": "rootdir,images,backup", "path": vz},
		},
	})

	pve := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch {
		case strings.HasSuffix(r.URL.Path, "/nodes"):
			_, _ = w.Write([]byte(`{"data":[{"node":"pve1"}]}`))
		case strings.Contains(r.URL.Path, "/qemu") && !strings.Contains(r.URL.Path, "/config"):
			_, _ = w.Write([]byte(`{"data":[]}`))
		case strings.HasSuffix(r.URL.Path, "/lxc"):
			_, _ = w.Write([]byte(`{"data":[{"vmid":104,"name":"SoundDock","status":"stopped","cpus":4,"maxmem":8589934592,"maxdisk":128849018880}]}`))
		case strings.Contains(r.URL.Path, "/lxc/104/config"):
			_, _ = w.Write([]byte(`{"data":{"hostname":"SoundDock","cores":4,"memory":8192,"rootfs":"local:subvol-104-disk-0,size=120G","net0":"name=eth0,bridge=vmbr0"}}`))
		case strings.Contains(r.URL.Path, "/storage") && strings.Contains(r.URL.Path, "/content"):
			_, _ = w.Write([]byte(`{"data":[]}`))
		case strings.Contains(r.URL.Path, "/storage"):
			_, _ = w.Write(storageJSON)
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

	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/migration/plans", strings.NewReader(`{"source_id":"`+id+`","selected":["pve1/104"]}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(req)
	raw, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("plan %d %s", res.StatusCode, raw)
	}
	if !strings.Contains(string(raw), `"mode":"local"`) || !strings.Contains(string(raw), `"local_host":true`) {
		t.Fatalf("expected local host plan %s", raw)
	}
	if strings.Contains(string(raw), `"BLOCKED"`) || strings.Contains(string(raw), `"temp_backup":true`) {
		t.Fatalf("local host must not block or use vzdump %s", raw)
	}
}

func writeTmpMigrationFile(t *testing.T, suffix, body string) string {
	t.Helper()
	path := filepath.Join("/tmp", "ndl-mig-"+uuid.NewString()+suffix)
	if err := os.WriteFile(path, []byte(body), 0o640); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(path) })
	return path
}

func TestMigrationSubmitRejectsUnsupportedArchiveAndPartialDest(t *testing.T) {
	s, mem, token := testServer(t)
	cluster, _ := mem.GetCluster(context.Background())
	_ = mem.UpsertNode(context.Background(), appdb.Node{ID: uuid.NewString(), ClusterID: cluster.ID, Name: "local"})
	poolID, netID := seedCompute(t, mem, cluster.ID, memNodeID(t, mem, cluster.ID))
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)

	vma := writeTmpMigrationFile(t, ".vma", "vma-bytes")
	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/migration/jobs", strings.NewReader(`{"adapter":"disk","mode":"disk","path":"`+vma+`","name":"vma-guest","kind":"vm","cpus":1,"memory_bytes":134217728,"pool_id":"`+poolID+`","network_id":"`+netID+`"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ := ts.Client().Do(req)
	raw, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusUnprocessableEntity || !strings.Contains(strings.ToLower(string(raw)), "vma") {
		t.Fatalf("vma %d %s", res.StatusCode, raw)
	}

	if err := mem.CreateWorkload(context.Background(), appdb.Workload{
		ID: uuid.NewString(), ClusterID: cluster.ID, NodeID: memNodeID(t, mem, cluster.ID),
		Name: "partial-guest", Kind: "vm", Status: "failed", ImagePin: "imported", ImageVerified: false,
	}); err != nil {
		t.Fatal(err)
	}
	qcow := writeTmpMigrationFile(t, ".qcow2", "qcow-data")
	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/migration/jobs", strings.NewReader(`{"adapter":"disk","mode":"disk","path":"`+qcow+`","name":"partial-guest","kind":"vm","cpus":1,"memory_bytes":134217728,"pool_id":"`+poolID+`","network_id":"`+netID+`"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(req)
	raw, _ = io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusUnprocessableEntity || !strings.Contains(strings.ToLower(string(raw)), "partial") {
		t.Fatalf("partial %d %s", res.StatusCode, raw)
	}
}

func TestMigrationSubmitRejectsDuplicateDestNames(t *testing.T) {
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
		case strings.Contains(r.URL.Path, "/qemu") && !strings.Contains(r.URL.Path, "/config"):
			_, _ = w.Write([]byte(`{"data":[{"vmid":100,"name":"web","status":"stopped","cpus":1,"maxmem":134217728,"maxdisk":1073741824},{"vmid":101,"name":"web","status":"stopped","cpus":1,"maxmem":134217728,"maxdisk":1073741824}]}`))
		case strings.Contains(r.URL.Path, "/lxc"):
			_, _ = w.Write([]byte(`{"data":[]}`))
		case strings.Contains(r.URL.Path, "/100/config"):
			_, _ = w.Write([]byte(`{"data":{"name":"web","cores":1,"memory":128,"scsi0":"local:100/vm-100-disk-0.qcow2,size=1G","net0":"virtio=AA:BB:CC:DD:EE:01,bridge=vmbr0"}}`))
		case strings.Contains(r.URL.Path, "/101/config"):
			_, _ = w.Write([]byte(`{"data":{"name":"web","cores":1,"memory":128,"scsi0":"local:101/vm-101-disk-0.qcow2,size=1G","net0":"virtio=AA:BB:CC:DD:EE:02,bridge=vmbr0"}}`))
		case strings.Contains(r.URL.Path, "/storage"):
			_, _ = w.Write([]byte(`{"data":[{"storage":"local","type":"dir","content":"images,iso,backup"}]}`))
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
	body := `{"source_id":"` + id + `","selected":["pve1/100","pve1/101"],"modes":{"pve1/100":"offline","pve1/101":"offline"},"mapping":{"storage":{"local":"` + poolID + `"},"network":{"vmbr0":"` + netID + `"}}}`
	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/migration/jobs", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(req)
	raw, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusUnprocessableEntity || !strings.Contains(strings.ToLower(string(raw)), "duplicate") {
		t.Fatalf("duplicate %d %s", res.StatusCode, raw)
	}
}

func TestMigrationRetrySkipsCompletedAndDiagnosticsBundle(t *testing.T) {
	s, mem, token := testServer(t)
	cluster, _ := mem.GetCluster(context.Background())
	nodeID := uuid.NewString()
	_ = mem.UpsertNode(context.Background(), appdb.Node{ID: nodeID, ClusterID: cluster.ID, Name: "local"})
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)

	destID := uuid.NewString()
	if err := mem.CreateWorkload(context.Background(), appdb.Workload{
		ID: destID, ClusterID: cluster.ID, NodeID: nodeID, Name: "already-in",
		Kind: "vm", Status: "stopped", ImagePin: "imported", ImageVerified: true,
	}); err != nil {
		t.Fatal(err)
	}
	plan := migration.Plan{
		ID: uuid.NewString(), Direction: "import", Adapter: migration.AdapterDisk,
		Items: []migration.ItemPlan{{
			SourceID: "/tmp/already.qcow2", Name: "already-in", Kind: migration.KindVM,
			Mode: migration.ModeDisk, Compatibility: migration.CompatReady,
		}},
	}
	planBody, _ := json.Marshal(plan)
	st := migration.JobStatus{
		ID: uuid.NewString(), State: "failed", Stage: "transfer", SourceUntouched: true, Retryable: true,
		Reports: []migration.Report{{SourceID: "/tmp/already.qcow2", Name: "already-in", WorkloadID: destID}},
	}
	stBody, _ := json.Marshal(st)
	job := appdb.MigrationJob{
		ID: st.ID, ClusterID: cluster.ID, Adapter: migration.AdapterDisk, Direction: "import",
		State: "failed", Stage: "transfer", PlanJSON: planBody, StatusJSON: stBody,
	}
	if err := mem.CreateMigrationJob(context.Background(), job); err != nil {
		t.Fatal(err)
	}
	if err := mem.InsertEvent(context.Background(), appdb.Event{
		ID: uuid.NewString(), ClusterID: cluster.ID, Type: "migration.failed",
		Payload: mustJSONBytes(map[string]string{"job_id": job.ID, "error": "transfer failed"}), CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}

	diag, _ := http.NewRequest("GET", ts.URL+"/api/v1/migration/jobs/"+job.ID+"/diagnostics", nil)
	diag.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	dres, _ := ts.Client().Do(diag)
	draw, _ := io.ReadAll(dres.Body)
	_ = dres.Body.Close()
	if dres.StatusCode != http.StatusOK {
		t.Fatalf("diagnostics %d %s", dres.StatusCode, draw)
	}
	if !strings.Contains(string(draw), `"health_checks"`) || !strings.Contains(string(draw), `"already-in"`) || !strings.Contains(string(draw), destID) {
		t.Fatalf("bundle %s", draw)
	}
	if strings.Contains(string(draw), "SECRET") || strings.Contains(string(draw), `"token"`) {
		t.Fatalf("token leaked %s", draw)
	}

	wlDiag, _ := http.NewRequest("GET", ts.URL+"/api/v1/workloads/"+destID+"/migration-diagnostics", nil)
	wlDiag.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	wres, _ := ts.Client().Do(wlDiag)
	wraw, _ := io.ReadAll(wres.Body)
	_ = wres.Body.Close()
	if wres.StatusCode != http.StatusOK || !strings.Contains(string(wraw), job.ID) {
		t.Fatalf("workload diagnostics %d %s", wres.StatusCode, wraw)
	}

	retry, _ := http.NewRequest("POST", ts.URL+"/api/v1/migration/jobs/"+job.ID+"/retry", strings.NewReader("{}"))
	retry.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	rres, _ := ts.Client().Do(retry)
	rraw, _ := io.ReadAll(rres.Body)
	_ = rres.Body.Close()
	if rres.StatusCode != http.StatusAccepted {
		t.Fatalf("retry %d %s", rres.StatusCode, rraw)
	}
	got := waitMigrationJob(t, ts, cookie, job.ID)
	if got["state"] != "succeeded" {
		t.Fatalf("retry job %+v", got)
	}
	if wl, _ := mem.GetWorkload(context.Background(), cluster.ID, destID); wl == nil || wl.Name != "already-in" {
		t.Fatal("completed dest must remain")
	}
}

func TestRollbackImportedContainerRemovesOwnedVolume(t *testing.T) {
	s, mem, _ := testServer(t)
	cluster, _ := mem.GetCluster(context.Background())
	nodeID := uuid.NewString()
	_ = mem.UpsertNode(context.Background(), appdb.Node{ID: nodeID, ClusterID: cluster.ID, Name: "local"})
	poolID := uuid.NewString()
	_ = mem.CreateStoragePool(context.Background(), appdb.StoragePool{
		ID: poolID, ClusterID: cluster.ID, NodeID: nodeID, Name: "local",
		BackendType: storage.BackendDirectory, Status: storage.StatusAvailable, RootPath: "/var/lib/ndl/storage/local",
	})
	volID := uuid.NewString()
	jobID := uuid.NewString()
	tiny := int64(1024)
	_ = mem.CreateVolume(context.Background(), appdb.Volume{
		ID: volID, ClusterID: cluster.ID, NodeID: nodeID, PoolID: poolID,
		Class: storage.ClassContainerRoot, Kind: storage.KindFilesystem, Format: storage.FormatDirectory,
		SizeBytes: 4 << 30, Status: storage.StatusAvailable, BackendType: storage.BackendDirectory,
		BackendRef: "volumes/container-root/" + volID, AllocatedBytes: &tiny,
		Owner: storage.VolumeOwnerName, OwnerKind: storage.VolumeKindMigration, OwnerJobID: jobID,
	})
	s.Storage = fakeStorage{}
	s.rollbackImportedContainer(context.Background(), cluster.ID, appdb.Volume{
		ID: volID, ClusterID: cluster.ID, PoolID: poolID, Class: storage.ClassContainerRoot,
		Format: storage.FormatDirectory, BackendRef: "volumes/container-root/" + volID,
		SizeBytes: 4 << 30, BackendType: storage.BackendDirectory,
	}, "/var/lib/ndl/storage/local/volumes/container-root/"+volID, jobID)
	if got, _ := mem.GetVolume(context.Background(), cluster.ID, volID); got != nil {
		t.Fatal("rollback must forget the dest volume")
	}
	ev, _ := mem.ListEvents(context.Background(), cluster.ID, 20)
	found := false
	for _, e := range ev {
		if e.Type == "migration.volume.rollback" {
			found = true
		}
	}
	if !found {
		t.Fatal("rollback must record evidence")
	}
}

func TestStartAndFinishOpPersistsByID(t *testing.T) {
	s, mem, _ := testServer(t)
	cluster, _ := mem.GetCluster(context.Background())
	op := s.startOp(context.Background(), cluster.ID, "", "pool.create", "validating", 10)
	s.finishOp(context.Background(), op, "succeeded", "directory pool created", 100)
	ops, _ := mem.ListOperations(context.Background(), cluster.ID, 20)
	var got *appdb.Operation
	for i := range ops {
		if ops[i].ID == op.ID {
			got = &ops[i]
		}
	}
	if got == nil || got.State != "succeeded" || got.Stage != "done" {
		t.Fatalf("%+v", got)
	}
}
