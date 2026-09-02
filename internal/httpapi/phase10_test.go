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
