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
	"github.com/no-dal/ndl-ce/internal/qemu"
	"github.com/no-dal/ndl-ce/internal/rbac"
	"github.com/no-dal/ndl-ce/internal/storage"
	"github.com/no-dal/ndl-ce/internal/vmspec"
)

func TestVMSnapshotCreateRollbackAndCTHidden(t *testing.T) {
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
	s.Workloads = &fakeWorkloads{}
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)

	body := `{"name":"web","kind":"vm","pool_id":"` + poolID + `","network_id":"` + netID + `","cpus":1,"memory_bytes":268435456}`
	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/workloads", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ := ts.Client().Do(req)
	if res.StatusCode != http.StatusCreated {
		b, _ := io.ReadAll(res.Body)
		t.Fatalf("vm create %d %s", res.StatusCode, b)
	}
	var vm map[string]any
	_ = json.NewDecoder(res.Body).Decode(&vm)
	_ = res.Body.Close()
	vmID := vm["id"].(string)

	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/workloads/"+vmID+"/snapshots", strings.NewReader(`{"name":"before-upgrade"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(req)
	if res.StatusCode != http.StatusCreated {
		b, _ := io.ReadAll(res.Body)
		t.Fatalf("snap create %d %s", res.StatusCode, b)
	}
	var snap map[string]any
	_ = json.NewDecoder(res.Body).Decode(&snap)
	_ = res.Body.Close()
	if snap["purpose_tag"] != "ndl-user-before-upgrade" || snap["mechanism"] != "qcow2-overlay" {
		t.Fatalf("%+v", snap)
	}

	req, _ = http.NewRequest("GET", ts.URL+"/api/v1/workloads/"+vmID+"/snapshots", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(req)
	var listed map[string]any
	_ = json.NewDecoder(res.Body).Decode(&listed)
	_ = res.Body.Close()
	capab := listed["capability"].(map[string]any)
	if capab["supported"] != true || capab["mechanism"] != "qcow2-overlay" {
		t.Fatalf("%+v", listed)
	}

	if err := mem.UpdateWorkloadObserved(context.Background(), appdb.Workload{
		ID: vmID, Status: qemu.StatusRunning, UnitActive: true,
	}); err != nil {
		t.Fatal(err)
	}
	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/workloads/"+vmID+"/snapshots/flatten", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Nodal-Confirm", "flatten")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(req)
	rawFlat, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusUnprocessableEntity || !strings.Contains(strings.ToLower(string(rawFlat)), "stop") {
		t.Fatalf("running overlay flatten %d %s", res.StatusCode, rawFlat)
	}
	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/snapshots/"+snap["id"].(string)+"/rollback", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Nodal-Confirm", "rollback")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(req)
	raw, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusUnprocessableEntity || !strings.Contains(strings.ToLower(string(raw)), "stop") {
		t.Fatalf("running overlay rollback %d %s", res.StatusCode, raw)
	}
	if err := mem.UpdateWorkloadObserved(context.Background(), appdb.Workload{
		ID: vmID, Status: qemu.StatusStopped, UnitActive: false,
	}); err != nil {
		t.Fatal(err)
	}

	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/snapshots/"+snap["id"].(string)+"/rollback", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(req)
	if res.StatusCode != http.StatusConflict {
		t.Fatalf("rollback without confirm %d", res.StatusCode)
	}
	_ = res.Body.Close()
	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/snapshots/"+snap["id"].(string)+"/rollback", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Nodal-Confirm", "rollback")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != 200 {
		b, _ := io.ReadAll(res.Body)
		t.Fatalf("rollback %d %s", res.StatusCode, b)
	}
	_ = res.Body.Close()

	ctBody := `{"name":"alpine-a","kind":"system-container","image_pin":"alpine/3.21/amd64/default","pool_id":"` + poolID + `","network_id":"` + netID + `"}`
	s.Storage = fakeStorage{vol: storage.CreateVolumeResult{Handle: storage.VolumeHandle{
		BackendType: storage.BackendDirectory, BackendRef: "volumes/container-root/x",
		Kind: storage.KindFilesystem, Class: storage.ClassContainerRoot, Format: storage.FormatDirectory,
	}}}
	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/workloads", strings.NewReader(ctBody))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(req)
	if res.StatusCode != http.StatusCreated {
		b, _ := io.ReadAll(res.Body)
		t.Fatalf("ct create %d %s", res.StatusCode, b)
	}
	var ct map[string]any
	_ = json.NewDecoder(res.Body).Decode(&ct)
	_ = res.Body.Close()
	ctID := ct["id"].(string)

	req, _ = http.NewRequest("GET", ts.URL+"/api/v1/workloads/"+ctID+"/snapshots", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(req)
	var ctList map[string]any
	_ = json.NewDecoder(res.Body).Decode(&ctList)
	_ = res.Body.Close()
	ctCap := ctList["capability"].(map[string]any)
	if ctCap["supported"] != false {
		t.Fatal("Directory CT snapshots must be hidden")
	}
	reason, _ := ctCap["reason"].(string)
	if !strings.Contains(strings.ToLower(reason), "zfs") {
		t.Fatalf("honest CT reason: %s", reason)
	}

	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/workloads/"+ctID+"/snapshots", strings.NewReader(`{"name":"nope"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(req)
	if res.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("ct create snap %d", res.StatusCode)
	}
	_ = res.Body.Close()
}

func TestSnapshotViewerCannotCreate(t *testing.T) {
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
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	_ = claimAdmin(t, ts, token)
	hash, err := auth.HashPassword("password1")
	if err != nil {
		t.Fatal(err)
	}
	u := appdb.User{ID: uuid.NewString(), ClusterID: cluster.ID, Username: "view", PasswordHash: hash}
	_ = mem.CreateUser(context.Background(), u)
	_ = mem.BindRole(context.Background(), cluster.ID, u.ID, rbac.Viewer)
	login, _ := ts.Client().Post(ts.URL+"/api/v1/auth/login", "application/json", strings.NewReader(`{"username":"view","password":"password1"}`))
	var viewCookie string
	for _, c := range login.Cookies() {
		if c.Name == sessionCookie {
			viewCookie = c.Value
		}
	}
	_ = login.Body.Close()

	wl := appdb.Workload{ID: uuid.NewString(), ClusterID: cluster.ID, NodeID: nodeID, Name: "web", Kind: vmspec.KindVM, Status: qemu.StatusStopped}
	_ = mem.CreateWorkload(context.Background(), wl)
	_ = poolID
	_ = netID
	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/workloads/"+wl.ID+"/snapshots", strings.NewReader(`{"name":"x"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: viewCookie})
	res, _ := ts.Client().Do(req)
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("viewer snapshot %d", res.StatusCode)
	}
	_ = res.Body.Close()
}

func TestSnapshotChainCap(t *testing.T) {
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
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)
	body := `{"name":"cap","kind":"vm","pool_id":"` + poolID + `","network_id":"` + netID + `"}`
	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/workloads", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ := ts.Client().Do(req)
	var vm map[string]any
	_ = json.NewDecoder(res.Body).Decode(&vm)
	_ = res.Body.Close()
	vmID := vm["id"].(string)
	for i := 0; i < qemu.ChainMax; i++ {
		req, _ = http.NewRequest("POST", ts.URL+"/api/v1/workloads/"+vmID+"/snapshots", strings.NewReader(`{"name":"s"}`))
		req.Header.Set("Content-Type", "application/json")
		req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
		res, _ = ts.Client().Do(req)
		if res.StatusCode != http.StatusCreated {
			b, _ := io.ReadAll(res.Body)
			t.Fatalf("snap %d: %d %s", i, res.StatusCode, b)
		}
		_ = res.Body.Close()
	}
	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/workloads/"+vmID+"/snapshots", strings.NewReader(`{"name":"overflow"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(req)
	if res.StatusCode != http.StatusConflict {
		t.Fatalf("chain cap %d", res.StatusCode)
	}
	_ = res.Body.Close()
}

func TestOverlayChainDepthCountsUncataloguedLiveOverlay(t *testing.T) {
	vol := uuid.NewString()
	base := "volumes/vm-disk/" + vol + ".qcow2"
	uncatalogued := "volumes/vm-disk/" + vol + "--" + uuid.NewString() + ".qcow2"
	live := "volumes/vm-disk/" + vol + "--" + uuid.NewString() + ".qcow2"
	tmpl := "volumes/vm-disk/" + vol + "-" + uuid.NewString() + "-tmpl.qcow2"
	flat := "volumes/vm-disk/" + vol + "--flat-" + uuid.NewString() + ".qcow2"
	leftover := []appdb.Snapshot{{
		ID: uuid.NewString(), BackendRef: "volumes/vm-disk/" + vol + "--" + uuid.NewString() + ".qcow2",
	}}
	if got := overlayChainDepth(base, nil); got != 0 {
		t.Fatalf("base depth %d", got)
	}
	if got := overlayChainDepth(uncatalogued, nil); got != 1 {
		t.Fatalf("uncatalogued live depth %d", got)
	}
	if got := overlayChainDepth(live, []appdb.Snapshot{{ID: uuid.NewString(), BackendRef: uncatalogued}}); got != 2 {
		t.Fatalf("live plus uncatalogued backing depth %d", got)
	}
	if got := overlayChainDepth(tmpl, []appdb.Snapshot{{ID: uuid.NewString(), BackendRef: base}}); got != 1 {
		t.Fatalf("template from base depth %d", got)
	}
	if got := overlayChainDepth(flat, leftover); got != 0 {
		t.Fatalf("flatten live must reset leftover catalog, got %d", got)
	}
	if got := overlayChainDepth(live, []appdb.Snapshot{{ID: uuid.NewString(), BackendRef: flat}}); got != 1 {
		t.Fatalf("post-flatten overlay depth %d", got)
	}
}

type failCreateSnapshotStore struct {
	appdb.Store
	remaining int
}

func (f *failCreateSnapshotStore) CreateSnapshot(ctx context.Context, snap appdb.Snapshot) error {
	if f.remaining > 0 {
		f.remaining--
		return errors.New("snapshot catalog persist failed")
	}
	return f.Store.CreateSnapshot(ctx, snap)
}

func TestSnapshotChainCapCountsUncataloguedOverlay(t *testing.T) {
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
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)
	body := `{"name":"cap-uncat","kind":"vm","pool_id":"` + poolID + `","network_id":"` + netID + `"}`
	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/workloads", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ := ts.Client().Do(req)
	var vm map[string]any
	_ = json.NewDecoder(res.Body).Decode(&vm)
	_ = res.Body.Close()
	vmID := vm["id"].(string)

	fail := &failCreateSnapshotStore{Store: mem, remaining: 1}
	s.Store = fail
	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/workloads/"+vmID+"/snapshots", strings.NewReader(`{"name":"lost-catalog"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(req)
	raw, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusInternalServerError {
		t.Fatalf("uncatalogued overlay persist %d %s", res.StatusCode, raw)
	}

	s.Store = mem
	req, _ = http.NewRequest("GET", ts.URL+"/api/v1/workloads/"+vmID+"/snapshots", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(req)
	var listed map[string]any
	_ = json.NewDecoder(res.Body).Decode(&listed)
	_ = res.Body.Close()
	items, _ := listed["items"].([]any)
	if len(items) != 0 {
		t.Fatalf("failed persist must not catalog a snapshot %+v", listed)
	}
	capab := listed["capability"].(map[string]any)
	if capab["chain_depth"] != float64(1) {
		t.Fatalf("uncatalogued live overlay chain_depth %+v", listed)
	}

	for i := 0; i < qemu.ChainMax-1; i++ {
		req, _ = http.NewRequest("POST", ts.URL+"/api/v1/workloads/"+vmID+"/snapshots", strings.NewReader(`{"name":"s"}`))
		req.Header.Set("Content-Type", "application/json")
		req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
		res, _ = ts.Client().Do(req)
		if res.StatusCode != http.StatusCreated {
			b, _ := io.ReadAll(res.Body)
			t.Fatalf("snap %d: %d %s", i, res.StatusCode, b)
		}
		_ = res.Body.Close()
	}
	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/workloads/"+vmID+"/snapshots", strings.NewReader(`{"name":"overflow"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(req)
	if res.StatusCode != http.StatusConflict {
		b, _ := io.ReadAll(res.Body)
		t.Fatalf("uncatalogued overlay must consume chain cap %d %s", res.StatusCode, b)
	}
	_ = res.Body.Close()
}

func TestOverlayFlattenThenSnapshotDoesNotInheritStaleParent(t *testing.T) {
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
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)

	body := `{"name":"web","kind":"vm","pool_id":"` + poolID + `","network_id":"` + netID + `","cpus":1,"memory_bytes":268435456}`
	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/workloads", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ := ts.Client().Do(req)
	if res.StatusCode != http.StatusCreated {
		b, _ := io.ReadAll(res.Body)
		t.Fatalf("vm create %d %s", res.StatusCode, b)
	}
	var vm map[string]any
	_ = json.NewDecoder(res.Body).Decode(&vm)
	_ = res.Body.Close()
	vmID := vm["id"].(string)

	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/workloads/"+vmID+"/snapshots", strings.NewReader(`{"name":"before-flat"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(req)
	raw, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("snap create %d %s", res.StatusCode, raw)
	}
	var first map[string]any
	if err := json.Unmarshal(raw, &first); err != nil {
		t.Fatal(err)
	}

	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/workloads/"+vmID+"/snapshots/flatten", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Nodal-Confirm", "flatten")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(req)
	raw, _ = io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("flatten %d %s", res.StatusCode, raw)
	}

	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/workloads/"+vmID+"/snapshots", strings.NewReader(`{"name":"after-flat"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(req)
	raw, _ = io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("post-flatten snap %d %s", res.StatusCode, raw)
	}
	var second map[string]any
	if err := json.Unmarshal(raw, &second); err != nil {
		t.Fatal(err)
	}
	if second["parent_id"] != "" && second["parent_id"] != nil {
		t.Fatalf("flattened chain must not inherit leftover parent %s", raw)
	}
	if second["chain_depth"] != float64(1) {
		t.Fatalf("post-flatten chain_depth %s", raw)
	}
	if second["id"] == first["id"] {
		t.Fatalf("expected a new snapshot %s", raw)
	}
}

func TestOverlayRollbackThenSnapshotDoesNotInheritStaleParent(t *testing.T) {
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
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)

	body := `{"name":"web","kind":"vm","pool_id":"` + poolID + `","network_id":"` + netID + `","cpus":1,"memory_bytes":268435456}`
	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/workloads", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ := ts.Client().Do(req)
	if res.StatusCode != http.StatusCreated {
		b, _ := io.ReadAll(res.Body)
		t.Fatalf("vm create %d %s", res.StatusCode, b)
	}
	var vm map[string]any
	_ = json.NewDecoder(res.Body).Decode(&vm)
	_ = res.Body.Close()
	vmID := vm["id"].(string)

	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/workloads/"+vmID+"/snapshots", strings.NewReader(`{"name":"first"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(req)
	raw, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("first snap %d %s", res.StatusCode, raw)
	}
	var first map[string]any
	if err := json.Unmarshal(raw, &first); err != nil {
		t.Fatal(err)
	}

	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/workloads/"+vmID+"/snapshots", strings.NewReader(`{"name":"second"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(req)
	raw, _ = io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("second snap %d %s", res.StatusCode, raw)
	}
	var second map[string]any
	if err := json.Unmarshal(raw, &second); err != nil {
		t.Fatal(err)
	}

	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/snapshots/"+first["id"].(string)+"/rollback", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Nodal-Confirm", "rollback")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(req)
	raw, _ = io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("rollback %d %s", res.StatusCode, raw)
	}

	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/workloads/"+vmID+"/snapshots", strings.NewReader(`{"name":"after-rollback"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(req)
	raw, _ = io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("post-rollback snap %d %s", res.StatusCode, raw)
	}
	var third map[string]any
	if err := json.Unmarshal(raw, &third); err != nil {
		t.Fatal(err)
	}
	if third["parent_id"] != "" && third["parent_id"] != nil {
		t.Fatalf("rollback chain must not inherit leftover parent %s", raw)
	}
	if third["chain_depth"] != float64(1) {
		t.Fatalf("post-rollback chain_depth %s", raw)
	}
	if third["id"] == first["id"] || third["id"] == second["id"] {
		t.Fatalf("expected a new snapshot %s", raw)
	}
}

func TestOverlayRollbackThenSnapshotDoesNotHitStaleChainCap(t *testing.T) {
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
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)

	body := `{"name":"cap","kind":"vm","pool_id":"` + poolID + `","network_id":"` + netID + `"}`
	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/workloads", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ := ts.Client().Do(req)
	var vm map[string]any
	_ = json.NewDecoder(res.Body).Decode(&vm)
	_ = res.Body.Close()
	vmID := vm["id"].(string)

	var firstID string
	for i := 0; i < qemu.ChainMax; i++ {
		req, _ = http.NewRequest("POST", ts.URL+"/api/v1/workloads/"+vmID+"/snapshots", strings.NewReader(`{"name":"s"}`))
		req.Header.Set("Content-Type", "application/json")
		req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
		res, _ = ts.Client().Do(req)
		raw, _ := io.ReadAll(res.Body)
		_ = res.Body.Close()
		if res.StatusCode != http.StatusCreated {
			t.Fatalf("snap %d: %d %s", i, res.StatusCode, raw)
		}
		if i == 0 {
			var first map[string]any
			if err := json.Unmarshal(raw, &first); err != nil {
				t.Fatal(err)
			}
			firstID = first["id"].(string)
		}
	}

	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/snapshots/"+firstID+"/rollback", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Nodal-Confirm", "rollback")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(req)
	raw, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("rollback %d %s", res.StatusCode, raw)
	}

	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/workloads/"+vmID+"/snapshots", strings.NewReader(`{"name":"after-rollback"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(req)
	raw, _ = io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("post-rollback snap must not inherit leftover chain cap %d %s", res.StatusCode, raw)
	}
}

func TestVMSnapshotCreateFailsClosedForUnavailableBootVolume(t *testing.T) {
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
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)
	body := `{"name":"web","kind":"vm","pool_id":"` + poolID + `","network_id":"` + netID + `"}`
	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/workloads", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ := ts.Client().Do(req)
	if res.StatusCode != http.StatusCreated {
		raw, _ := io.ReadAll(res.Body)
		t.Fatalf("vm create %d %s", res.StatusCode, raw)
	}
	var vm map[string]any
	_ = json.NewDecoder(res.Body).Decode(&vm)
	_ = res.Body.Close()
	vmID := vm["id"].(string)
	disks, _ := mem.ListWorkloadDisks(context.Background(), cluster.ID, vmID)
	if len(disks) != 1 {
		t.Fatalf("source disks %+v", disks)
	}
	if err := mem.UpdateVolumeObserved(context.Background(), appdb.Volume{ID: disks[0].VolumeID, Status: storage.StatusUnavailable}); err != nil {
		t.Fatal(err)
	}

	list, _ := http.NewRequest("GET", ts.URL+"/api/v1/workloads/"+vmID+"/snapshots", nil)
	list.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	listed, _ := ts.Client().Do(list)
	listRaw, _ := io.ReadAll(listed.Body)
	_ = listed.Body.Close()
	if listed.StatusCode != http.StatusOK {
		t.Fatalf("GET snapshots must still list when boot storage is unavailable %d %s", listed.StatusCode, listRaw)
	}

	snapsBefore, _ := mem.ListSnapshots(context.Background(), cluster.ID, vmID)
	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/workloads/"+vmID+"/snapshots", strings.NewReader(`{"name":"before-upgrade"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(req)
	raw, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusConflict {
		t.Fatalf("unavailable boot volume snapshot %d %s", res.StatusCode, raw)
	}
	if !strings.Contains(string(raw), "storage is unavailable") {
		t.Fatalf("unavailable boot volume snapshot body %s", raw)
	}
	snapsAfter, _ := mem.ListSnapshots(context.Background(), cluster.ID, vmID)
	if len(snapsAfter) != len(snapsBefore) {
		t.Fatalf("snapshot must not persist an overlay apply cannot create: %d -> %d", len(snapsBefore), len(snapsAfter))
	}
}

type missCreateSnapshotStore struct {
	appdb.Store
}

func (missCreateSnapshotStore) CreateSnapshot(context.Context, appdb.Snapshot) error {
	return nil
}

func TestSnapshotCreateFailsClosedWhenPersistMisses(t *testing.T) {
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
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)
	body := `{"name":"web-miss","kind":"vm","pool_id":"` + poolID + `","network_id":"` + netID + `"}`
	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/workloads", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ := ts.Client().Do(req)
	raw, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("create %d %s", res.StatusCode, raw)
	}
	var vm map[string]any
	if err := json.Unmarshal(raw, &vm); err != nil {
		t.Fatal(err)
	}
	vmID := vm["id"].(string)
	s.Store = missCreateSnapshotStore{Store: mem}

	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/workloads/"+vmID+"/snapshots", strings.NewReader(`{"name":"lost-catalog"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(req)
	raw, _ = io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusInternalServerError {
		t.Fatalf("snapshot persist miss %d %s", res.StatusCode, raw)
	}
	if !strings.Contains(string(raw), "could not record snapshot") {
		t.Fatalf("snapshot persist miss body %s", raw)
	}

	s.Store = mem
	req, _ = http.NewRequest("GET", ts.URL+"/api/v1/workloads/"+vmID+"/snapshots", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(req)
	raw, _ = io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("GET snapshots %d %s", res.StatusCode, raw)
	}
	if strings.Contains(string(raw), "lost-catalog") {
		t.Fatalf("GET /snapshots must not list the snapshot after persist miss: %s", raw)
	}
}
