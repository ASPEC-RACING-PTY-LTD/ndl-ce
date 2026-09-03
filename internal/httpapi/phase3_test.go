package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/no-dal/ndl-ce/internal/appdb"
	"github.com/no-dal/ndl-ce/internal/rbac"
	"github.com/no-dal/ndl-ce/internal/secutil"
	"github.com/no-dal/ndl-ce/internal/storage"
)

type fakeStorage struct {
	pool storage.CreatePoolResult
	vol  storage.CreateVolumeResult
	img  storage.UploadResult
	obs  storage.Observation
	err  error
}

func (f fakeStorage) CreateDirectoryPool(context.Context, storage.CreatePoolRequest, []string) (storage.CreatePoolResult, error) {
	return f.pool, f.err
}
func (f fakeStorage) CreateDirectoryVolume(context.Context, storage.CreateVolumeRequest, storage.PoolHint) (storage.CreateVolumeResult, error) {
	return f.vol, f.err
}
func (f fakeStorage) GetStorage(context.Context, []storage.PoolHint) (storage.Observation, error) {
	return f.obs, nil
}
func (f fakeStorage) UploadLibrary(context.Context, storage.BeginUploadRequest, storage.PoolHint, io.Reader, string) (storage.UploadResult, error) {
	return f.img, f.err
}

func TestStorageCreatePoolAndVolume(t *testing.T) {
	s, mem, token := testServer(t)
	cluster, _ := mem.GetCluster(context.Background())
	_ = mem.UpsertNode(context.Background(), appdb.Node{ID: uuid.NewString(), ClusterID: cluster.ID, Name: "local"})
	usable := int64(50 << 30)
	s.Storage = fakeStorage{
		pool: storage.CreatePoolResult{
			RootPath: "/var/lib/ndl/storage/local", Status: storage.StatusWarning,
			Warnings: []string{storage.WarnRootFilesystem}, WarningText: []string{storage.RootHeadroomMessage},
			Capacity: storage.Capacity{UsableBytes: &usable}, Capabilities: storage.DirectoryCapabilities(true, false),
		},
		vol: storage.CreateVolumeResult{Handle: storage.VolumeHandle{
			BackendType: storage.BackendDirectory, BackendRef: "volumes/vm-disk/x.qcow2",
			Kind: storage.KindBlock, Class: storage.ClassVMDisk, Format: storage.FormatQCOW2,
		}},
	}
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := ""
	res, err := ts.Client().Post(ts.URL+"/api/v1/setup/claim", "application/json", strings.NewReader(
		`{"token":"`+token+`","username":"admin","password":"correct-horse"}`))
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range res.Cookies() {
		if c.Name == sessionCookie {
			cookie = c.Value
		}
	}
	_ = res.Body.Close()
	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/storage/pools", strings.NewReader(`{"name":"local"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, err = ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != http.StatusCreated {
		b, _ := io.ReadAll(res.Body)
		t.Fatalf("create pool %d %s", res.StatusCode, b)
	}
	var created map[string]any
	_ = json.NewDecoder(res.Body).Decode(&created)
	_ = res.Body.Close()
	if created["status"] != storage.StatusWarning {
		t.Fatalf("%v", created)
	}
	warns, _ := created["warnings"].([]any)
	if len(warns) == 0 {
		t.Fatal("root warning missing")
	}
	poolID, _ := created["id"].(string)
	body, _ := json.Marshal(map[string]any{"pool_id": poolID, "class": "vm-disk", "size_bytes": 1 << 30})
	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/storage/volumes", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, err = ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != http.StatusCreated {
		b, _ := io.ReadAll(res.Body)
		t.Fatalf("volume %d %s", res.StatusCode, b)
	}
	_ = res.Body.Close()
}

func TestDirectoryVolumeCreateFailsClosedForTinySize(t *testing.T) {
	s, mem, token := testServer(t)
	cluster, _ := mem.GetCluster(context.Background())
	_ = mem.UpsertNode(context.Background(), appdb.Node{ID: uuid.NewString(), ClusterID: cluster.ID, Name: "local"})
	usable := int64(50 << 30)
	s.Storage = fakeStorage{
		pool: storage.CreatePoolResult{
			RootPath: "/var/lib/ndl/storage/local", Status: storage.StatusWarning,
			Warnings: []string{storage.WarnRootFilesystem}, WarningText: []string{storage.RootHeadroomMessage},
			Capacity: storage.Capacity{UsableBytes: &usable}, Capabilities: storage.DirectoryCapabilities(true, false),
		},
		vol: storage.CreateVolumeResult{Handle: storage.VolumeHandle{
			BackendType: storage.BackendDirectory, BackendRef: "volumes/vm-disk/x.qcow2",
			Kind: storage.KindBlock, Class: storage.ClassVMDisk, Format: storage.FormatQCOW2,
		}},
	}
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := ""
	res, err := ts.Client().Post(ts.URL+"/api/v1/setup/claim", "application/json", strings.NewReader(
		`{"token":"`+token+`","username":"admin","password":"correct-horse"}`))
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range res.Cookies() {
		if c.Name == sessionCookie {
			cookie = c.Value
		}
	}
	_ = res.Body.Close()
	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/storage/pools", strings.NewReader(`{"name":"local"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, err = ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != http.StatusCreated {
		b, _ := io.ReadAll(res.Body)
		t.Fatalf("create pool %d %s", res.StatusCode, b)
	}
	var created map[string]any
	_ = json.NewDecoder(res.Body).Decode(&created)
	_ = res.Body.Close()
	poolID, _ := created["id"].(string)
	body, _ := json.Marshal(map[string]any{"pool_id": poolID, "class": "vm-disk", "size_bytes": 1})
	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/storage/volumes", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, err = ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("tiny directory volume %d %s", res.StatusCode, raw)
	}
	if !strings.Contains(string(raw), storage.ErrInvalidSize.Error()) {
		t.Fatalf("tiny directory volume body %s", raw)
	}
	items, _ := mem.ListVolumes(context.Background(), cluster.ID, poolID)
	if len(items) != 0 {
		t.Fatalf("GET must not list a volume apply cannot create: %+v", items)
	}
}

func TestDirectoryTemplateCreateFailsClosedForTinySize(t *testing.T) {
	s, mem, token := testServer(t)
	cluster, _ := mem.GetCluster(context.Background())
	_ = mem.UpsertNode(context.Background(), appdb.Node{ID: uuid.NewString(), ClusterID: cluster.ID, Name: "local"})
	usable := int64(50 << 30)
	s.Storage = fakeStorage{
		pool: storage.CreatePoolResult{
			RootPath: "/var/lib/ndl/storage/local", Status: storage.StatusWarning,
			Warnings: []string{storage.WarnRootFilesystem}, WarningText: []string{storage.RootHeadroomMessage},
			Capacity: storage.Capacity{UsableBytes: &usable}, Capabilities: storage.DirectoryCapabilities(true, false),
		},
		vol: storage.CreateVolumeResult{Handle: storage.VolumeHandle{
			BackendType: storage.BackendDirectory, BackendRef: "volumes/template/x.qcow2",
			Kind: storage.KindBlock, Class: storage.ClassTemplate, Format: storage.FormatQCOW2,
		}},
	}
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := ""
	res, err := ts.Client().Post(ts.URL+"/api/v1/setup/claim", "application/json", strings.NewReader(
		`{"token":"`+token+`","username":"admin","password":"correct-horse"}`))
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range res.Cookies() {
		if c.Name == sessionCookie {
			cookie = c.Value
		}
	}
	_ = res.Body.Close()
	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/storage/pools", strings.NewReader(`{"name":"local"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, err = ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != http.StatusCreated {
		b, _ := io.ReadAll(res.Body)
		t.Fatalf("create pool %d %s", res.StatusCode, b)
	}
	var created map[string]any
	_ = json.NewDecoder(res.Body).Decode(&created)
	_ = res.Body.Close()
	poolID, _ := created["id"].(string)
	body, _ := json.Marshal(map[string]any{"pool_id": poolID, "class": "template", "size_bytes": 1})
	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/storage/volumes", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, err = ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("tiny template volume %d %s", res.StatusCode, raw)
	}
	if !strings.Contains(string(raw), storage.ErrInvalidSize.Error()) {
		t.Fatalf("tiny template volume body %s", raw)
	}
	items, _ := mem.ListVolumes(context.Background(), cluster.ID, poolID)
	if len(items) != 0 {
		t.Fatalf("GET must not list a template volume apply cannot create: %+v", items)
	}
}

func TestStorageUploadAndListRefresh(t *testing.T) {
	s, mem, token := testServer(t)
	cluster, _ := mem.GetCluster(context.Background())
	nodeID := uuid.NewString()
	_ = mem.UpsertNode(context.Background(), appdb.Node{ID: nodeID, ClusterID: cluster.ID, Name: "local"})
	poolID := uuid.NewString()
	_ = mem.CreateStoragePool(context.Background(), appdb.StoragePool{
		ID: poolID, ClusterID: cluster.ID, NodeID: nodeID, Name: "local",
		BackendType: storage.BackendDirectory, Status: storage.StatusAvailable, RootPath: "/mnt/p",
	})
	itemID := uuid.NewString()
	s.Storage = fakeStorage{
		img: storage.UploadResult{
			ItemID: itemID, PoolID: poolID, Kind: storage.LibraryISO, DisplayName: "test.iso",
			BackendRef: "library/iso/" + itemID + ".iso", SizeBytes: 32, SHA256: "abc123",
		},
		obs: storage.Observation{Pools: []storage.ObservedPool{{
			PoolID: poolID, Status: storage.StatusUnavailable, Reason: "missing",
		}}},
	}
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := ""
	res, err := ts.Client().Post(ts.URL+"/api/v1/setup/claim", "application/json", strings.NewReader(
		`{"token":"`+token+`","username":"admin","password":"correct-horse"}`))
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range res.Cookies() {
		if c.Name == sessionCookie {
			cookie = c.Value
		}
	}
	_ = res.Body.Close()
	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/storage/images?pool_id="+poolID+"&kind=iso&filename=test.iso", strings.NewReader("not-used"))
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, err = ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != http.StatusCreated {
		b, _ := io.ReadAll(res.Body)
		t.Fatalf("upload %d %s", res.StatusCode, b)
	}
	_ = res.Body.Close()
	_ = mem.CreateVolume(context.Background(), appdb.Volume{
		ID: uuid.NewString(), ClusterID: cluster.ID, NodeID: nodeID, PoolID: poolID,
		Class: storage.ClassVMDisk, Kind: storage.KindBlock, Format: storage.FormatQCOW2,
		SizeBytes: 1, Status: storage.StatusAvailable, BackendType: storage.BackendDirectory, BackendRef: "volumes/vm-disk/x.qcow2",
	})
	req, _ = http.NewRequest("GET", ts.URL+"/api/v1/storage/volumes", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, err = ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	var listed struct {
		Items []map[string]any `json:"items"`
	}
	_ = json.NewDecoder(res.Body).Decode(&listed)
	_ = res.Body.Close()
	if len(listed.Items) != 1 || listed.Items[0]["status"] != storage.StatusUnavailable {
		t.Fatalf("volume status after missing pool: %+v", listed.Items)
	}
}

func TestStorageViewerCannotMutate(t *testing.T) {
	s, mem, token := testServer(t)
	cluster, _ := mem.GetCluster(context.Background())
	_ = mem.UpsertNode(context.Background(), appdb.Node{ID: uuid.NewString(), ClusterID: cluster.ID, Name: "local"})
	s.Storage = fakeStorage{pool: storage.CreatePoolResult{RootPath: "/mnt/p", Status: storage.StatusAvailable}}
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	res, err := ts.Client().Post(ts.URL+"/api/v1/setup/claim", "application/json", strings.NewReader(
		`{"token":"`+token+`","username":"admin","password":"correct-horse"}`))
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	viewer := appdb.User{ID: uuid.NewString(), ClusterID: cluster.ID, Username: "view"}
	_ = mem.CreateUser(context.Background(), viewer)
	_ = mem.BindRole(context.Background(), cluster.ID, viewer.ID, rbac.Viewer)
	plain := "ndl_viewer_token"
	_ = mem.CreateToken(context.Background(), appdb.APIToken{
		ID: uuid.NewString(), ClusterID: cluster.ID, UserID: viewer.ID, Name: "v",
		TokenHash: secutil.HashSHA256(plain), Prefix: "ndl_view",
	})
	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/storage/pools", strings.NewReader(`{"name":"x","path":"/mnt/p"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+plain)
	res, err = ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("viewer mutate=%d", res.StatusCode)
	}
	_ = res.Body.Close()
	req, _ = http.NewRequest("GET", ts.URL+"/api/v1/storage/pools", nil)
	req.Header.Set("Authorization", "Bearer "+plain)
	res, err = ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != http.StatusOK {
		t.Fatalf("viewer read=%d", res.StatusCode)
	}
	_ = res.Body.Close()
}

func TestDirectoryVolumeCreateFailsClosedForFailedPool(t *testing.T) {
	s, mem, token := testServer(t)
	cluster, _ := mem.GetCluster(context.Background())
	_ = mem.UpsertNode(context.Background(), appdb.Node{ID: uuid.NewString(), ClusterID: cluster.ID, Name: "local"})
	s.Storage = fakeStorage{vol: storage.CreateVolumeResult{Handle: storage.VolumeHandle{
		BackendType: storage.BackendDirectory, BackendRef: "volumes/vm-disk/x.qcow2",
		Kind: storage.KindBlock, Class: storage.ClassVMDisk, Format: storage.FormatQCOW2,
	}}}
	failedPool := uuid.NewString()
	if err := mem.CreateStoragePool(context.Background(), appdb.StoragePool{
		ID: failedPool, ClusterID: cluster.ID, Name: "failed-vol",
		BackendType: storage.BackendDirectory, Status: storage.StatusFailed,
		RootPath: "/var/lib/ndl/storage/failed-vol",
	}); err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)
	volsBefore, _ := mem.ListVolumes(context.Background(), cluster.ID, "")
	body, _ := json.Marshal(map[string]any{"pool_id": failedPool, "class": "vm-disk", "size_bytes": 1 << 30})
	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/storage/volumes", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ := ts.Client().Do(req)
	raw, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusConflict {
		t.Fatalf("failed pool volume %d %s", res.StatusCode, raw)
	}
	if !strings.Contains(string(raw), "storage pool is unavailable") {
		t.Fatalf("failed pool volume body %s", raw)
	}
	volsAfter, _ := mem.ListVolumes(context.Background(), cluster.ID, "")
	if len(volsAfter) != len(volsBefore) {
		t.Fatalf("GET must not list a volume apply cannot allocate: %d -> %d", len(volsBefore), len(volsAfter))
	}
}

func TestStorageUploadFailsClosedForFailedPool(t *testing.T) {
	s, mem, token := testServer(t)
	cluster, _ := mem.GetCluster(context.Background())
	nodeID := uuid.NewString()
	_ = mem.UpsertNode(context.Background(), appdb.Node{ID: nodeID, ClusterID: cluster.ID, Name: "local"})
	failedPool := uuid.NewString()
	if err := mem.CreateStoragePool(context.Background(), appdb.StoragePool{
		ID: failedPool, ClusterID: cluster.ID, NodeID: nodeID, Name: "failed-img",
		BackendType: storage.BackendDirectory, Status: storage.StatusFailed, RootPath: "/mnt/failed-img",
	}); err != nil {
		t.Fatal(err)
	}
	s.Storage = fakeStorage{img: storage.UploadResult{
		ItemID: uuid.NewString(), PoolID: failedPool, Kind: storage.LibraryISO, DisplayName: "test.iso",
		BackendRef: "library/iso/x.iso", SizeBytes: 32, SHA256: "abc123",
	}}
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)
	libsBefore, _ := mem.ListLibraryItems(context.Background(), cluster.ID, "")
	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/storage/images?pool_id="+failedPool+"&kind=iso&filename=test.iso", strings.NewReader("not-used"))
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ := ts.Client().Do(req)
	raw, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusConflict {
		t.Fatalf("failed pool upload %d %s", res.StatusCode, raw)
	}
	if !strings.Contains(string(raw), "storage pool is unavailable") {
		t.Fatalf("failed pool upload body %s", raw)
	}
	libsAfter, _ := mem.ListLibraryItems(context.Background(), cluster.ID, "")
	if len(libsAfter) != len(libsBefore) {
		t.Fatalf("GET must not list a library item upload cannot write: %d -> %d", len(libsBefore), len(libsAfter))
	}
}

func TestEmitEventFailsClosedWhenPersistFails(t *testing.T) {
	s, mem, _ := testServer(t)
	cluster, _ := mem.GetCluster(context.Background())
	hub := &EventHub{}
	s.Hub = hub
	ch := hub.subscribe()
	s.Store = failInsertEventStore{Store: mem}
	s.emitEvent(context.Background(), cluster.ID, "", "workload.created", map[string]string{"workload_id": "missing"})
	select {
	case e := <-ch:
		t.Fatalf("hub published an event GET /events would miss: %+v", e)
	default:
	}
	ev, _ := mem.ListEvents(context.Background(), cluster.ID, 50)
	for _, e := range ev {
		if e.Type == "workload.created" {
			t.Fatalf("event persist fail must not record: %+v", e)
		}
	}
}
