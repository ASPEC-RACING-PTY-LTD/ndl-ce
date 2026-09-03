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
	"github.com/no-dal/ndl-ce/internal/features"
	"github.com/no-dal/ndl-ce/internal/storage"
)

type fakeDistributed struct {
	up    bool
	calls []storage.DistributedOp
	osdOn bool
}

func (f *fakeDistributed) Distributed(_ context.Context, op storage.DistributedOp) (storage.DistributedResult, error) {
	f.calls = append(f.calls, op)
	if storage.ArgvContainsSecret([]string{op.CephxKey}, op.CephxKey) && op.Action == "never" {
		return storage.DistributedResult{}, nil
	}
	switch op.Action {
	case "attach", "observe":
		if !f.up {
			return storage.DistributedResult{
				Status: storage.StatusUnavailable, Reason: storage.ClusterDownMsg,
				PoolID: op.PoolID, BackendType: storage.BackendDistributed,
				Capabilities: storage.DistributedCapabilities(), Incremental: false,
			}, nil
		}
		return storage.DistributedResult{
			Status: storage.StatusAvailable, PoolID: op.PoolID, BackendType: storage.BackendDistributed,
			RootPath: storage.RBDDevPrefix + "rbd", Capabilities: storage.DistributedCapabilities(),
		}, nil
	case "create-volume":
		if !f.up {
			return storage.DistributedResult{
				Status: storage.StatusUnavailable, Reason: storage.ClusterDownMsg,
				Capabilities: storage.DistributedCapabilities(), Incremental: false,
			}, nil
		}
		dev, _ := storage.RBDDevicePath("rbd", op.VolumeID)
		return storage.DistributedResult{
			Status: storage.StatusAvailable, PoolID: op.PoolID, BackendType: storage.BackendDistributed,
			BackendRef: dev, Kind: storage.KindBlock, Format: storage.FormatRaw, Class: storage.ClassVMDisk,
			Capabilities: storage.DistributedCapabilities(),
			Argv:         []string{storage.RBDBin, "create", "rbd/" + op.VolumeID},
		}, nil
	case "osd-create":
		return storage.DistributedResult{
			Status: storage.StatusUnavailable, OSDStarted: false, Reason: storage.OSDNotStarted,
			Argv:         []string{storage.CephVolumeBin, "lvm", "create", "--data", op.Disk},
			Capabilities: storage.DistributedCapabilities(),
		}, nil
	default:
		return storage.DistributedResult{Status: storage.StatusUnavailable, Reason: "unsupported"}, nil
	}
}

func enableDistributed(t *testing.T, ts *httptest.Server, cookie string) {
	t.Helper()
	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/features/distributed_storage/enable", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("enable %d %s", res.StatusCode, raw)
	}
}

func TestPhase39AttachFakeRBDAndClusterDown(t *testing.T) {
	s, mem, token := testServer(t)
	s.Update = &fakeUpdate{supported: true}
	fd := &fakeDistributed{up: true}
	s.Distributed = fd
	cluster, _ := mem.GetCluster(context.Background())
	seedNode(t, mem, cluster.ID, debianInv(), false)
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)

	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/storage/distributed", strings.NewReader(`{"name":"ceph","locator":"mon.example/rbd","cephx_key":"AQBfakekeyvalue0123456789abcd=="}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ := ts.Client().Do(req)
	raw, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("attach before feature %d %s", res.StatusCode, raw)
	}

	enableDistributed(t, ts, cookie)

	req, _ = http.NewRequest("GET", ts.URL+"/api/v1/storage/distributed", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(req)
	raw, _ = io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK || strings.Contains(string(raw), "AQBfake") {
		t.Fatalf("runtime %d %s", res.StatusCode, raw)
	}
	var rt map[string]any
	if err := json.Unmarshal(raw, &rt); err != nil {
		t.Fatal(err)
	}
	if rt["osd_started"] != false || rt["osd_process"] != false || rt["feature_enabled"] != true {
		t.Fatalf("enable must not start OSDs %s", raw)
	}

	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/storage/distributed", strings.NewReader(`{"name":"ceph","locator":"mon.example/rbd","cephx_key":"AQBfakekeyvalue0123456789abcd=="}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(req)
	raw, _ = io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("attach %d %s", res.StatusCode, raw)
	}
	if strings.Contains(string(raw), "AQBfake") || strings.Contains(string(raw), "cephx_key") {
		t.Fatalf("key leaked %s", raw)
	}
	var pool map[string]any
	if err := json.Unmarshal(raw, &pool); err != nil {
		t.Fatal(err)
	}
	if pool["backend_type"] != storage.BackendDistributed {
		t.Fatalf("%s", raw)
	}
	poolID, _ := pool["id"].(string)
	dp, err := mem.GetDistributedPool(context.Background(), poolID)
	if err != nil || dp == nil || dp.Locator == "" || dp.CephPool != "rbd" {
		t.Fatalf("distributed locator %+v %v", dp, err)
	}
	key, err := mem.DistributedSecret(context.Background(), poolID)
	if err != nil || key != "AQBfakekeyvalue0123456789abcd==" {
		t.Fatalf("stored cephx %q %v", key, err)
	}

	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/storage/volumes", strings.NewReader(`{"pool_id":"`+poolID+`","class":"vm-disk","size_bytes":1073741824}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(req)
	raw, _ = io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("volume %d %s", res.StatusCode, raw)
	}
	var vol map[string]any
	if err := json.Unmarshal(raw, &vol); err != nil {
		t.Fatal(err)
	}
	ref, _ := vol["backend_ref"].(string)
	if err := storage.ValidateRBDPath(ref); err != nil {
		t.Fatalf("fake rbd handle %s %v", ref, err)
	}
	if vol["kind"] != storage.KindBlock || vol["format"] != storage.FormatRaw {
		t.Fatalf("%s", raw)
	}

	fd.up = false
	s.refreshStorage(context.Background(), cluster.ID)
	got, _ := mem.GetVolume(context.Background(), cluster.ID, vol["id"].(string))
	if got == nil || got.Status != storage.StatusUnavailable {
		t.Fatalf("cluster down must mark volume unavailable %+v", got)
	}

	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/storage/volumes", strings.NewReader(`{"pool_id":"`+poolID+`","class":"vm-disk","size_bytes":1073741824}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(req)
	raw, _ = io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode == http.StatusCreated {
		t.Fatalf("create while down must fail %s", raw)
	}
}

func TestPhase39OSDBringUpConfirmAndRootDisk(t *testing.T) {
	s, mem, token := testServer(t)
	s.Update = &fakeUpdate{supported: true}
	s.Distributed = &fakeDistributed{up: true}
	s.OSDProcs = func() []string { return []string{"ndl-control"} }
	cluster, _ := mem.GetCluster(context.Background())
	seedNode(t, mem, cluster.ID, debianInv(), false)
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)

	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/storage/distributed/osds", strings.NewReader(`{"disk":"/dev/disk/by-id/wwn-0x5000"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(confirmHeader, storage.StartOSDConfirm)
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ := ts.Client().Do(req)
	raw, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusUnprocessableEntity || !strings.Contains(string(raw), "feature") {
		t.Fatalf("osd before feature %d %s", res.StatusCode, raw)
	}

	enableDistributed(t, ts, cookie)

	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/storage/distributed/osds", strings.NewReader(`{"disk":"/dev/disk/by-id/wwn-0x5000"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(req)
	raw, _ = io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusUnprocessableEntity || !strings.Contains(string(raw), "start-ceph-osd") {
		t.Fatalf("confirm %d %s", res.StatusCode, raw)
	}

	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/storage/distributed/osds", strings.NewReader(`{"disk":"/"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(confirmHeader, storage.StartOSDConfirm)
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(req)
	raw, _ = io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusBadRequest || !strings.Contains(string(raw), "extra disks") {
		t.Fatalf("root %d %s", res.StatusCode, raw)
	}

	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/storage/distributed/osds", strings.NewReader(`{"disk":"/dev/disk/by-id/wwn-0x5000"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(confirmHeader, storage.StartOSDConfirm)
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(req)
	raw, _ = io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("osd %d %s", res.StatusCode, raw)
	}
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatal(err)
	}
	if body["osd_started"] != false {
		t.Fatalf("skip host must not start OSD %s", raw)
	}
	if strings.Contains(string(raw), "bash") {
		t.Fatalf("argv %s", raw)
	}

	feat, _ := mem.GetFeature(context.Background(), cluster.ID, features.IDDistStorage)
	if feat != nil && feat.RuntimeStatus == appdb.FeatureRunning {
		t.Fatalf("feature enable must not mark OSD running %+v", feat)
	}
	osds, err := mem.ListDistributedOSDs(context.Background(), cluster.ID)
	if err != nil || len(osds) != 1 || osds[0].Disk != "/dev/disk/by-id/wwn-0x5000" {
		t.Fatalf("osd row %+v %v", osds, err)
	}
}

func TestPhase39OSDCreateFailsClosedForMissingPool(t *testing.T) {
	s, mem, token := testServer(t)
	s.Update = &fakeUpdate{supported: true}
	s.Distributed = &fakeDistributed{up: true}
	cluster, _ := mem.GetCluster(context.Background())
	seedNode(t, mem, cluster.ID, debianInv(), false)
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)
	enableDistributed(t, ts, cookie)

	missing := uuid.NewString()
	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/storage/distributed/osds", strings.NewReader(`{"disk":"/dev/disk/by-id/wwn-0x5000","pool_id":"`+missing+`"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(confirmHeader, storage.StartOSDConfirm)
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ := ts.Client().Do(req)
	raw, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusNotFound || !strings.Contains(string(raw), "storage pool not found") {
		t.Fatalf("missing pool %d %s", res.StatusCode, raw)
	}

	osds, err := mem.ListDistributedOSDs(context.Background(), cluster.ID)
	if err != nil || len(osds) != 0 {
		t.Fatalf("GET must not list an OSD for a pool GET /storage/pools would miss: %+v %v", osds, err)
	}
}

type failUpsertDistributedPoolStore struct {
	appdb.Store
}

func (f failUpsertDistributedPoolStore) UpsertDistributedPool(context.Context, appdb.DistributedPool) error {
	return errors.New("persist failed")
}

type failUpsertDistributedSecretStore struct {
	appdb.Store
}

func (f failUpsertDistributedSecretStore) UpsertDistributedSecret(context.Context, string, string) error {
	return errors.New("persist failed")
}

type failCreateDistributedOSDStore struct {
	appdb.Store
}

func (f failCreateDistributedOSDStore) CreateDistributedOSD(context.Context, appdb.DistributedOSD) error {
	return errors.New("persist failed")
}

func TestPhase39AttachFailsClosedWhenLocatorPersistFails(t *testing.T) {
	s, mem, token := testServer(t)
	s.Update = &fakeUpdate{supported: true}
	s.Distributed = &fakeDistributed{up: true}
	cluster, _ := mem.GetCluster(context.Background())
	seedNode(t, mem, cluster.ID, debianInv(), false)
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)
	enableDistributed(t, ts, cookie)
	s.Store = failUpsertDistributedPoolStore{Store: mem}

	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/storage/distributed", strings.NewReader(`{"name":"ceph","locator":"mon.example/rbd","cephx_key":"AQBfakekeyvalue0123456789abcd=="}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ := ts.Client().Do(req)
	raw, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusInternalServerError {
		t.Fatalf("attach persist %d %s", res.StatusCode, raw)
	}
	if strings.Contains(string(raw), "AQBfake") {
		t.Fatalf("key leaked %s", raw)
	}
	if !strings.Contains(string(raw), "could not record distributed pool") {
		t.Fatalf("attach persist body %s", raw)
	}
}

func TestPhase39AttachFailsClosedWhenSecretPersistFails(t *testing.T) {
	s, mem, token := testServer(t)
	s.Update = &fakeUpdate{supported: true}
	s.Distributed = &fakeDistributed{up: true}
	cluster, _ := mem.GetCluster(context.Background())
	seedNode(t, mem, cluster.ID, debianInv(), false)
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)
	enableDistributed(t, ts, cookie)
	s.Store = failUpsertDistributedSecretStore{Store: mem}

	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/storage/distributed", strings.NewReader(`{"name":"ceph","locator":"mon.example/rbd","cephx_key":"AQBfakekeyvalue0123456789abcd=="}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ := ts.Client().Do(req)
	raw, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusInternalServerError {
		t.Fatalf("secret persist %d %s", res.StatusCode, raw)
	}
	if strings.Contains(string(raw), "AQBfake") {
		t.Fatalf("key leaked %s", raw)
	}
	if !strings.Contains(string(raw), "could not record distributed credentials") {
		t.Fatalf("secret persist body %s", raw)
	}
}

func TestPhase39OSDFailsClosedWhenPersistFails(t *testing.T) {
	s, mem, token := testServer(t)
	s.Update = &fakeUpdate{supported: true}
	s.Distributed = &fakeDistributed{up: true}
	cluster, _ := mem.GetCluster(context.Background())
	seedNode(t, mem, cluster.ID, debianInv(), false)
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)
	enableDistributed(t, ts, cookie)
	s.Store = failCreateDistributedOSDStore{Store: mem}

	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/storage/distributed/osds", strings.NewReader(`{"disk":"/dev/disk/by-id/wwn-0x5000"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(confirmHeader, storage.StartOSDConfirm)
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ := ts.Client().Do(req)
	raw, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusInternalServerError {
		t.Fatalf("osd persist %d %s", res.StatusCode, raw)
	}
	if !strings.Contains(string(raw), "could not record OSD") {
		t.Fatalf("osd persist body %s", raw)
	}
}

func seedDistributedPool(t *testing.T, mem *appdb.Memory, clusterID, nodeID string) string {
	t.Helper()
	poolID := uuid.NewString()
	if err := mem.CreateStoragePool(context.Background(), appdb.StoragePool{
		ID: poolID, ClusterID: clusterID, NodeID: nodeID, Name: "ceph",
		BackendType: storage.BackendDistributed, Status: storage.StatusAvailable,
		RootPath: storage.RBDDevPrefix + "rbd",
	}); err != nil {
		t.Fatal(err)
	}
	if err := mem.UpsertDistributedPool(context.Background(), appdb.DistributedPool{
		PoolID: poolID, ClusterID: clusterID, Locator: "mon.example:6789/rbd", CephPool: "rbd", CephUser: "admin",
	}); err != nil {
		t.Fatal(err)
	}
	if err := mem.UpsertDistributedSecret(context.Background(), poolID, "AQBfakekeyvalue0123456789abcd=="); err != nil {
		t.Fatal(err)
	}
	return poolID
}

func TestPhase39VMCreateUsesRBDHandle(t *testing.T) {
	s, mem, token := testServer(t)
	cluster, _ := mem.GetCluster(context.Background())
	nodeID := uuid.NewString()
	_ = mem.UpsertNode(context.Background(), appdb.Node{ID: nodeID, ClusterID: cluster.ID, Name: "local"})
	_, netID := seedCompute(t, mem, cluster.ID, nodeID)
	poolID := seedDistributedPool(t, mem, cluster.ID, nodeID)
	fd := &fakeDistributed{up: true}
	s.Distributed = fd
	s.Storage = fakeStorage{vol: storage.CreateVolumeResult{Handle: storage.VolumeHandle{
		BackendType: storage.BackendDirectory, BackendRef: "volumes/vm-disk/boot.qcow2",
		Kind: storage.KindBlock, Class: storage.ClassVMDisk, Format: storage.FormatQCOW2,
	}}}
	vm := &fakeVM{}
	s.VM = vm
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)
	body := `{"name":"rbd-vm","kind":"vm","pool_id":"` + poolID + `","network_id":"` + netID + `"}`
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
	for _, op := range fd.calls {
		if op.Action == "create-volume" {
			created++
		}
	}
	if created != 1 {
		t.Fatalf("create-volume calls %d %+v", created, fd.calls)
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
	if vol == nil || vol.BackendType != storage.BackendDistributed {
		t.Fatalf("boot volume %+v", vol)
	}
	if err := storage.ValidateRBDPath(vol.BackendRef); err != nil {
		t.Fatalf("boot BackendRef must be an RBD device: %s %v", vol.BackendRef, err)
	}
	if strings.Contains(vol.BackendRef, "volumes/vm-disk/") {
		t.Fatalf("boot must not be a directory qcow2 on the RBD pool: %s", vol.BackendRef)
	}
	if len(vm.launch.Disks) != 1 || vm.launch.Disks[0].Path != vol.BackendRef {
		t.Fatalf("launch must attach the RBD device: %+v want %s", vm.launch.Disks, vol.BackendRef)
	}
}

func TestPhase39VMCreateFailsClosedWhenRBDUnavailable(t *testing.T) {
	s, mem, token := testServer(t)
	cluster, _ := mem.GetCluster(context.Background())
	nodeID := uuid.NewString()
	_ = mem.UpsertNode(context.Background(), appdb.Node{ID: nodeID, ClusterID: cluster.ID, Name: "local"})
	_, netID := seedCompute(t, mem, cluster.ID, nodeID)
	poolID := seedDistributedPool(t, mem, cluster.ID, nodeID)
	s.Distributed = &fakeDistributed{up: false}
	s.Storage = fakeStorage{vol: storage.CreateVolumeResult{Handle: storage.VolumeHandle{
		BackendType: storage.BackendDirectory, BackendRef: "volumes/vm-disk/boot.qcow2",
		Kind: storage.KindBlock, Class: storage.ClassVMDisk, Format: storage.FormatQCOW2,
	}}}
	s.VM = &fakeVM{}
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)
	body := `{"name":"rbd-down","kind":"vm","pool_id":"` + poolID + `","network_id":"` + netID + `"}`
	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/workloads", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode == http.StatusCreated || res.StatusCode == http.StatusOK {
		t.Fatalf("unavailable RBD must not create a VM %d %s", res.StatusCode, raw)
	}
	items, _ := mem.ListWorkloads(context.Background(), cluster.ID)
	if len(items) != 0 {
		t.Fatalf("GET must not list a VM whose RBD create-volume cannot map: %+v", items)
	}
}

func TestPhase39CTCreateFailsClosedForDistributedPool(t *testing.T) {
	s, mem, token := testServer(t)
	cluster, _ := mem.GetCluster(context.Background())
	nodeID := uuid.NewString()
	_ = mem.UpsertNode(context.Background(), appdb.Node{ID: nodeID, ClusterID: cluster.ID, Name: "local"})
	_, netID := seedCompute(t, mem, cluster.ID, nodeID)
	poolID := seedDistributedPool(t, mem, cluster.ID, nodeID)
	s.Distributed = &fakeDistributed{up: true}
	s.Workloads = &fakeWorkloads{}
	s.Storage = fakeStorage{vol: storage.CreateVolumeResult{Handle: storage.VolumeHandle{
		BackendType: storage.BackendDirectory, BackendRef: "volumes/container-root/x",
		Kind: storage.KindFilesystem, Class: storage.ClassContainerRoot, Format: storage.FormatDirectory,
	}}}
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)
	body := `{"name":"rbd-ct","kind":"system-container","image_pin":"alpine/3.21/amd64/default","pool_id":"` + poolID + `","network_id":"` + netID + `"}`
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
		t.Fatalf("distributed CT %d %s", res.StatusCode, raw)
	}
	if !strings.Contains(string(raw), "distributed RBD system containers are not supported") {
		t.Fatalf("distributed CT body %s", raw)
	}
	items, _ := mem.ListWorkloads(context.Background(), cluster.ID)
	if len(items) != 0 {
		t.Fatalf("GET must not list a system container whose root is a directory copy on an RBD pool: %+v", items)
	}
}

func TestPhase39VMCloneAndExportFailClosedForDistributed(t *testing.T) {
	s, mem, token := testServer(t)
	cluster, _ := mem.GetCluster(context.Background())
	nodeID := uuid.NewString()
	_ = mem.UpsertNode(context.Background(), appdb.Node{ID: nodeID, ClusterID: cluster.ID, Name: "local"})
	_, netID := seedCompute(t, mem, cluster.ID, nodeID)
	poolID := seedDistributedPool(t, mem, cluster.ID, nodeID)
	s.Distributed = &fakeDistributed{up: true}
	s.Storage = fakeStorage{vol: storage.CreateVolumeResult{Handle: storage.VolumeHandle{
		BackendType: storage.BackendDirectory, BackendRef: "volumes/vm-disk/boot.qcow2",
		Kind: storage.KindBlock, Class: storage.ClassVMDisk, Format: storage.FormatQCOW2,
	}}}
	s.VM = &fakeVM{}
	s.Backup = &fakeBackup{}
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)
	body := `{"name":"rbd-src","kind":"vm","pool_id":"` + poolID + `","network_id":"` + netID + `"}`
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

	clone, _ := http.NewRequest("POST", ts.URL+"/api/v1/workloads/"+id+"/clone", strings.NewReader(`{"name":"rbd-clone"}`))
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
	if !strings.Contains(string(craw), "distributed RBD pools do not store directory qcow2 copies") {
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
	if !strings.Contains(string(eraw), "distributed RBD pools do not store directory qcow2 copies") {
		t.Fatalf("export body %s", eraw)
	}

	items, _ := mem.ListWorkloads(context.Background(), cluster.ID)
	if len(items) != 1 || items[0].ID != id {
		t.Fatalf("GET must keep the source VM and not list a directory clone: %+v", items)
	}
	libs, _ := mem.ListLibraryItems(context.Background(), cluster.ID, poolID)
	if len(libs) != 0 {
		t.Fatalf("GET /images must not list an export under /dev/rbd: %+v", libs)
	}
}

func TestPhase39OSDStartFailsClosedWhenStatusPersistFails(t *testing.T) {
	s, mem, token := testServer(t)
	s.Update = &fakeUpdate{supported: true}
	s.Distributed = &fakeDistributed{up: true}
	cluster, _ := mem.GetCluster(context.Background())
	seedNode(t, mem, cluster.ID, debianInv(), false)
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)
	enableDistributed(t, ts, cookie)
	s.Store = failUpsertFeatureStore{Store: mem}

	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/storage/distributed/osds/start", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(confirmHeader, storage.StartOSDConfirm)
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ := ts.Client().Do(req)
	raw, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusInternalServerError {
		t.Fatalf("osd start persist %d %s", res.StatusCode, raw)
	}
	if !strings.Contains(string(raw), "could not record OSD status") {
		t.Fatalf("osd start persist body %s", raw)
	}
}
