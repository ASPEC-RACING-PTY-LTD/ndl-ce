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
	var pool map[string]any
	if err := json.Unmarshal(b, &pool); err != nil {
		t.Fatal(err)
	}
	poolID, _ := pool["id"].(string)
	ds, err := mem.GetDatastore(context.Background(), poolID)
	if err != nil || ds == nil || ds.Locator != "nas.example:/export/iso" {
		t.Fatalf("datastore locator %+v %v", ds, err)
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
	var pool map[string]any
	if err := json.Unmarshal(b, &pool); err != nil {
		t.Fatal(err)
	}
	poolID, _ := pool["id"].(string)
	ds, err := mem.GetDatastore(context.Background(), poolID)
	if err != nil || ds == nil || ds.Locator != "//files.example/iso" {
		t.Fatalf("datastore locator %+v %v", ds, err)
	}
	user, pass, err := mem.DatastoreSecret(context.Background(), poolID)
	if err != nil || user != "u" || pass != "s3cret" {
		t.Fatalf("stored secret %q %q %v", user, pass, err)
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
	seedNode(t, mem, cluster.ID, debianInv(), false)
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
	if strings.Contains(string(b), "nas.example") || strings.Contains(strings.ToLower(string(b)), "landed") {
		t.Fatalf("must not claim a file landed on the share: %s", b)
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
	if strings.Contains(string(b), "nas.example") || strings.Contains(strings.ToLower(string(b)), "landed") {
		t.Fatalf("must not claim a file landed on the share: %s", b)
	}
}

func TestISCSIChapIsUnsupported(t *testing.T) {
	s, mem, token := testServer(t)
	s.Datastore = &fakeDatastore{}
	cluster, _ := mem.GetCluster(context.Background())
	seedNode(t, mem, cluster.ID, debianInv(), false)
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)
	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/storage/iscsi", strings.NewReader(`{"name":"lun","iqn":"iqn.2020-01.com.example:target1","portal":"10.0.0.8:3260","username":"chap","password":"s3cret"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ := ts.Client().Do(req)
	b, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("chap %d %s", res.StatusCode, b)
	}
	if !strings.Contains(strings.ToLower(string(b)), "chap") {
		t.Fatalf("chap reason %s", b)
	}
}

func TestISCSIVolumeNotAvailableWithoutDevice(t *testing.T) {
	s, mem, token := testServer(t)
	s.Datastore = &fakeDatastore{status: storage.StatusUnavailable, reason: storage.ISCSIMissing}
	cluster, _ := mem.GetCluster(context.Background())
	seedNode(t, mem, cluster.ID, debianInv(), false)
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)
	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/storage/iscsi", strings.NewReader(`{"name":"lun","iqn":"iqn.2020-01.com.example:target1","portal":"10.0.0.8:3260"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ := ts.Client().Do(req)
	b, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("pool %d %s", res.StatusCode, b)
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
	if res.StatusCode == http.StatusCreated && strings.Contains(string(b), `"status":"available"`) {
		t.Fatalf("volume must not be available without a device: %s", b)
	}
	if res.StatusCode == http.StatusCreated {
		t.Fatalf("volume create without login %d %s", res.StatusCode, b)
	}
}

type failUpsertDatastoreStore struct {
	appdb.Store
}

func (f failUpsertDatastoreStore) UpsertDatastore(context.Context, appdb.Datastore) error {
	return errors.New("persist failed")
}

type failUpsertDatastoreSecretStore struct {
	appdb.Store
}

func (f failUpsertDatastoreSecretStore) UpsertDatastoreSecret(context.Context, string, string, string) error {
	return errors.New("persist failed")
}

func TestNFSCreateFailsClosedWhenDatastorePersistFails(t *testing.T) {
	s, mem, token := testServer(t)
	s.Datastore = &fakeDatastore{}
	s.Store = failUpsertDatastoreStore{Store: mem}
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
	if res.StatusCode != http.StatusInternalServerError {
		t.Fatalf("nfs persist %d %s", res.StatusCode, b)
	}
	if !strings.Contains(string(b), "could not record datastore") {
		t.Fatalf("nfs persist body %s", b)
	}
}

func TestSMBCreateFailsClosedWhenSecretPersistFails(t *testing.T) {
	s, mem, token := testServer(t)
	s.Datastore = &fakeDatastore{}
	s.Store = failUpsertDatastoreSecretStore{Store: mem}
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
	if res.StatusCode != http.StatusInternalServerError {
		t.Fatalf("smb persist %d %s", res.StatusCode, b)
	}
	if strings.Contains(string(b), "s3cret") {
		t.Fatalf("secret echoed %s", b)
	}
	if !strings.Contains(string(b), "could not record datastore credentials") {
		t.Fatalf("smb persist body %s", b)
	}
}

func TestPhase26VMCreateUsesISCSIDevice(t *testing.T) {
	s, mem, token := testServer(t)
	s.Datastore = &fakeDatastore{}
	cluster, _ := mem.GetCluster(context.Background())
	node := seedNode(t, mem, cluster.ID, debianInv(), false)
	_, netID := seedCompute(t, mem, cluster.ID, node.ID)
	s.Storage = fakeStorage{vol: storage.CreateVolumeResult{Handle: storage.VolumeHandle{
		BackendType: storage.BackendDirectory, BackendRef: "volumes/vm-disk/boot.qcow2",
		Kind: storage.KindBlock, Class: storage.ClassVMDisk, Format: storage.FormatQCOW2,
	}}}
	vm := &fakeVM{}
	s.VM = vm
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)
	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/storage/iscsi", strings.NewReader(`{"name":"lun-vm","iqn":"iqn.2020-01.com.example:target1","portal":"10.0.0.8:3260"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ := ts.Client().Do(req)
	b, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("iscsi %d %s", res.StatusCode, b)
	}
	var pool map[string]any
	if err := json.Unmarshal(b, &pool); err != nil {
		t.Fatal(err)
	}
	poolID, _ := pool["id"].(string)
	body := `{"name":"iscsi-vm","kind":"vm","pool_id":"` + poolID + `","network_id":"` + netID + `"}`
	wreq, _ := http.NewRequest("POST", ts.URL+"/api/v1/workloads", strings.NewReader(body))
	wreq.Header.Set("Content-Type", "application/json")
	wreq.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	wres, err := ts.Client().Do(wreq)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(wres.Body)
	_ = wres.Body.Close()
	if wres.StatusCode != http.StatusCreated {
		t.Fatalf("create %d %s", wres.StatusCode, raw)
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
	if vol == nil || vol.BackendType != storage.BackendISCSI {
		t.Fatalf("boot volume %+v", vol)
	}
	if !strings.HasPrefix(vol.BackendRef, storage.ISCSIByPath) {
		t.Fatalf("boot BackendRef must be an iSCSI by-path device: %s", vol.BackendRef)
	}
	if strings.Contains(vol.BackendRef, "volumes/vm-disk/") {
		t.Fatalf("boot must not be a directory qcow2 on the iSCSI pool: %s", vol.BackendRef)
	}
	if len(vm.launch.Disks) != 1 || vm.launch.Disks[0].Path != vol.BackendRef || vm.launch.Disks[0].Format != "raw" {
		t.Fatalf("launch must attach the iSCSI LUN: %+v want %s", vm.launch.Disks, vol.BackendRef)
	}
}

func TestPhase26VMCreateFailsClosedWhenISCSIUnavailable(t *testing.T) {
	s, mem, token := testServer(t)
	s.Datastore = &fakeDatastore{status: storage.StatusUnavailable, reason: storage.ISCSIMissing}
	cluster, _ := mem.GetCluster(context.Background())
	node := seedNode(t, mem, cluster.ID, debianInv(), false)
	_, netID := seedCompute(t, mem, cluster.ID, node.ID)
	portal, iqn := "10.0.0.8:3260", "iqn.2020-01.com.example:target1"
	dev, err := storage.ISCSIDevicePath(portal, iqn)
	if err != nil {
		t.Fatal(err)
	}
	poolID := uuid.NewString()
	if err := mem.CreateStoragePool(context.Background(), appdb.StoragePool{
		ID: poolID, ClusterID: cluster.ID, NodeID: node.ID, Name: "lun-down",
		BackendType: storage.BackendISCSI, Status: storage.StatusAvailable, RootPath: dev,
	}); err != nil {
		t.Fatal(err)
	}
	if err := mem.UpsertDatastore(context.Background(), appdb.Datastore{
		PoolID: poolID, Kind: storage.BackendISCSI, Locator: iqn, Portal: portal, IQN: iqn,
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
	body := `{"name":"iscsi-down","kind":"vm","pool_id":"` + poolID + `","network_id":"` + netID + `"}`
	wreq, _ := http.NewRequest("POST", ts.URL+"/api/v1/workloads", strings.NewReader(body))
	wreq.Header.Set("Content-Type", "application/json")
	wreq.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	wres, err := ts.Client().Do(wreq)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(wres.Body)
	_ = wres.Body.Close()
	if wres.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("unavailable iSCSI VM %d %s", wres.StatusCode, raw)
	}
	if !strings.Contains(string(raw), storage.ISCSIMissing) {
		t.Fatalf("unavailable iSCSI body %s", raw)
	}
	items, _ := mem.ListWorkloads(context.Background(), cluster.ID)
	if len(items) != 0 {
		t.Fatalf("GET must not list a VM whose iSCSI LUN apply cannot attach: %+v", items)
	}
}

func TestPhase26VMCloneAndExportFailClosedForISCSI(t *testing.T) {
	s, mem, token := testServer(t)
	s.Datastore = &fakeDatastore{}
	cluster, _ := mem.GetCluster(context.Background())
	node := seedNode(t, mem, cluster.ID, debianInv(), false)
	_, netID := seedCompute(t, mem, cluster.ID, node.ID)
	s.Storage = fakeStorage{vol: storage.CreateVolumeResult{Handle: storage.VolumeHandle{
		BackendType: storage.BackendDirectory, BackendRef: "volumes/vm-disk/boot.qcow2",
		Kind: storage.KindBlock, Class: storage.ClassVMDisk, Format: storage.FormatQCOW2,
	}}}
	s.VM = &fakeVM{}
	s.Backup = &fakeBackup{}
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)
	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/storage/iscsi", strings.NewReader(`{"name":"lun-clone","iqn":"iqn.2020-01.com.example:target1","portal":"10.0.0.8:3260"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ := ts.Client().Do(req)
	b, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("iscsi %d %s", res.StatusCode, b)
	}
	var pool map[string]any
	if err := json.Unmarshal(b, &pool); err != nil {
		t.Fatal(err)
	}
	poolID, _ := pool["id"].(string)
	body := `{"name":"iscsi-src","kind":"vm","pool_id":"` + poolID + `","network_id":"` + netID + `"}`
	wreq, _ := http.NewRequest("POST", ts.URL+"/api/v1/workloads", strings.NewReader(body))
	wreq.Header.Set("Content-Type", "application/json")
	wreq.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	wres, err := ts.Client().Do(wreq)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(wres.Body)
	_ = wres.Body.Close()
	if wres.StatusCode != http.StatusCreated {
		t.Fatalf("create %d %s", wres.StatusCode, raw)
	}
	var created map[string]any
	if err := json.Unmarshal(raw, &created); err != nil {
		t.Fatal(err)
	}
	id, _ := created["id"].(string)

	clone, _ := http.NewRequest("POST", ts.URL+"/api/v1/workloads/"+id+"/clone", strings.NewReader(`{"name":"iscsi-clone"}`))
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
	if !strings.Contains(string(craw), "iSCSI pools do not store directory qcow2 copies") {
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
	if !strings.Contains(string(eraw), "iSCSI pools do not store directory qcow2 copies") {
		t.Fatalf("export body %s", eraw)
	}

	items, _ := mem.ListWorkloads(context.Background(), cluster.ID)
	if len(items) != 1 || items[0].ID != id {
		t.Fatalf("GET must keep the source VM and not list a directory clone: %+v", items)
	}
	libs, _ := mem.ListLibraryItems(context.Background(), cluster.ID, poolID)
	if len(libs) != 0 {
		t.Fatalf("GET /images must not list an export under an iSCSI by-path: %+v", libs)
	}
}

func TestPhase26VMRestoreFailsClosedForISCSI(t *testing.T) {
	s, mem, token := testServer(t)
	s.Datastore = &fakeDatastore{}
	cluster, _ := mem.GetCluster(context.Background())
	node := seedNode(t, mem, cluster.ID, debianInv(), false)
	_, netID := seedCompute(t, mem, cluster.ID, node.ID)
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
	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/storage/iscsi", strings.NewReader(`{"name":"lun-restore","iqn":"iqn.2020-01.com.example:target1","portal":"10.0.0.8:3260"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ := ts.Client().Do(req)
	b, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("iscsi %d %s", res.StatusCode, b)
	}
	var pool map[string]any
	if err := json.Unmarshal(b, &pool); err != nil {
		t.Fatal(err)
	}
	poolID, _ := pool["id"].(string)
	body := `{"name":"iscsi-restore","kind":"vm","pool_id":"` + poolID + `","network_id":"` + netID + `"}`
	wreq, _ := http.NewRequest("POST", ts.URL+"/api/v1/workloads", strings.NewReader(body))
	wreq.Header.Set("Content-Type", "application/json")
	wreq.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	wres, err := ts.Client().Do(wreq)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(wres.Body)
	_ = wres.Body.Close()
	if wres.StatusCode != http.StatusCreated {
		t.Fatalf("create %d %s", wres.StatusCode, raw)
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
	if !strings.Contains(string(nraw), "iSCSI pools do not store directory qcow2 copies") {
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
	if !strings.Contains(string(rraw), "iSCSI pools do not store directory qcow2 copies") {
		t.Fatalf("restore replace body %s", rraw)
	}
	for _, c := range fb.copies {
		if c[0] == "replace" {
			t.Fatalf("replace must not qemu-img onto the iSCSI LUN: %+v", fb.copies)
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
	if vol == nil || vol.BackendRef != bootRef || !strings.HasPrefix(vol.BackendRef, storage.ISCSIByPath) {
		t.Fatalf("boot locator must stay an iSCSI by-path: %+v want %s", vol, bootRef)
	}
}

func TestPhase26VMMigrationExportFailsClosedForISCSI(t *testing.T) {
	s, mem, token := testServer(t)
	s.Datastore = &fakeDatastore{}
	cluster, _ := mem.GetCluster(context.Background())
	node := seedNode(t, mem, cluster.ID, debianInv(), false)
	_, netID := seedCompute(t, mem, cluster.ID, node.ID)
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
	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/storage/iscsi", strings.NewReader(`{"name":"lun-export","iqn":"iqn.2020-01.com.example:target1","portal":"10.0.0.8:3260"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ := ts.Client().Do(req)
	b, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("iscsi %d %s", res.StatusCode, b)
	}
	var pool map[string]any
	if err := json.Unmarshal(b, &pool); err != nil {
		t.Fatal(err)
	}
	poolID, _ := pool["id"].(string)
	body := `{"name":"iscsi-export","kind":"vm","pool_id":"` + poolID + `","network_id":"` + netID + `"}`
	wreq, _ := http.NewRequest("POST", ts.URL+"/api/v1/workloads", strings.NewReader(body))
	wreq.Header.Set("Content-Type", "application/json")
	wreq.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	wres, err := ts.Client().Do(wreq)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(wres.Body)
	_ = wres.Body.Close()
	if wres.StatusCode != http.StatusCreated {
		t.Fatalf("create %d %s", wres.StatusCode, raw)
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
	if !strings.Contains(string(eraw), "iSCSI pools do not store directory qcow2 copies") {
		t.Fatalf("migration export body %s", eraw)
	}
	if len(fb.converts) != 0 {
		t.Fatalf("ConvertImport must not qemu-img an iSCSI LUN as qcow2: %+v", fb.converts)
	}
	jobs, _ := mem.ListMigrationJobs(context.Background(), cluster.ID, 100)
	if len(jobs) != 0 {
		t.Fatalf("GET must not list a succeeded iSCSI export: %+v", jobs)
	}
}

func TestPhase26VMTemplateBackupFailClosedForISCSI(t *testing.T) {
	s, mem, token := testServer(t)
	s.Datastore = &fakeDatastore{}
	cluster, _ := mem.GetCluster(context.Background())
	node := seedNode(t, mem, cluster.ID, debianInv(), false)
	_, netID := seedCompute(t, mem, cluster.ID, node.ID)
	s.Storage = fakeStorage{vol: storage.CreateVolumeResult{Handle: storage.VolumeHandle{
		BackendType: storage.BackendDirectory, BackendRef: "volumes/vm-disk/boot.qcow2",
		Kind: storage.KindBlock, Class: storage.ClassVMDisk, Format: storage.FormatQCOW2,
	}}}
	s.VM = &fakeVM{}
	s.Backup = &fakeBackup{}
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)
	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/storage/iscsi", strings.NewReader(`{"name":"lun-snap","iqn":"iqn.2020-01.com.example:target1","portal":"10.0.0.8:3260"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ := ts.Client().Do(req)
	b, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("iscsi %d %s", res.StatusCode, b)
	}
	var pool map[string]any
	if err := json.Unmarshal(b, &pool); err != nil {
		t.Fatal(err)
	}
	poolID, _ := pool["id"].(string)
	body := `{"name":"iscsi-snap","kind":"vm","pool_id":"` + poolID + `","network_id":"` + netID + `"}`
	wreq, _ := http.NewRequest("POST", ts.URL+"/api/v1/workloads", strings.NewReader(body))
	wreq.Header.Set("Content-Type", "application/json")
	wreq.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	wres, err := ts.Client().Do(wreq)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(wres.Body)
	_ = wres.Body.Close()
	if wres.StatusCode != http.StatusCreated {
		t.Fatalf("create %d %s", wres.StatusCode, raw)
	}
	var created map[string]any
	if err := json.Unmarshal(raw, &created); err != nil {
		t.Fatal(err)
	}
	id, _ := created["id"].(string)
	bootRef := ""
	disks, _ := mem.ListWorkloadDisks(context.Background(), cluster.ID, id)
	if len(disks) == 1 {
		if vol, _ := mem.GetVolume(context.Background(), cluster.ID, disks[0].VolumeID); vol != nil {
			bootRef = vol.BackendRef
		}
	}

	flat, _ := http.NewRequest("POST", ts.URL+"/api/v1/workloads/"+id+"/snapshots/flatten", strings.NewReader(`{}`))
	flat.Header.Set("Content-Type", "application/json")
	flat.Header.Set("X-Nodal-Confirm", "flatten")
	flat.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	fres, err := ts.Client().Do(flat)
	if err != nil {
		t.Fatal(err)
	}
	fraw, _ := io.ReadAll(fres.Body)
	_ = fres.Body.Close()
	if fres.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("flatten %d %s", fres.StatusCode, fraw)
	}
	if !strings.Contains(string(fraw), iscsiSnapReason) {
		t.Fatalf("flatten body %s", fraw)
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
	if !strings.Contains(string(traw), iscsiSnapReason) {
		t.Fatalf("template body %s", traw)
	}

	run, _ := http.NewRequest("POST", ts.URL+"/api/v1/backups/run", strings.NewReader(`{"workload_id":"`+id+`","target_id":"`+uuid.NewString()+`"}`))
	run.Header.Set("Content-Type", "application/json")
	run.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	rres, err := ts.Client().Do(run)
	if err != nil {
		t.Fatal(err)
	}
	rraw, _ := io.ReadAll(rres.Body)
	_ = rres.Body.Close()
	if rres.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("backup %d %s", rres.StatusCode, rraw)
	}
	if !strings.Contains(string(rraw), iscsiSnapReason) {
		t.Fatalf("backup body %s", rraw)
	}
	snaps, _ := mem.ListSnapshots(context.Background(), cluster.ID, id)
	if len(snaps) != 0 {
		t.Fatalf("GET snapshots must not list a qcow2 overlay on an iSCSI LUN: %+v", snaps)
	}
	tmpls, _ := mem.ListVMTemplates(context.Background(), cluster.ID)
	if len(tmpls) != 0 {
		t.Fatalf("GET /templates must not list an iSCSI overlay template: %+v", tmpls)
	}
	runs, _ := mem.ListBackupRuns(context.Background(), cluster.ID)
	if len(runs) != 0 {
		t.Fatalf("GET /backups/runs must not list an iSCSI overlay backup: %+v", runs)
	}
	if bootRef != "" {
		vol, _ := mem.GetVolume(context.Background(), cluster.ID, disks[0].VolumeID)
		if vol == nil || vol.BackendRef != bootRef || !strings.HasPrefix(vol.BackendRef, storage.ISCSIByPath) {
			t.Fatalf("boot locator must stay an iSCSI by-path: %+v want %s", vol, bootRef)
		}
	}
}
