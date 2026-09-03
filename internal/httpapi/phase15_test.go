package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/no-dal/ndl-ce/internal/appdb"
	"github.com/no-dal/ndl-ce/internal/auth"
	"github.com/no-dal/ndl-ce/internal/inventory"
	"github.com/no-dal/ndl-ce/internal/lxc"
	"github.com/no-dal/ndl-ce/internal/rbac"
	"github.com/no-dal/ndl-ce/internal/storage"
)

type fakeZFS struct {
	calls  []storage.ZFSOp
	status string
	reason string
}

func (f *fakeZFS) ZFSPool(_ context.Context, op storage.ZFSOp) (storage.ZFSResult, error) {
	f.calls = append(f.calls, op)
	if err := storage.RefuseForceImport(op.Force); err != nil {
		return storage.ZFSResult{}, err
	}
	st := f.status
	if st == "" {
		st = storage.StatusAvailable
	}
	res := storage.ZFSResult{
		Status: st, Reason: f.reason, PoolID: op.PoolID, Name: op.Name, GUID: op.GUID,
		Incremental: true, Capabilities: storage.ZFSCapabilities(),
	}
	switch op.Action {
	case "import":
		res.RootPath = storage.ZFSMountRoot + "/" + op.GUID
	case "create-pool":
		res.GUID = "123456789012345"
		res.RootPath = storage.ZFSMountRoot + "/" + res.GUID
	case "create-volume":
		ds := op.Name + "/" + op.VolumeID
		res.Dataset = ds
		if op.Class == storage.ClassVMDisk {
			res.BackendRef = storage.ZVolPath(ds)
		} else {
			res.BackendRef = storage.ZFSMountRoot + "/" + op.PoolID + "/volumes/" + op.Class + "/" + op.VolumeID
		}
	case "snapshot", "rollback":
		res.Dataset = op.Name + "/" + op.VolumeID
		res.BackendRef = res.Dataset + "@" + op.Snapshot
	case "send":
		res.BackendRef = op.DestPath
	}
	return res, nil
}

func debianInv() inventory.Inventory {
	return inventory.Inventory{
		Host: inventory.Host{ID: "debian", VersionID: "13", Architecture: "amd64"},
	}
}

func TestZFSForceImportRefusedAndCapabilities(t *testing.T) {
	s, mem, token := testServer(t)
	fz := &fakeZFS{}
	s.ZFS = fz
	cluster, _ := mem.GetCluster(context.Background())
	seedNode(t, mem, cluster.ID, debianInv(), false)
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)

	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/storage/zfs/import", strings.NewReader(`{"guid":"1234567890","force":true}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ := ts.Client().Do(req)
	b, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusUnprocessableEntity || !strings.Contains(string(b), "import -f") {
		t.Fatalf("force %d %s", res.StatusCode, b)
	}

	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/storage/zfs/import", strings.NewReader(`{"guid":"1234567890","name":"tank"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(req)
	b, _ = io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("import %d %s", res.StatusCode, b)
	}
	if !strings.Contains(string(b), `"backend_type":"zfs"`) || !strings.Contains(string(b), `"incremental_send":true`) {
		t.Fatalf("caps %s", b)
	}
	var imported map[string]any
	if err := json.Unmarshal(b, &imported); err != nil {
		t.Fatal(err)
	}
	z, err := mem.GetZFSPool(context.Background(), imported["id"].(string))
	if err != nil || z == nil || z.ZPoolGUID != "1234567890" || z.ZPoolName != "tank" {
		t.Fatalf("zpool identity %+v %v", z, err)
	}
	for _, op := range fz.calls {
		if op.Force {
			t.Fatal("force reached engine")
		}
	}
}

func TestZFSCreateRefusesRootDiskAndMakesZvolDataset(t *testing.T) {
	s, mem, token := testServer(t)
	s.ZFS = &fakeZFS{}
	cluster, _ := mem.GetCluster(context.Background())
	seedNode(t, mem, cluster.ID, debianInv(), false)
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)

	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/storage/zfs/create", strings.NewReader(`{"name":"tank","disks":["/"]}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ := ts.Client().Do(req)
	if res.StatusCode != http.StatusBadRequest {
		b, _ := io.ReadAll(res.Body)
		t.Fatalf("root disk %d %s", res.StatusCode, b)
	}
	_ = res.Body.Close()

	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/storage/zfs/create", strings.NewReader(`{"name":"tank","disks":["/dev/disk/by-id/wwn-0x5000"]}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(req)
	b, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("create %d %s", res.StatusCode, b)
	}
	var pool map[string]any
	if err := json.Unmarshal(b, &pool); err != nil {
		t.Fatal(err)
	}
	poolID := pool["id"].(string)
	z, err := mem.GetZFSPool(context.Background(), poolID)
	if err != nil || z == nil || z.ZPoolGUID != "123456789012345" || z.ZPoolName != "tank" {
		t.Fatalf("zpool identity %+v %v", z, err)
	}

	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/storage/volumes", strings.NewReader(`{"pool_id":"`+poolID+`","class":"vm-disk","size_bytes":1073741824}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(req)
	b, _ = io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusCreated || !strings.Contains(string(b), `/dev/zvol/`) || !strings.Contains(string(b), `"format":"zvol"`) {
		t.Fatalf("zvol %d %s", res.StatusCode, b)
	}

	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/storage/volumes", strings.NewReader(`{"pool_id":"`+poolID+`","class":"container-root","size_bytes":1073741824}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(req)
	b, _ = io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusCreated || !strings.Contains(string(b), storage.ZFSMountRoot) || !strings.Contains(string(b), `"format":"dataset"`) {
		t.Fatalf("dataset %d %s", res.StatusCode, b)
	}
}

func TestZFSPulledDiskRowsRemainUnavailable(t *testing.T) {
	s, mem, token := testServer(t)
	fz := &fakeZFS{status: storage.StatusUnavailable, reason: "pool is faulted or missing. Desired rows remain."}
	s.ZFS = fz
	cluster, _ := mem.GetCluster(context.Background())
	node := seedNode(t, mem, cluster.ID, debianInv(), false)
	caps, _ := json.Marshal(storage.ZFSCapabilities())
	pool := appdb.StoragePool{
		ID: uuid.NewString(), ClusterID: cluster.ID, NodeID: node.ID, Name: "tank",
		BackendType: storage.BackendZFS, Status: storage.StatusAvailable, RootPath: storage.ZFSMountRoot + "/1",
		Capabilities: caps,
	}
	if err := mem.CreateStoragePool(context.Background(), pool); err != nil {
		t.Fatal(err)
	}
	if err := mem.UpsertZFSPool(context.Background(), appdb.ZFSPool{PoolID: pool.ID, ZPoolGUID: "1", ZPoolName: "tank"}); err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)
	req, _ := http.NewRequest("GET", ts.URL+"/api/v1/storage/pools", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ := ts.Client().Do(req)
	b, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != 200 || !strings.Contains(string(b), pool.ID) {
		t.Fatalf("rows %d %s", res.StatusCode, b)
	}
	if !strings.Contains(string(b), `"status":"unavailable"`) {
		t.Fatalf("status %s", b)
	}
	if strings.Contains(string(b), `"usable_bytes":0`) {
		t.Fatal("unavailable must not report zero capacity")
	}
}

func TestZFSSnapshotAndFlattenHonesty(t *testing.T) {
	s, mem, token := testServer(t)
	s.ZFS = &fakeZFS{}
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

	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)

	req, _ := http.NewRequest("GET", ts.URL+"/api/v1/workloads/"+wl.ID+"/snapshots", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ := ts.Client().Do(req)
	b, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != 200 || !strings.Contains(string(b), `"mechanism":"zfs"`) || !strings.Contains(string(b), `"supported":true`) {
		t.Fatalf("cap %d %s", res.StatusCode, b)
	}

	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/workloads/"+wl.ID+"/snapshots", strings.NewReader(`{"name":"before"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(req)
	b, _ = io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusCreated || !strings.Contains(string(b), `"mechanism":"zfs"`) {
		t.Fatalf("snap %d %s", res.StatusCode, b)
	}

	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/workloads/"+wl.ID+"/snapshots/flatten", nil)
	req.Header.Set("X-Nodal-Confirm", "flatten")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(req)
	if res.StatusCode != http.StatusUnprocessableEntity {
		b, _ = io.ReadAll(res.Body)
		t.Fatalf("flatten %d %s", res.StatusCode, b)
	}
	_ = res.Body.Close()
}

func TestZFSViewerDeniedAndUbuntuUnsupported(t *testing.T) {
	s, mem, token := testServer(t)
	s.ZFS = &fakeZFS{}
	cluster, _ := mem.GetCluster(context.Background())
	seedNode(t, mem, cluster.ID, inventory.Inventory{Host: inventory.Host{ID: "ubuntu", VersionID: "24.04", Architecture: "amd64"}}, false)
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)

	req, _ := http.NewRequest("GET", ts.URL+"/api/v1/storage/zfs", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ := ts.Client().Do(req)
	b, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != 200 || !strings.Contains(string(b), `"host_supported":false`) || !strings.Contains(string(b), `"directory_default":true`) {
		t.Fatalf("runtime %d %s", res.StatusCode, b)
	}

	hash, _ := auth.HashPassword("password1")
	u := appdb.User{ID: uuid.NewString(), ClusterID: cluster.ID, Username: "view", PasswordHash: hash}
	_ = mem.CreateUser(context.Background(), u)
	_ = mem.BindRole(context.Background(), cluster.ID, u.ID, rbac.Viewer)
	vlogin, _ := ts.Client().Post(ts.URL+"/api/v1/auth/login", "application/json", strings.NewReader(`{"username":"view","password":"password1"}`))
	var viewCookie string
	for _, c := range vlogin.Cookies() {
		if c.Name == sessionCookie {
			viewCookie = c.Value
		}
	}
	_ = vlogin.Body.Close()
	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/storage/zfs/import", strings.NewReader(`{"guid":"1234567890"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: viewCookie})
	res, _ = ts.Client().Do(req)
	if res.StatusCode != http.StatusForbidden {
		b, _ = io.ReadAll(res.Body)
		t.Fatalf("viewer %d %s", res.StatusCode, b)
	}
	_ = res.Body.Close()
}

func TestZFSCreateRefusesInventoryRootDisk(t *testing.T) {
	s, mem, token := testServer(t)
	s.ZFS = &fakeZFS{}
	cluster, _ := mem.GetCluster(context.Background())
	inv := debianInv()
	inv.BlockDevices = []inventory.BlockDevice{{Name: "sda", MountHint: "/"}}
	seedNode(t, mem, cluster.ID, inv, false)
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)
	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/storage/zfs/create", strings.NewReader(`{"name":"tank","disks":["/dev/sda"]}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ := ts.Client().Do(req)
	if res.StatusCode != http.StatusBadRequest {
		b, _ := io.ReadAll(res.Body)
		t.Fatalf("root sda %d %s", res.StatusCode, b)
	}
	_ = res.Body.Close()
}

func TestZFSDirectoryPoolCreateStillRefusesBackendZFS(t *testing.T) {
	s, mem, token := testServer(t)
	s.ZFS = &fakeZFS{}
	cluster, _ := mem.GetCluster(context.Background())
	seedNode(t, mem, cluster.ID, debianInv(), false)
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)
	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/storage/pools", strings.NewReader(`{"name":"z","path":"/var/lib/ndl/storage/z","backend_type":"zfs"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ := ts.Client().Do(req)
	if res.StatusCode != http.StatusBadRequest {
		b, _ := io.ReadAll(res.Body)
		t.Fatalf("dir zfs %d %s", res.StatusCode, b)
	}
	_ = res.Body.Close()
}

func TestZFSCTSnapshotSupported(t *testing.T) {
	s, mem, token := testServer(t)
	s.ZFS = &fakeZFS{}
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
	volID := uuid.NewString()
	vol := appdb.Volume{
		ID: volID, ClusterID: cluster.ID, NodeID: node.ID, PoolID: pool.ID,
		Class: storage.ClassContainerRoot, Kind: storage.KindFilesystem, Format: storage.FormatDataset,
		Status: storage.StatusAvailable, BackendType: storage.BackendZFS,
		BackendRef: storage.ZFSMountRoot + "/" + pool.ID + "/volumes/container-root/" + volID,
	}
	_ = mem.CreateVolume(context.Background(), vol)
	wl := appdb.Workload{ID: uuid.NewString(), ClusterID: cluster.ID, Name: "ct", Kind: lxc.KindSystemContainer, Status: "stopped"}
	_ = mem.CreateWorkload(context.Background(), wl)
	_ = mem.CreateWorkloadDisk(context.Background(), appdb.WorkloadDisk{ID: uuid.NewString(), ClusterID: cluster.ID, WorkloadID: wl.ID, VolumeID: vol.ID, Role: "root"})
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)
	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/workloads/"+wl.ID+"/snapshots", strings.NewReader(`{"name":"ct1"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ := ts.Client().Do(req)
	b, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("ct snap %d %s", res.StatusCode, b)
	}
}

type failUpsertZFSPoolStore struct {
	appdb.Store
}

func (f failUpsertZFSPoolStore) UpsertZFSPool(context.Context, appdb.ZFSPool) error {
	return errors.New("persist failed")
}

func TestZFSImportFailsClosedWhenIdentityPersistFails(t *testing.T) {
	s, mem, token := testServer(t)
	s.ZFS = &fakeZFS{}
	s.Store = failUpsertZFSPoolStore{Store: mem}
	cluster, _ := mem.GetCluster(context.Background())
	seedNode(t, mem, cluster.ID, debianInv(), false)
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)
	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/storage/zfs/import", strings.NewReader(`{"guid":"1234567890","name":"tank"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ := ts.Client().Do(req)
	b, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusInternalServerError {
		t.Fatalf("import persist %d %s", res.StatusCode, b)
	}
	if !strings.Contains(string(b), "could not record zpool identity") {
		t.Fatalf("import persist body %s", b)
	}
}

func TestZFSCreateFailsClosedWhenIdentityPersistFails(t *testing.T) {
	s, mem, token := testServer(t)
	s.ZFS = &fakeZFS{}
	s.Store = failUpsertZFSPoolStore{Store: mem}
	cluster, _ := mem.GetCluster(context.Background())
	seedNode(t, mem, cluster.ID, debianInv(), false)
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)
	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/storage/zfs/create", strings.NewReader(`{"name":"tank","disks":["/dev/disk/by-id/wwn-0x5000"]}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ := ts.Client().Do(req)
	b, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusInternalServerError {
		t.Fatalf("create persist %d %s", res.StatusCode, b)
	}
	if !strings.Contains(string(b), "could not record zpool identity") {
		t.Fatalf("create persist body %s", b)
	}
}

func TestZFSVolumeCreateFailsClosedForUnsupportedClass(t *testing.T) {
	s, mem, token := testServer(t)
	s.ZFS = &fakeZFS{}
	cluster, _ := mem.GetCluster(context.Background())
	seedNode(t, mem, cluster.ID, debianInv(), false)
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)
	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/storage/zfs/create", strings.NewReader(`{"name":"tank","disks":["/dev/disk/by-id/wwn-0x5000"]}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ := ts.Client().Do(req)
	b, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("create pool %d %s", res.StatusCode, b)
	}
	var pool map[string]any
	if err := json.Unmarshal(b, &pool); err != nil {
		t.Fatal(err)
	}
	poolID := pool["id"].(string)
	for _, class := range []string{"garbage", "../etc", ""} {
		body := `{"pool_id":"` + poolID + `","class":"` + class + `","size_bytes":1073741824}`
		volReq, _ := http.NewRequest("POST", ts.URL+"/api/v1/storage/volumes", strings.NewReader(body))
		volReq.Header.Set("Content-Type", "application/json")
		volReq.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
		volRes, _ := ts.Client().Do(volReq)
		raw, _ := io.ReadAll(volRes.Body)
		_ = volRes.Body.Close()
		if volRes.StatusCode != http.StatusBadRequest {
			t.Fatalf("class %q %d %s", class, volRes.StatusCode, raw)
		}
		if !strings.Contains(string(raw), "storage class is unsupported") {
			t.Fatalf("class %q body %s", class, raw)
		}
	}
	items, _ := mem.ListVolumes(context.Background(), cluster.ID, poolID)
	if len(items) != 0 {
		t.Fatalf("GET must not list a volume whose class apply cannot create: %+v", items)
	}
}

func TestZFSVolumeCreateFailsClosedForTinySize(t *testing.T) {
	s, mem, token := testServer(t)
	s.ZFS = &fakeZFS{}
	cluster, _ := mem.GetCluster(context.Background())
	seedNode(t, mem, cluster.ID, debianInv(), false)
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)
	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/storage/zfs/create", strings.NewReader(`{"name":"tank","disks":["/dev/disk/by-id/wwn-0x5000"]}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ := ts.Client().Do(req)
	b, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("create pool %d %s", res.StatusCode, b)
	}
	var pool map[string]any
	if err := json.Unmarshal(b, &pool); err != nil {
		t.Fatal(err)
	}
	poolID := pool["id"].(string)
	body := `{"pool_id":"` + poolID + `","class":"vm-disk","size_bytes":1}`
	volReq, _ := http.NewRequest("POST", ts.URL+"/api/v1/storage/volumes", strings.NewReader(body))
	volReq.Header.Set("Content-Type", "application/json")
	volReq.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	volRes, _ := ts.Client().Do(volReq)
	raw, _ := io.ReadAll(volRes.Body)
	_ = volRes.Body.Close()
	if volRes.StatusCode != http.StatusBadRequest {
		t.Fatalf("tiny zvol %d %s", volRes.StatusCode, raw)
	}
	if !strings.Contains(string(raw), storage.ErrInvalidSize.Error()) {
		t.Fatalf("tiny zvol body %s", raw)
	}
	items, _ := mem.ListVolumes(context.Background(), cluster.ID, poolID)
	if len(items) != 0 {
		t.Fatalf("GET must not list a zvol apply cannot create: %+v", items)
	}
}

func TestPhase15VMCreateUsesZVol(t *testing.T) {
	s, mem, token := testServer(t)
	fz := &fakeZFS{}
	s.ZFS = fz
	cluster, _ := mem.GetCluster(context.Background())
	node := seedNode(t, mem, cluster.ID, debianInv(), false)
	_, netID := seedCompute(t, mem, cluster.ID, node.ID)
	poolID := uuid.NewString()
	if err := mem.CreateStoragePool(context.Background(), appdb.StoragePool{
		ID: poolID, ClusterID: cluster.ID, NodeID: node.ID, Name: "tank",
		BackendType: storage.BackendZFS, Status: storage.StatusAvailable,
		RootPath: storage.ZFSMountRoot + "/1",
	}); err != nil {
		t.Fatal(err)
	}
	if err := mem.UpsertZFSPool(context.Background(), appdb.ZFSPool{PoolID: poolID, ZPoolGUID: "1", ZPoolName: "tank"}); err != nil {
		t.Fatal(err)
	}
	s.Storage = fakeStorage{vol: storage.CreateVolumeResult{Handle: storage.VolumeHandle{
		BackendType: storage.BackendDirectory, BackendRef: "volumes/vm-disk/boot.qcow2",
		Kind: storage.KindBlock, Class: storage.ClassVMDisk, Format: storage.FormatQCOW2,
	}}}
	vm := &fakeVM{}
	s.VM = vm
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)
	body := `{"name":"zfs-vm","kind":"vm","pool_id":"` + poolID + `","network_id":"` + netID + `"}`
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
		t.Fatalf("create %d %s", res.StatusCode, raw)
	}
	created := 0
	for _, op := range fz.calls {
		if op.Action == "create-volume" {
			created++
		}
	}
	if created != 1 {
		t.Fatalf("create-volume calls %d %+v", created, fz.calls)
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	id, _ := out["id"].(string)
	disks, _ := mem.ListWorkloadDisks(context.Background(), cluster.ID, id)
	if len(disks) != 1 {
		t.Fatalf("disks %+v", disks)
	}
	vol, _ := mem.GetVolume(context.Background(), cluster.ID, disks[0].VolumeID)
	if vol == nil || vol.BackendType != storage.BackendZFS {
		t.Fatalf("boot volume %+v", vol)
	}
	if err := storage.ValidateZVolPath(vol.BackendRef); err != nil {
		t.Fatalf("boot BackendRef must be a zvol: %s %v", vol.BackendRef, err)
	}
	if strings.Contains(vol.BackendRef, "volumes/vm-disk/") {
		t.Fatalf("boot must not be a directory qcow2 on the ZFS pool: %s", vol.BackendRef)
	}
	if len(vm.launch.Disks) != 1 || vm.launch.Disks[0].Path != vol.BackendRef || vm.launch.Disks[0].Format != "raw" {
		t.Fatalf("launch must attach the zvol: %+v want %s", vm.launch.Disks, vol.BackendRef)
	}
}

func TestPhase15VMCreateFailsClosedWhenZFSUnavailable(t *testing.T) {
	s, mem, token := testServer(t)
	s.ZFS = &fakeZFS{status: storage.StatusUnavailable, reason: storage.ZFSMissing}
	cluster, _ := mem.GetCluster(context.Background())
	node := seedNode(t, mem, cluster.ID, debianInv(), false)
	_, netID := seedCompute(t, mem, cluster.ID, node.ID)
	poolID := uuid.NewString()
	if err := mem.CreateStoragePool(context.Background(), appdb.StoragePool{
		ID: poolID, ClusterID: cluster.ID, NodeID: node.ID, Name: "tank-down",
		BackendType: storage.BackendZFS, Status: storage.StatusAvailable,
		RootPath: storage.ZFSMountRoot + "/1",
	}); err != nil {
		t.Fatal(err)
	}
	s.Storage = fakeStorage{vol: storage.CreateVolumeResult{Handle: storage.VolumeHandle{
		BackendType: storage.BackendDirectory, BackendRef: "volumes/vm-disk/boot.qcow2",
		Kind: storage.KindBlock, Class: storage.ClassVMDisk, Format: storage.FormatQCOW2,
	}}}
	s.VM = &fakeVM{}
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)
	body := `{"name":"zfs-down","kind":"vm","pool_id":"` + poolID + `","network_id":"` + netID + `"}`
	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/workloads", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("unavailable ZFS %d %s", res.StatusCode, raw)
	}
	if !strings.Contains(string(raw), storage.ZFSMissing) {
		t.Fatalf("unavailable ZFS body %s", raw)
	}
	items, _ := mem.ListWorkloads(context.Background(), cluster.ID)
	if len(items) != 0 {
		t.Fatalf("GET must not list a VM whose zvol apply cannot create: %+v", items)
	}
}

func TestPhase15VMCloneExportAndTemplateFailClosedForZFS(t *testing.T) {
	s, mem, token := testServer(t)
	s.ZFS = &fakeZFS{}
	cluster, _ := mem.GetCluster(context.Background())
	node := seedNode(t, mem, cluster.ID, debianInv(), false)
	_, netID := seedCompute(t, mem, cluster.ID, node.ID)
	poolID := uuid.NewString()
	if err := mem.CreateStoragePool(context.Background(), appdb.StoragePool{
		ID: poolID, ClusterID: cluster.ID, NodeID: node.ID, Name: "tank",
		BackendType: storage.BackendZFS, Status: storage.StatusAvailable,
		RootPath: storage.ZFSMountRoot + "/1",
	}); err != nil {
		t.Fatal(err)
	}
	if err := mem.UpsertZFSPool(context.Background(), appdb.ZFSPool{PoolID: poolID, ZPoolGUID: "1", ZPoolName: "tank"}); err != nil {
		t.Fatal(err)
	}
	s.Storage = fakeStorage{vol: storage.CreateVolumeResult{Handle: storage.VolumeHandle{
		BackendType: storage.BackendDirectory, BackendRef: "volumes/vm-disk/boot.qcow2",
		Kind: storage.KindBlock, Class: storage.ClassVMDisk, Format: storage.FormatQCOW2,
	}}}
	s.VM = &fakeVM{}
	s.Backup = &fakeBackup{}
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)
	body := `{"name":"zfs-src","kind":"vm","pool_id":"` + poolID + `","network_id":"` + netID + `"}`
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
		t.Fatalf("create %d %s", res.StatusCode, raw)
	}
	var created map[string]any
	if err := json.Unmarshal(raw, &created); err != nil {
		t.Fatal(err)
	}
	id, _ := created["id"].(string)
	disks, _ := mem.ListWorkloadDisks(context.Background(), cluster.ID, id)
	if len(disks) != 1 {
		t.Fatalf("disks %+v", disks)
	}
	bootRef := ""
	if vol, _ := mem.GetVolume(context.Background(), cluster.ID, disks[0].VolumeID); vol != nil {
		bootRef = vol.BackendRef
	}

	clone, _ := http.NewRequest("POST", ts.URL+"/api/v1/workloads/"+id+"/clone", strings.NewReader(`{"name":"zfs-clone"}`))
	clone.Header.Set("Content-Type", "application/json")
	clone.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	cres, err := ts.Client().Do(clone)
	if err != nil {
		t.Fatal(err)
	}
	craw, _ := io.ReadAll(cres.Body)
	_ = cres.Body.Close()
	if cres.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("clone %d %s", cres.StatusCode, craw)
	}
	if !strings.Contains(string(craw), "ZFS pools do not store directory qcow2 copies") {
		t.Fatalf("clone body %s", craw)
	}

	exp, _ := http.NewRequest("POST", ts.URL+"/api/v1/workloads/"+id+"/export", strings.NewReader(`{}`))
	exp.Header.Set("Content-Type", "application/json")
	exp.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	eres, err := ts.Client().Do(exp)
	if err != nil {
		t.Fatal(err)
	}
	eraw, _ := io.ReadAll(eres.Body)
	_ = eres.Body.Close()
	if eres.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("export %d %s", eres.StatusCode, eraw)
	}
	if !strings.Contains(string(eraw), "ZFS pools do not store directory qcow2 copies") {
		t.Fatalf("export body %s", eraw)
	}

	tmpl, _ := http.NewRequest("POST", ts.URL+"/api/v1/templates", strings.NewReader(`{"workload_id":"`+id+`","name":"golden"}`))
	tmpl.Header.Set("Content-Type", "application/json")
	tmpl.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	tres, err := ts.Client().Do(tmpl)
	if err != nil {
		t.Fatal(err)
	}
	traw, _ := io.ReadAll(tres.Body)
	_ = tres.Body.Close()
	if tres.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("template %d %s", tres.StatusCode, traw)
	}
	if !strings.Contains(string(traw), zfsTemplateReason) {
		t.Fatalf("template body %s", traw)
	}

	items, _ := mem.ListWorkloads(context.Background(), cluster.ID)
	if len(items) != 1 || items[0].ID != id {
		t.Fatalf("GET must keep the source VM and not list a directory clone: %+v", items)
	}
	libs, _ := mem.ListLibraryItems(context.Background(), cluster.ID, poolID)
	if len(libs) != 0 {
		t.Fatalf("GET /images must not list an export under the ZFS mount: %+v", libs)
	}
	snaps, _ := mem.ListSnapshots(context.Background(), cluster.ID, id)
	if len(snaps) != 0 {
		t.Fatalf("GET snapshots must not list a qcow2 template overlay on a zvol: %+v", snaps)
	}
	vol, _ := mem.GetVolume(context.Background(), cluster.ID, disks[0].VolumeID)
	if vol == nil || vol.BackendRef != bootRef || storage.ValidateZVolPath(vol.BackendRef) != nil {
		t.Fatalf("boot locator must stay a zvol: %+v want %s", vol, bootRef)
	}
}

func TestPhase15VMRestoreFailsClosedForZFS(t *testing.T) {
	s, mem, token := testServer(t)
	s.ZFS = &fakeZFS{}
	cluster, _ := mem.GetCluster(context.Background())
	node := seedNode(t, mem, cluster.ID, debianInv(), false)
	_, netID := seedCompute(t, mem, cluster.ID, node.ID)
	poolID := uuid.NewString()
	if err := mem.CreateStoragePool(context.Background(), appdb.StoragePool{
		ID: poolID, ClusterID: cluster.ID, NodeID: node.ID, Name: "tank",
		BackendType: storage.BackendZFS, Status: storage.StatusAvailable,
		RootPath: storage.ZFSMountRoot + "/1",
	}); err != nil {
		t.Fatal(err)
	}
	if err := mem.UpsertZFSPool(context.Background(), appdb.ZFSPool{PoolID: poolID, ZPoolGUID: "1", ZPoolName: "tank"}); err != nil {
		t.Fatal(err)
	}
	s.Storage = fakeStorage{vol: storage.CreateVolumeResult{Handle: storage.VolumeHandle{
		BackendType: storage.BackendDirectory, BackendRef: "volumes/vm-disk/boot.qcow2",
		Kind: storage.KindBlock, Class: storage.ClassVMDisk, Format: storage.FormatQCOW2,
	}}}
	vm := &fakeVM{}
	s.VM = vm
	fb := &fakeBackup{}
	s.Backup = fb
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)
	body := `{"name":"zfs-restore","kind":"vm","pool_id":"` + poolID + `","network_id":"` + netID + `"}`
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
		t.Fatalf("create %d %s", res.StatusCode, raw)
	}
	var created map[string]any
	if err := json.Unmarshal(raw, &created); err != nil {
		t.Fatal(err)
	}
	id, _ := created["id"].(string)
	disks, _ := mem.ListWorkloadDisks(context.Background(), cluster.ID, id)
	if len(disks) != 1 {
		t.Fatalf("disks %+v", disks)
	}
	bootRef := ""
	if vol, _ := mem.GetVolume(context.Background(), cluster.ID, disks[0].VolumeID); vol != nil {
		bootRef = vol.BackendRef
	}
	artID := seedVMRestoreArtifact(t, mem, cluster.ID, id, "volumes/vm-disk/boot.qcow2")

	restoreNew, _ := http.NewRequest("POST", ts.URL+"/api/v1/backups/artifacts/"+artID+"/restore", strings.NewReader(`{"mode":"new"}`))
	restoreNew.Header.Set("Content-Type", "application/json")
	restoreNew.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	nres, err := ts.Client().Do(restoreNew)
	if err != nil {
		t.Fatal(err)
	}
	nraw, _ := io.ReadAll(nres.Body)
	_ = nres.Body.Close()
	if nres.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("restore new %d %s", nres.StatusCode, nraw)
	}
	if !strings.Contains(string(nraw), "ZFS pools do not store directory qcow2 copies") {
		t.Fatalf("restore new body %s", nraw)
	}

	restore, _ := http.NewRequest("POST", ts.URL+"/api/v1/backups/artifacts/"+artID+"/restore", strings.NewReader(`{"mode":"replace"}`))
	restore.Header.Set("Content-Type", "application/json")
	restore.Header.Set("X-Nodal-Confirm", "restore")
	restore.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	rres, err := ts.Client().Do(restore)
	if err != nil {
		t.Fatal(err)
	}
	rraw, _ := io.ReadAll(rres.Body)
	_ = rres.Body.Close()
	if rres.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("restore replace %d %s", rres.StatusCode, rraw)
	}
	if !strings.Contains(string(rraw), "ZFS pools do not store directory qcow2 copies") {
		t.Fatalf("restore replace body %s", rraw)
	}
	for _, c := range fb.copies {
		if c[0] == "replace" {
			t.Fatalf("replace must not qemu-img onto the zvol: %+v", fb.copies)
		}
	}
	for _, a := range vm.actions {
		if a == "stop" {
			t.Fatalf("replace must not stop before dest refuse: %+v", vm.actions)
		}
	}
	items, _ := mem.ListWorkloads(context.Background(), cluster.ID)
	if len(items) != 1 || items[0].ID != id {
		t.Fatalf("GET must keep the source VM and not list a directory restore: %+v", items)
	}
	vol, _ := mem.GetVolume(context.Background(), cluster.ID, disks[0].VolumeID)
	if vol == nil || vol.BackendRef != bootRef || storage.ValidateZVolPath(vol.BackendRef) != nil {
		t.Fatalf("boot locator must stay a zvol: %+v want %s", vol, bootRef)
	}
}

func TestPhase15VMMigrationExportFailsClosedForZFS(t *testing.T) {
	s, mem, token := testServer(t)
	s.ZFS = &fakeZFS{}
	cluster, _ := mem.GetCluster(context.Background())
	node := seedNode(t, mem, cluster.ID, debianInv(), false)
	_, netID := seedCompute(t, mem, cluster.ID, node.ID)
	poolID := uuid.NewString()
	if err := mem.CreateStoragePool(context.Background(), appdb.StoragePool{
		ID: poolID, ClusterID: cluster.ID, NodeID: node.ID, Name: "tank",
		BackendType: storage.BackendZFS, Status: storage.StatusAvailable,
		RootPath: storage.ZFSMountRoot + "/1",
	}); err != nil {
		t.Fatal(err)
	}
	if err := mem.UpsertZFSPool(context.Background(), appdb.ZFSPool{PoolID: poolID, ZPoolGUID: "1", ZPoolName: "tank"}); err != nil {
		t.Fatal(err)
	}
	s.Storage = fakeStorage{vol: storage.CreateVolumeResult{Handle: storage.VolumeHandle{
		BackendType: storage.BackendDirectory, BackendRef: "volumes/vm-disk/boot.qcow2",
		Kind: storage.KindBlock, Class: storage.ClassVMDisk, Format: storage.FormatQCOW2,
	}}}
	s.VM = &fakeVM{}
	fb := &fakeBackup{}
	s.Backup = fb
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)
	body := `{"name":"zfs-export","kind":"vm","pool_id":"` + poolID + `","network_id":"` + netID + `"}`
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
		t.Fatalf("create %d %s", res.StatusCode, raw)
	}
	var created map[string]any
	if err := json.Unmarshal(raw, &created); err != nil {
		t.Fatal(err)
	}
	id, _ := created["id"].(string)

	exp, _ := http.NewRequest("POST", ts.URL+"/api/v1/migration/jobs", strings.NewReader(`{"direction":"export","workload_id":"`+id+`"}`))
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
	if !strings.Contains(string(eraw), "ZFS pools do not store directory qcow2 copies") {
		t.Fatalf("migration export body %s", eraw)
	}
	if len(fb.converts) != 0 {
		t.Fatalf("ConvertImport must not qemu-img a zvol as qcow2: %+v", fb.converts)
	}
	jobs, _ := mem.ListMigrationJobs(context.Background(), cluster.ID, 100)
	if len(jobs) != 0 {
		t.Fatalf("GET must not list a succeeded zvol export: %+v", jobs)
	}
}
