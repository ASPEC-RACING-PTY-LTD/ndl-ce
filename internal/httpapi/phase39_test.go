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
