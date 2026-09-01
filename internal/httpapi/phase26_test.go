package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/no-dal/ndl-ce/internal/appdb"
	"github.com/no-dal/ndl-ce/internal/storage"
)

type fakeDatastore struct {
	calls  []storage.DatastoreOp
	status string
	reason string
}

func (f *fakeDatastore) Datastore(_ context.Context, op storage.DatastoreOp) (storage.DatastoreResult, error) {
	f.calls = append(f.calls, op)
	for _, a := range op.Password {
		_ = a
	}
	st := f.status
	if st == "" {
		st = storage.StatusAvailable
	}
	res := storage.DatastoreResult{
		Status: st, Reason: f.reason, PoolID: op.PoolID, Kind: op.Kind,
		Incremental: false, Capabilities: storage.NFSCapabilities(),
	}
	if op.Kind == storage.BackendISCSI {
		res.Capabilities = storage.ISCSICapabilities()
	}
	switch op.Action {
	case "mount", "add", "create":
		if op.Kind == storage.BackendISCSI {
			p, _ := storage.ISCSIDevicePath(op.Portal, firstNonEmpty(op.IQN, op.Locator))
			res.RootPath, res.BackendRef = p, p
		} else {
			res.RootPath, _ = storage.DatastoreMountPath(op.Kind, op.PoolID)
		}
		res.Warnings = []string{storage.WarnSharedFilesystem}
		res.WarningText = []string{storage.NFSShareMsg}
	}
	return res, nil
}

func TestNFSCreateAndISOVolumeOnShare(t *testing.T) {
	s, mem, token := testServer(t)
	s.Datastore = &fakeDatastore{}
	cluster, _ := mem.GetCluster(context.Background())
	seedNode(t, mem, cluster.ID, debianInv(), false)
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)

	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/storage/nfs", strings.NewReader(`{"name":"iso","locator":"nas.example:/export/iso"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ := ts.Client().Do(req)
	b, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusCreated || strings.Contains(string(b), `"incremental_send":true`) {
		t.Fatalf("nfs %d %s", res.StatusCode, b)
	}
	if !strings.Contains(string(b), `"backend_type":"nfs"`) {
		t.Fatalf("backend %s", b)
	}
}

func TestSMBPasswordNotReturnedAndNotOnArgv(t *testing.T) {
	s, mem, token := testServer(t)
	fd := &fakeDatastore{}
	s.Datastore = fd
	cluster, _ := mem.GetCluster(context.Background())
	seedNode(t, mem, cluster.ID, debianInv(), false)
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)
	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/storage/smb", strings.NewReader(`{"name":"share","locator":"//files.example/iso","username":"u","password":"s3cret"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ := ts.Client().Do(req)
	b, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusCreated || strings.Contains(string(b), "s3cret") {
		t.Fatalf("smb %d %s", res.StatusCode, b)
	}
	if len(fd.calls) == 0 || fd.calls[0].Password != "s3cret" {
		t.Fatal("password must reach agent, not JSON")
	}
}

func TestShareDownRowsRemainUnavailable(t *testing.T) {
	s, mem, token := testServer(t)
	s.Datastore = &fakeDatastore{status: storage.StatusUnavailable, reason: storage.NFSMissing}
	cluster, _ := mem.GetCluster(context.Background())
	node := seedNode(t, mem, cluster.ID, debianInv(), false)
	caps, _ := json.Marshal(storage.NFSCapabilities())
	pool := appdb.StoragePool{
		ID: uuid.NewString(), ClusterID: cluster.ID, NodeID: node.ID, Name: "iso",
		BackendType: storage.BackendNFS, Status: storage.StatusAvailable, RootPath: storage.NFSMountRoot + "/p",
		Capabilities: caps,
	}
	_ = mem.CreateStoragePool(context.Background(), pool)
	_ = mem.UpsertDatastore(context.Background(), appdb.Datastore{PoolID: pool.ID, Kind: storage.BackendNFS, Locator: "nas.example:/export"})
	volID := uuid.NewString()
	_ = mem.CreateVolume(context.Background(), appdb.Volume{
		ID: volID, ClusterID: cluster.ID, NodeID: node.ID, PoolID: pool.ID,
		Class: storage.ClassISO, Kind: storage.KindFilesystem, Format: storage.FormatFile,
		Status: storage.StatusAvailable, BackendType: storage.BackendNFS, BackendRef: "library/iso/a.iso",
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
		t.Fatalf("down %d %s", res.StatusCode, b)
	}
	if strings.Contains(string(b), `"usable_bytes":0`) {
		t.Fatal("zero capacity")
	}
	got, _ := mem.GetVolume(context.Background(), cluster.ID, volID)
	if got == nil || got.Status != storage.StatusUnavailable {
		t.Fatalf("volume %v", got)
	}
}

func TestISCSICreatesRawLUNAndRefusesSnapshot(t *testing.T) {
	s, mem, token := testServer(t)
	s.Datastore = &fakeDatastore{}
	cluster, _ := mem.GetCluster(context.Background())
	node := seedNode(t, mem, cluster.ID, debianInv(), false)
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)
	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/storage/iscsi", strings.NewReader(`{"name":"lun","iqn":"iqn.2020-01.com.example:target1","portal":"10.0.0.8:3260"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ := ts.Client().Do(req)
	b, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusCreated || strings.Contains(string(b), `"incremental_send":true`) {
		t.Fatalf("iscsi %d %s", res.StatusCode, b)
	}
	var pool map[string]any
	if err := json.Unmarshal(b, &pool); err != nil {
		t.Fatal(err)
	}
	poolID := pool["id"].(string)
	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/storage/volumes", strings.NewReader(`{"pool_id":"`+poolID+`","class":"vm-disk","size_bytes":1073741824}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(req)
	b, _ = io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusCreated || !strings.Contains(string(b), `/dev/disk/by-path/`) {
		t.Fatalf("lun %d %s", res.StatusCode, b)
	}
	wl := appdb.Workload{ID: uuid.NewString(), ClusterID: cluster.ID, Name: "vm", Kind: "vm", Status: "stopped"}
	_ = mem.CreateWorkload(context.Background(), wl)
	vols, _ := mem.ListVolumes(context.Background(), cluster.ID, poolID)
	if len(vols) == 0 {
		t.Fatal("volume")
	}
	_ = mem.CreateWorkloadDisk(context.Background(), appdb.WorkloadDisk{ID: uuid.NewString(), ClusterID: cluster.ID, WorkloadID: wl.ID, VolumeID: vols[0].ID, Role: "boot"})
	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/workloads/"+wl.ID+"/snapshots", strings.NewReader(`{"name":"s1"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(req)
	if res.StatusCode != http.StatusUnprocessableEntity {
		b, _ = io.ReadAll(res.Body)
		t.Fatalf("snap %d %s", res.StatusCode, b)
	}
	_ = res.Body.Close()
}

func TestDirectoryPoolCreateRefusesNFSBackend(t *testing.T) {
	s, mem, token := testServer(t)
	s.Datastore = &fakeDatastore{}
	cluster, _ := mem.GetCluster(context.Background())
	seedNode(t, mem, cluster.ID, debianInv(), false)
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)
	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/storage/pools", strings.NewReader(`{"name":"n","path":"/var/lib/ndl/storage/n","backend_type":"nfs"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ := ts.Client().Do(req)
	if res.StatusCode != http.StatusBadRequest {
		b, _ := io.ReadAll(res.Body)
		t.Fatalf("dir nfs %d %s", res.StatusCode, b)
	}
	_ = res.Body.Close()
}

func TestPhase26NFSISOLibraryAndVMDisk(t *testing.T) {
	s, mem, token := testServer(t)
	s.Datastore = &fakeDatastore{}
	cluster, _ := mem.GetCluster(context.Background())
	seedNode(t, mem, cluster.ID, debianInv(), false)
	itemID := uuid.NewString()
	s.Storage = fakeStorage{
		img: storage.UploadResult{
			ItemID: itemID, Kind: storage.LibraryISO, DisplayName: "debian.iso",
			BackendRef: "library/iso/" + itemID + ".iso", SizeBytes: 12, SHA256: "nfsiso",
		},
		vol: storage.CreateVolumeResult{Handle: storage.VolumeHandle{
			BackendType: storage.BackendDirectory, BackendRef: "volumes/vm-disk/nfs.qcow2",
			Kind: storage.KindBlock, Class: storage.ClassVMDisk, Format: storage.FormatQCOW2,
		}},
	}
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)

	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/storage/nfs", strings.NewReader(`{"name":"iso","locator":"nas.example:/export/iso"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("nfs %d %s", res.StatusCode, b)
	}
	var pool map[string]any
	if err := json.Unmarshal(b, &pool); err != nil {
		t.Fatal(err)
	}
	poolID := pool["id"].(string)

	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/storage/images?pool_id="+poolID+"&kind=iso&filename=debian.iso", strings.NewReader("ISO-CONTENT"))
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, err = ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	b, _ = io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusCreated || !strings.Contains(string(b), "debian.iso") {
		t.Fatalf("iso library %d %s", res.StatusCode, b)
	}

	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/storage/volumes", strings.NewReader(`{"pool_id":"`+poolID+`","class":"vm-disk","size_bytes":1048576}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, err = ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	b, _ = io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("nfs vm-disk %d %s", res.StatusCode, b)
	}
	if !strings.Contains(string(b), `"backend_type":"nfs"`) {
		t.Fatalf("expected nfs backend, got %s", b)
	}
	if !strings.Contains(string(b), "volumes/vm-disk/") {
		t.Fatalf("expected NFS file disk locator, got %s", b)
	}
}
