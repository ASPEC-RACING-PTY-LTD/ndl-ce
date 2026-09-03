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
	"github.com/no-dal/ndl-ce/internal/inventory"
	"github.com/no-dal/ndl-ce/internal/lxc"
	"github.com/no-dal/ndl-ce/internal/storage"
)

type fakeLVM struct {
	calls  []storage.LVMOp
	status string
	reason string
	meta   *float64
}

func (f *fakeLVM) LVMPool(_ context.Context, op storage.LVMOp) (storage.LVMResult, error) {
	f.calls = append(f.calls, op)
	if strings.EqualFold(op.Action, "send") {
		return storage.LVMResult{}, fmtSend()
	}
	st := f.status
	if st == "" {
		st = storage.StatusAvailable
	}
	res := storage.LVMResult{
		Status: st, Reason: f.reason, PoolID: op.PoolID, Name: op.Name, VGUUID: op.VGUUID,
		Incremental: false, Capabilities: storage.LVMCapabilities(), ThinPool: storage.LVMThinPoolName,
		MetadataPercent: f.meta,
	}
	switch op.Action {
	case "create-pool":
		if st != storage.StatusUnavailable {
			res.VGUUID = "AbCdEfGh0123"
		}
		res.RootPath = storage.LVMMountRoot + "/" + op.Name
	case "create-volume":
		if op.Class == storage.ClassVMDisk {
			res.BackendRef = storage.LVMDevicePath(op.Name, op.VolumeID)
		} else {
			res.BackendRef = storage.LVMMountRoot + "/" + op.PoolID + "/volumes/" + op.Class + "/" + op.VolumeID
		}
		res.LVUUID = "LvUuid0001"
	case "snapshot", "rollback":
		res.BackendRef = storage.LVMDevicePath(op.Name, op.Snapshot)
	case "observe":
		if f.meta != nil {
			res.WarningText = []string{"Thin pool metadata percent: 12.3"}
		}
	}
	return res, nil
}

func fmtSend() error { return errSend() }

func errSend() error { return sendErr{msg: storage.LVMNoSend} }

type sendErr struct{ msg string }

func (e sendErr) Error() string { return e.msg }

func TestLVMCreateRefusesRootDiskAndHasNoIncrementalSend(t *testing.T) {
	s, mem, token := testServer(t)
	s.LVM = &fakeLVM{}
	cluster, _ := mem.GetCluster(context.Background())
	inv := debianInv()
	inv.BlockDevices = []inventory.BlockDevice{{Name: "sda", MountHint: "/"}}
	seedNode(t, mem, cluster.ID, inv, false)
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)

	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/storage/lvm/create", strings.NewReader(`{"name":"ndlvg","disks":["/dev/sda"]}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ := ts.Client().Do(req)
	if res.StatusCode != http.StatusBadRequest {
		b, _ := io.ReadAll(res.Body)
		t.Fatalf("root disk %d %s", res.StatusCode, b)
	}
	_ = res.Body.Close()

	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/storage/lvm/create", strings.NewReader(`{"name":"ndlvg","disks":["/dev/disk/by-id/wwn-0x5000"]}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(req)
	b, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("create %d %s", res.StatusCode, b)
	}
	if strings.Contains(string(b), `"incremental_send":true`) {
		t.Fatalf("lvm must not claim incremental send: %s", b)
	}
	if !strings.Contains(string(b), `"backend_type":"lvm"`) || !strings.Contains(string(b), `"incremental_send":false`) {
		t.Fatalf("caps %s", b)
	}
	var pool map[string]any
	if err := json.Unmarshal(b, &pool); err != nil {
		t.Fatal(err)
	}
	poolID := pool["id"].(string)
	vg, err := mem.GetLVMVG(context.Background(), poolID)
	if err != nil || vg == nil || vg.VGUUID != "AbCdEfGh0123" || vg.VGName != "ndlvg" {
		t.Fatalf("vg identity %+v %v", vg, err)
	}

	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/storage/volumes", strings.NewReader(`{"pool_id":"`+poolID+`","class":"vm-disk","size_bytes":1073741824}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(req)
	b, _ = io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusCreated || !strings.Contains(string(b), `/dev/ndlvg/`) || !strings.Contains(string(b), `"format":"thin-lv"`) {
		t.Fatalf("thin lv %d %s", res.StatusCode, b)
	}
}

func TestLVMDirectoryPoolCreateStillRefusesBackendLVM(t *testing.T) {
	s, mem, token := testServer(t)
	s.LVM = &fakeLVM{}
	cluster, _ := mem.GetCluster(context.Background())
	seedNode(t, mem, cluster.ID, debianInv(), false)
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)
	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/storage/pools", strings.NewReader(`{"name":"l","path":"/var/lib/ndl/storage/l","backend_type":"lvm"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ := ts.Client().Do(req)
	if res.StatusCode != http.StatusBadRequest {
		b, _ := io.ReadAll(res.Body)
		t.Fatalf("dir lvm %d %s", res.StatusCode, b)
	}
	_ = res.Body.Close()
}

func TestLVMMissingPVRowsRemainUnavailable(t *testing.T) {
	s, mem, token := testServer(t)
	fl := &fakeLVM{status: storage.StatusUnavailable, reason: "physical volume is missing. Desired rows remain."}
	s.LVM = fl
	cluster, _ := mem.GetCluster(context.Background())
	node := seedNode(t, mem, cluster.ID, debianInv(), false)
	caps, _ := json.Marshal(storage.LVMCapabilities())
	pool := appdb.StoragePool{
		ID: uuid.NewString(), ClusterID: cluster.ID, NodeID: node.ID, Name: "ndlvg",
		BackendType: storage.BackendLVM, Status: storage.StatusAvailable, RootPath: storage.LVMMountRoot + "/ndlvg",
		Capabilities: caps,
	}
	if err := mem.CreateStoragePool(context.Background(), pool); err != nil {
		t.Fatal(err)
	}
	if err := mem.UpsertLVMVG(context.Background(), appdb.LVMVG{PoolID: pool.ID, VGUUID: "AbCdEfGh0123", VGName: "ndlvg"}); err != nil {
		t.Fatal(err)
	}
	volID := uuid.NewString()
	_ = mem.CreateVolume(context.Background(), appdb.Volume{
		ID: volID, ClusterID: cluster.ID, NodeID: node.ID, PoolID: pool.ID,
		Class: storage.ClassVMDisk, Kind: storage.KindBlock, Format: storage.FormatThinLV,
		Status: storage.StatusAvailable, BackendType: storage.BackendLVM, BackendRef: storage.LVMDevicePath("ndlvg", volID),
	})
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)
	req, _ := http.NewRequest("GET", ts.URL+"/api/v1/storage/pools/"+pool.ID, nil)
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ := ts.Client().Do(req)
	b, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK || !strings.Contains(string(b), `"status":"unavailable"`) {
		t.Fatalf("observe %d %s", res.StatusCode, b)
	}
	if strings.Contains(string(b), `"usable_bytes":0`) {
		t.Fatalf("must not invent zero capacity: %s", b)
	}
	got, _ := mem.GetVolume(context.Background(), cluster.ID, volID)
	if got == nil || got.Status != storage.StatusUnavailable {
		t.Fatalf("volume row %v", got)
	}
}

func TestLVMThinSnapWorksAndMetadataVisible(t *testing.T) {
	s, mem, token := testServer(t)
	meta := 12.3
	s.LVM = &fakeLVM{meta: &meta}
	cluster, _ := mem.GetCluster(context.Background())
	node := seedNode(t, mem, cluster.ID, debianInv(), false)
	caps, _ := json.Marshal(storage.LVMCapabilities())
	backing, _ := json.Marshal(storage.BackingIdentity{FSUUID: "AbCdEfGh0123", FSType: storage.BackendLVM, Device: "ndlvg", MetadataPercent: &meta, ThinPool: storage.LVMThinPoolName})
	pool := appdb.StoragePool{
		ID: uuid.NewString(), ClusterID: cluster.ID, NodeID: node.ID, Name: "ndlvg",
		BackendType: storage.BackendLVM, Status: storage.StatusAvailable, RootPath: storage.LVMMountRoot + "/ndlvg",
		Capabilities: caps, Backing: backing,
	}
	_ = mem.CreateStoragePool(context.Background(), pool)
	_ = mem.UpsertLVMVG(context.Background(), appdb.LVMVG{PoolID: pool.ID, VGUUID: "AbCdEfGh0123", VGName: "ndlvg"})
	volID := uuid.NewString()
	vol := appdb.Volume{
		ID: volID, ClusterID: cluster.ID, NodeID: node.ID, PoolID: pool.ID,
		Class: storage.ClassVMDisk, Kind: storage.KindBlock, Format: storage.FormatThinLV,
		Status: storage.StatusAvailable, BackendType: storage.BackendLVM,
		BackendRef: storage.LVMDevicePath("ndlvg", volID),
	}
	_ = mem.CreateVolume(context.Background(), vol)
	wl := appdb.Workload{ID: uuid.NewString(), ClusterID: cluster.ID, Name: "vm", Kind: "vm", Status: "stopped"}
	_ = mem.CreateWorkload(context.Background(), wl)
	_ = mem.CreateWorkloadDisk(context.Background(), appdb.WorkloadDisk{ID: uuid.NewString(), ClusterID: cluster.ID, WorkloadID: wl.ID, VolumeID: vol.ID, Role: "boot"})
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)

	req, _ := http.NewRequest("GET", ts.URL+"/api/v1/storage/pools/"+pool.ID, nil)
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ := ts.Client().Do(req)
	b, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK || !strings.Contains(string(b), `"metadata_percent"`) {
		t.Fatalf("metadata %d %s", res.StatusCode, b)
	}

	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/workloads/"+wl.ID+"/snapshots", strings.NewReader(`{"name":"s1"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(req)
	b, _ = io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusCreated || !strings.Contains(string(b), `"mechanism":"lvm"`) {
		t.Fatalf("snap %d %s", res.StatusCode, b)
	}

	req, _ = http.NewRequest("GET", ts.URL+"/api/v1/workloads/"+wl.ID+"/snapshots", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(req)
	b, _ = io.ReadAll(res.Body)
	_ = res.Body.Close()
	if !strings.Contains(string(b), `"mechanism":"lvm"`) || strings.Contains(string(b), `"incremental_send":true`) {
		t.Fatalf("capability %s", b)
	}
}

func TestLVMCTSnapshotSupported(t *testing.T) {
	s, mem, token := testServer(t)
	s.LVM = &fakeLVM{}
	cluster, _ := mem.GetCluster(context.Background())
	node := seedNode(t, mem, cluster.ID, debianInv(), false)
	caps, _ := json.Marshal(storage.LVMCapabilities())
	pool := appdb.StoragePool{
		ID: uuid.NewString(), ClusterID: cluster.ID, NodeID: node.ID, Name: "ndlvg",
		BackendType: storage.BackendLVM, Status: storage.StatusAvailable, RootPath: storage.LVMMountRoot + "/ndlvg",
		Capabilities: caps,
	}
	_ = mem.CreateStoragePool(context.Background(), pool)
	_ = mem.UpsertLVMVG(context.Background(), appdb.LVMVG{PoolID: pool.ID, VGUUID: "AbCdEfGh0123", VGName: "ndlvg"})
	volID := uuid.NewString()
	vol := appdb.Volume{
		ID: volID, ClusterID: cluster.ID, NodeID: node.ID, PoolID: pool.ID,
		Class: storage.ClassContainerRoot, Kind: storage.KindFilesystem, Format: storage.FormatDirectory,
		Status: storage.StatusAvailable, BackendType: storage.BackendLVM,
		BackendRef: storage.LVMMountRoot + "/" + pool.ID + "/volumes/container-root/" + volID,
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

func TestLVMRuntimeReportsNoIncrementalSend(t *testing.T) {
	s, mem, token := testServer(t)
	s.LVM = &fakeLVM{}
	cluster, _ := mem.GetCluster(context.Background())
	seedNode(t, mem, cluster.ID, debianInv(), false)
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)
	req, _ := http.NewRequest("GET", ts.URL+"/api/v1/storage/lvm", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ := ts.Client().Do(req)
	b, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK || !strings.Contains(string(b), `"incremental_send":false`) {
		t.Fatalf("runtime %d %s", res.StatusCode, b)
	}
	if strings.Contains(string(b), "vgexport") && strings.Contains(string(b), "argv") && strings.Contains(string(b), "vgexport") {
		// argv is apt-get, not vgexport; vgexport key is the refuse policy.
	}
	if strings.Contains(string(b), `"vgexport":"refused"`) == false {
		t.Fatalf("export policy %s", b)
	}
}

func TestLVMCreateDoesNotInventPendingUUID(t *testing.T) {
	s, mem, token := testServer(t)
	s.LVM = &fakeLVM{status: storage.StatusUnavailable, reason: storage.LVMMissing}
	cluster, _ := mem.GetCluster(context.Background())
	seedNode(t, mem, cluster.ID, debianInv(), false)
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)
	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/storage/lvm/create", strings.NewReader(`{"name":"ndlvg","disks":["/dev/disk/by-id/wwn-0x5000"]}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ := ts.Client().Do(req)
	b, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("create %d %s", res.StatusCode, b)
	}
	if strings.Contains(string(b), "pending-") {
		t.Fatalf("must not invent pending vg_uuid: %s", b)
	}
	if strings.Contains(string(b), `"status":"available"`) {
		t.Fatalf("unavailable agent must not be marked available: %s", b)
	}
	var pool map[string]any
	if err := json.Unmarshal(b, &pool); err != nil {
		t.Fatal(err)
	}
	if pool["status"] != storage.StatusUnavailable {
		t.Fatalf("status %s", b)
	}
}

type failUpsertLVMVGStore struct {
	appdb.Store
}

func (f failUpsertLVMVGStore) UpsertLVMVG(context.Context, appdb.LVMVG) error {
	return errors.New("persist failed")
}

func TestLVMCreateFailsClosedWhenIdentityPersistFails(t *testing.T) {
	s, mem, token := testServer(t)
	s.LVM = &fakeLVM{}
	s.Store = failUpsertLVMVGStore{Store: mem}
	cluster, _ := mem.GetCluster(context.Background())
	seedNode(t, mem, cluster.ID, debianInv(), false)
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)
	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/storage/lvm/create", strings.NewReader(`{"name":"ndlvg","disks":["/dev/disk/by-id/wwn-0x5000"]}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ := ts.Client().Do(req)
	b, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusInternalServerError {
		t.Fatalf("lvm persist %d %s", res.StatusCode, b)
	}
	if !strings.Contains(string(b), "could not record volume group identity") {
		t.Fatalf("lvm persist body %s", b)
	}
}

func TestLVMVolumeCreateFailsClosedForUnsupportedClass(t *testing.T) {
	s, mem, token := testServer(t)
	s.LVM = &fakeLVM{}
	cluster, _ := mem.GetCluster(context.Background())
	seedNode(t, mem, cluster.ID, debianInv(), false)
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)
	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/storage/lvm/create", strings.NewReader(`{"name":"ndlvg","disks":["/dev/disk/by-id/wwn-0x5000"]}`))
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
	body := `{"pool_id":"` + poolID + `","class":"../etc","size_bytes":1073741824}`
	volReq, _ := http.NewRequest("POST", ts.URL+"/api/v1/storage/volumes", strings.NewReader(body))
	volReq.Header.Set("Content-Type", "application/json")
	volReq.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	volRes, _ := ts.Client().Do(volReq)
	raw, _ := io.ReadAll(volRes.Body)
	_ = volRes.Body.Close()
	if volRes.StatusCode != http.StatusBadRequest {
		t.Fatalf("unsupported class %d %s", volRes.StatusCode, raw)
	}
	if !strings.Contains(string(raw), "storage class is unsupported") {
		t.Fatalf("unsupported class body %s", raw)
	}
	items, _ := mem.ListVolumes(context.Background(), cluster.ID, poolID)
	if len(items) != 0 {
		t.Fatalf("GET must not list a volume whose class apply cannot create: %+v", items)
	}
}
