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
	"github.com/no-dal/ndl-ce/internal/lxc"
	"github.com/no-dal/ndl-ce/internal/ndnet"
	"github.com/no-dal/ndl-ce/internal/oci"
	"github.com/no-dal/ndl-ce/internal/rbac"
	"github.com/no-dal/ndl-ce/internal/secutil"
	"github.com/no-dal/ndl-ce/internal/storage"
)

type fakeWorkloads struct {
	created  lxc.Result
	life     lxc.Result
	obs      lxc.Observation
	err      error
	creates  int
	vols     []string
	lastSpec lxc.Spec
	lastLife lxc.LifecycleRequest
}

func (f *fakeWorkloads) CreateCT(_ context.Context, spec lxc.Spec) (lxc.Result, error) {
	f.creates++
	f.lastSpec = spec
	f.vols = append(f.vols, spec.VolumeID)
	res := f.created
	if res.WorkloadID == "" {
		res.WorkloadID = spec.WorkloadID
	}
	if res.VolumeID == "" {
		res.VolumeID = spec.VolumeID
	}
	if res.MAC == "" {
		res.MAC = spec.MAC
	}
	if res.Status == "" {
		if spec.NoStart {
			res.Status = lxc.StatusStopped
		} else {
			res.Status = lxc.StatusRunning
		}
	}
	res.ImageVerified = true
	return res, f.err
}

func (f *fakeWorkloads) LifecycleCT(_ context.Context, req lxc.LifecycleRequest) (lxc.Result, error) {
	f.lastLife = req
	res := f.life
	if res.WorkloadID == "" {
		if req.Action == "clone" {
			res.WorkloadID = req.CloneID
			res.VolumeID = req.CloneVolumeID
			res.MAC = req.CloneMAC
			res.Status = lxc.StatusStopped
		} else {
			res.WorkloadID = req.WorkloadID
			res.Status = lxc.StatusRunning
		}
	}
	return res, f.err
}

func (f *fakeWorkloads) GetWorkloads(context.Context, []lxc.Hint) (lxc.Observation, error) {
	return f.obs, nil
}

func seedCompute(t *testing.T, mem *appdb.Memory, clusterID, nodeID string) (poolID, netID string) {
	t.Helper()
	poolID = uuid.NewString()
	netID = uuid.NewString()
	if err := mem.CreateStoragePool(context.Background(), appdb.StoragePool{
		ID: poolID, ClusterID: clusterID, NodeID: nodeID, Name: "local",
		BackendType: storage.BackendDirectory, Status: storage.StatusAvailable,
		RootPath: "/var/lib/ndl/storage/local",
	}); err != nil {
		t.Fatal(err)
	}
	if err := mem.CreateNetwork(context.Background(), appdb.Network{
		ID: netID, ClusterID: clusterID, NodeID: nodeID, Name: "iso",
		Kind: ndnet.KindIsolated, Status: ndnet.StatusAvailable, BridgeName: "ndldeadbeef",
	}); err != nil {
		t.Fatal(err)
	}
	return poolID, netID
}

func TestWorkloadCreateAndIdempotency(t *testing.T) {
	s, mem, token := testServer(t)
	cluster, _ := mem.GetCluster(context.Background())
	nodeID := uuid.NewString()
	_ = mem.UpsertNode(context.Background(), appdb.Node{ID: nodeID, ClusterID: cluster.ID, Name: "local"})
	poolID, netID := seedCompute(t, mem, cluster.ID, nodeID)
	fw := &fakeWorkloads{}
	s.Workloads = fw
	s.Storage = fakeStorage{
		vol: storage.CreateVolumeResult{Handle: storage.VolumeHandle{
			BackendType: storage.BackendDirectory, BackendRef: "volumes/container-root/x",
			Kind: storage.KindFilesystem, Class: storage.ClassContainerRoot, Format: storage.FormatDirectory,
		}},
	}
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)
	body := `{"name":"alpine-a","kind":"system-container","image_pin":"alpine/3.21/amd64/default","pool_id":"` + poolID + `","network_id":"` + netID + `"}`
	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/workloads", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", "create-alpine-a")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != http.StatusCreated {
		b, _ := io.ReadAll(res.Body)
		t.Fatalf("create %d %s", res.StatusCode, b)
	}
	var first map[string]any
	_ = json.NewDecoder(res.Body).Decode(&first)
	_ = res.Body.Close()
	req2, _ := http.NewRequest("POST", ts.URL+"/api/v1/workloads", strings.NewReader(body))
	req2.Header.Set("Content-Type", "application/json")
	req2.Header.Set("Idempotency-Key", "create-alpine-a")
	req2.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res2, err := ts.Client().Do(req2)
	if err != nil {
		t.Fatal(err)
	}
	if res2.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(res2.Body)
		t.Fatalf("replay %d %s", res2.StatusCode, b)
	}
	var second map[string]any
	_ = json.NewDecoder(res2.Body).Decode(&second)
	_ = res2.Body.Close()
	if first["id"] != second["id"] {
		t.Fatalf("workload id changed %v %v", first["id"], second["id"])
	}
	disks, _ := mem.ListWorkloadDisks(context.Background(), cluster.ID, first["id"].(string))
	if len(disks) != 1 {
		t.Fatalf("disks %d", len(disks))
	}
	nics, _ := mem.ListWorkloadNICs(context.Background(), cluster.ID, first["id"].(string))
	if len(nics) != 1 || nics[0].NetworkID != netID {
		t.Fatalf("nics %+v", nics)
	}
	vols, _ := mem.ListVolumes(context.Background(), cluster.ID, "")
	if len(vols) != 1 {
		t.Fatalf("second volume created: %d", len(vols))
	}
	if len(fw.vols) > 0 && fw.vols[0] != disks[0].VolumeID {
		t.Fatalf("volume mismatch %s %s", fw.vols[0], disks[0].VolumeID)
	}
}

func TestWorkloadViewerReadOnlyAndOperatorDeniedPrivileged(t *testing.T) {
	s, mem, token := testServer(t)
	cluster, _ := mem.GetCluster(context.Background())
	nodeID := uuid.NewString()
	_ = mem.UpsertNode(context.Background(), appdb.Node{ID: nodeID, ClusterID: cluster.ID, Name: "local"})
	poolID, netID := seedCompute(t, mem, cluster.ID, nodeID)
	s.Workloads = &fakeWorkloads{}
	s.Storage = fakeStorage{vol: storage.CreateVolumeResult{Handle: storage.VolumeHandle{
		BackendRef: "volumes/container-root/x", Class: storage.ClassContainerRoot,
	}}}
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	_ = claimAdmin(t, ts, token)

	op := appdb.User{ID: uuid.NewString(), ClusterID: cluster.ID, Username: "op"}
	_ = mem.CreateUser(context.Background(), op)
	_ = mem.BindRole(context.Background(), cluster.ID, op.ID, rbac.Operator)
	plain := "ndl_op_token"
	_ = mem.CreateToken(context.Background(), appdb.APIToken{
		ID: uuid.NewString(), ClusterID: cluster.ID, UserID: op.ID, Name: "op",
		TokenHash: secutil.HashSHA256(plain), Prefix: "ndl_op",
	})
	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/workloads", strings.NewReader(
		`{"name":"priv","kind":"system-container","image_pin":"alpine/3.21/amd64/default","pool_id":"`+poolID+`","network_id":"`+netID+`","privileged":true}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+plain)
	res, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != http.StatusForbidden {
		b, _ := io.ReadAll(res.Body)
		t.Fatalf("operator privileged %d %s", res.StatusCode, b)
	}
	_ = res.Body.Close()

	view := appdb.User{ID: uuid.NewString(), ClusterID: cluster.ID, Username: "view"}
	_ = mem.CreateUser(context.Background(), view)
	_ = mem.BindRole(context.Background(), cluster.ID, view.ID, rbac.Viewer)
	vtok := "ndl_view_token"
	_ = mem.CreateToken(context.Background(), appdb.APIToken{
		ID: uuid.NewString(), ClusterID: cluster.ID, UserID: view.ID, Name: "v",
		TokenHash: secutil.HashSHA256(vtok), Prefix: "ndl_vi",
	})
	list, _ := http.NewRequest("GET", ts.URL+"/api/v1/workloads", nil)
	list.Header.Set("Authorization", "Bearer "+vtok)
	listRes, err := ts.Client().Do(list)
	if err != nil {
		t.Fatal(err)
	}
	if listRes.StatusCode != http.StatusOK {
		t.Fatalf("viewer list %d", listRes.StatusCode)
	}
	_ = listRes.Body.Close()
	create, _ := http.NewRequest("POST", ts.URL+"/api/v1/workloads", strings.NewReader(
		`{"name":"x","kind":"system-container","image_pin":"alpine/3.21/amd64/default","pool_id":"`+poolID+`","network_id":"`+netID+`"}`))
	create.Header.Set("Content-Type", "application/json")
	create.Header.Set("Authorization", "Bearer "+vtok)
	cRes, err := ts.Client().Do(create)
	if err != nil {
		t.Fatal(err)
	}
	if cRes.StatusCode != http.StatusForbidden {
		t.Fatalf("viewer create %d", cRes.StatusCode)
	}
	_ = cRes.Body.Close()

	privID := uuid.NewString()
	_ = mem.CreateWorkload(context.Background(), appdb.Workload{
		ID: privID, ClusterID: cluster.ID, NodeID: nodeID, OwnerNodeID: nodeID, DesiredNodeID: nodeID,
		Name: "admin-priv", Kind: lxc.KindSystemContainer, Status: lxc.StatusStopped,
		ImagePin: "alpine/3.21/amd64/default", Privileged: true,
	})
	clone, _ := http.NewRequest("POST", ts.URL+"/api/v1/workloads/"+privID+"/clone", strings.NewReader(`{"name":"stolen"}`))
	clone.Header.Set("Content-Type", "application/json")
	clone.Header.Set("Authorization", "Bearer "+plain)
	clRes, err := ts.Client().Do(clone)
	if err != nil {
		t.Fatal(err)
	}
	if clRes.StatusCode != http.StatusForbidden {
		b, _ := io.ReadAll(clRes.Body)
		t.Fatalf("operator clone privileged %d %s", clRes.StatusCode, b)
	}
	_ = clRes.Body.Close()
}

func TestLifecycleTokenCannotDeleteWorkload(t *testing.T) {
	s, mem, token := testServer(t)
	cluster, _ := mem.GetCluster(context.Background())
	nodeID := uuid.NewString()
	_ = mem.UpsertNode(context.Background(), appdb.Node{ID: nodeID, ClusterID: cluster.ID, Name: "local"})
	id := uuid.NewString()
	_ = mem.CreateWorkload(context.Background(), appdb.Workload{
		ID: id, ClusterID: cluster.ID, NodeID: nodeID, OwnerNodeID: nodeID, DesiredNodeID: nodeID,
		Name: "ct", Kind: lxc.KindSystemContainer, Status: lxc.StatusStopped,
		ImagePin: "alpine/3.21/amd64/default", DesiredPower: "stopped",
	})
	s.Workloads = &fakeWorkloads{}
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	_ = claimAdmin(t, ts, token)
	admin, err := mem.GetUserByName(context.Background(), cluster.ID, "admin")
	if err != nil || admin == nil {
		t.Fatal("admin user")
	}
	plain := "ndl_lifecycle_only"
	if err := mem.CreateToken(context.Background(), appdb.APIToken{
		ID: uuid.NewString(), ClusterID: cluster.ID, UserID: admin.ID, Name: "life",
		TokenHash: secutil.HashSHA256(plain), Prefix: "ndl_lf",
		Permissions: []string{rbac.ComputeLifecycle, rbac.ComputeRead},
	}); err != nil {
		t.Fatal(err)
	}

	start, _ := http.NewRequest("POST", ts.URL+"/api/v1/workloads/"+id+"/start", strings.NewReader("{}"))
	start.Header.Set("Content-Type", "application/json")
	start.Header.Set("Authorization", "Bearer "+plain)
	res, err := ts.Client().Do(start)
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(res.Body)
		t.Fatalf("lifecycle start %d %s", res.StatusCode, b)
	}
	_ = res.Body.Close()

	del, _ := http.NewRequest("POST", ts.URL+"/api/v1/workloads/"+id+"/delete", strings.NewReader("{}"))
	del.Header.Set("Content-Type", "application/json")
	del.Header.Set("Authorization", "Bearer "+plain)
	res, err = ts.Client().Do(del)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("lifecycle must not delete %d %s", res.StatusCode, b)
	}
}

func TestLifecycleTokenCannotCloneOrPatchSpec(t *testing.T) {
	s, mem, token := testServer(t)
	cluster, _ := mem.GetCluster(context.Background())
	nodeID := uuid.NewString()
	_ = mem.UpsertNode(context.Background(), appdb.Node{ID: nodeID, ClusterID: cluster.ID, Name: "local"})
	id := uuid.NewString()
	_ = mem.CreateWorkload(context.Background(), appdb.Workload{
		ID: id, ClusterID: cluster.ID, NodeID: nodeID, OwnerNodeID: nodeID, DesiredNodeID: nodeID,
		Name: "ct", Kind: lxc.KindSystemContainer, Status: lxc.StatusStopped,
		ImagePin: "alpine/3.21/amd64/default", DesiredPower: "stopped", CPUs: 1,
	})
	s.Workloads = &fakeWorkloads{}
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	_ = claimAdmin(t, ts, token)
	admin, err := mem.GetUserByName(context.Background(), cluster.ID, "admin")
	if err != nil || admin == nil {
		t.Fatal("admin user")
	}
	life := "ndl_life_patch"
	if err := mem.CreateToken(context.Background(), appdb.APIToken{
		ID: uuid.NewString(), ClusterID: cluster.ID, UserID: admin.ID, Name: "life",
		TokenHash: secutil.HashSHA256(life), Prefix: "ndl_lp",
		Permissions: []string{rbac.ComputeLifecycle, rbac.ComputeRead},
	}); err != nil {
		t.Fatal(err)
	}
	mod := "ndl_modify_patch"
	if err := mem.CreateToken(context.Background(), appdb.APIToken{
		ID: uuid.NewString(), ClusterID: cluster.ID, UserID: admin.ID, Name: "mod",
		TokenHash: secutil.HashSHA256(mod), Prefix: "ndl_md",
		Permissions: []string{rbac.ComputeModify, rbac.ComputeRead},
	}); err != nil {
		t.Fatal(err)
	}

	cpu, _ := http.NewRequest("PATCH", ts.URL+"/api/v1/workloads/"+id, strings.NewReader(`{"cpus":2}`))
	cpu.Header.Set("Content-Type", "application/json")
	cpu.Header.Set("Authorization", "Bearer "+life)
	res, err := ts.Client().Do(cpu)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("lifecycle must not patch spec %d %s", res.StatusCode, b)
	}

	power, _ := http.NewRequest("PATCH", ts.URL+"/api/v1/workloads/"+id, strings.NewReader(`{"desired_power":"running"}`))
	power.Header.Set("Content-Type", "application/json")
	power.Header.Set("Authorization", "Bearer "+life)
	res, err = ts.Client().Do(power)
	if err != nil {
		t.Fatal(err)
	}
	b, _ = io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("lifecycle may patch desired_power %d %s", res.StatusCode, b)
	}

	clone, _ := http.NewRequest("POST", ts.URL+"/api/v1/workloads/"+id+"/clone", strings.NewReader(`{"name":"copy"}`))
	clone.Header.Set("Content-Type", "application/json")
	clone.Header.Set("Authorization", "Bearer "+life)
	res, err = ts.Client().Do(clone)
	if err != nil {
		t.Fatal(err)
	}
	b, _ = io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("lifecycle must not clone %d %s", res.StatusCode, b)
	}

	cpu, _ = http.NewRequest("PATCH", ts.URL+"/api/v1/workloads/"+id, strings.NewReader(`{"cpus":2}`))
	cpu.Header.Set("Content-Type", "application/json")
	cpu.Header.Set("Authorization", "Bearer "+mod)
	res, err = ts.Client().Do(cpu)
	if err != nil {
		t.Fatal(err)
	}
	b, _ = io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("modify may patch spec %d %s", res.StatusCode, b)
	}
}

func TestWorkloadLifecycleStart(t *testing.T) {
	s, mem, token := testServer(t)
	cluster, _ := mem.GetCluster(context.Background())
	nodeID := uuid.NewString()
	_ = mem.UpsertNode(context.Background(), appdb.Node{ID: nodeID, ClusterID: cluster.ID, Name: "local"})
	id := uuid.NewString()
	_ = mem.CreateWorkload(context.Background(), appdb.Workload{
		ID: id, ClusterID: cluster.ID, NodeID: nodeID, OwnerNodeID: nodeID, DesiredNodeID: nodeID,
		Name: "ct", Kind: lxc.KindSystemContainer, Status: lxc.StatusStopped,
		ImagePin: "alpine/3.21/amd64/default", DesiredPower: "stopped",
	})
	s.Workloads = &fakeWorkloads{}
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)
	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/workloads/"+id+"/start", strings.NewReader("{}"))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(res.Body)
		t.Fatalf("start %d %s", res.StatusCode, b)
	}
	_ = res.Body.Close()
}

func TestCTCreateRejectsEscapingRootfsLocator(t *testing.T) {
	s, mem, token := testServer(t)
	cluster, _ := mem.GetCluster(context.Background())
	nodeID := uuid.NewString()
	_ = mem.UpsertNode(context.Background(), appdb.Node{ID: nodeID, ClusterID: cluster.ID, Name: "local"})
	poolID, netID := seedCompute(t, mem, cluster.ID, nodeID)
	fw := &fakeWorkloads{}
	s.Workloads = fw
	s.Storage = fakeStorage{vol: storage.CreateVolumeResult{Handle: storage.VolumeHandle{
		BackendType: storage.BackendDirectory, BackendRef: "../../etc",
		Kind: storage.KindFilesystem, Class: storage.ClassContainerRoot, Format: storage.FormatDirectory,
	}}}
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)
	body := `{"name":"escape","kind":"system-container","image_pin":"alpine/3.21/amd64/default","pool_id":"` + poolID + `","network_id":"` + netID + `"}`
	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/workloads", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusConflict || !strings.Contains(strings.ToLower(string(raw)), "locator") {
		t.Fatalf("escaping rootfs %d %s", res.StatusCode, raw)
	}
	if fw.creates != 0 {
		t.Fatal("agent must not receive an escaped rootfs")
	}
}

func TestCTCreateStoppedDoesNotStart(t *testing.T) {
	s, mem, token := testServer(t)
	cluster, _ := mem.GetCluster(context.Background())
	nodeID := uuid.NewString()
	_ = mem.UpsertNode(context.Background(), appdb.Node{ID: nodeID, ClusterID: cluster.ID, Name: "local"})
	poolID, netID := seedCompute(t, mem, cluster.ID, nodeID)
	fw := &fakeWorkloads{}
	s.Workloads = fw
	s.Storage = fakeStorage{vol: storage.CreateVolumeResult{Handle: storage.VolumeHandle{
		BackendType: storage.BackendDirectory, BackendRef: "volumes/container-root/x",
		Kind: storage.KindFilesystem, Class: storage.ClassContainerRoot, Format: storage.FormatDirectory,
	}}}
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)
	body := `{"name":"alpine-stopped","kind":"system-container","image_pin":"alpine/3.21/amd64/default","pool_id":"` + poolID + `","network_id":"` + netID + `","desired_power":"stopped"}`
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
	if !fw.lastSpec.NoStart {
		t.Fatal("stopped create must pass NoStart")
	}
	var created map[string]any
	if err := json.Unmarshal(raw, &created); err != nil {
		t.Fatal(err)
	}
	if created["desired_power"] != "stopped" || created["status"] != lxc.StatusStopped {
		t.Fatalf("stopped create %+v", created)
	}
}

func TestCTPatchFailsClosedWhenApplyFails(t *testing.T) {
	s, mem, token := testServer(t)
	cluster, _ := mem.GetCluster(context.Background())
	nodeID := uuid.NewString()
	_ = mem.UpsertNode(context.Background(), appdb.Node{ID: nodeID, ClusterID: cluster.ID, Name: "local"})
	id := uuid.NewString()
	_ = mem.CreateWorkload(context.Background(), appdb.Workload{
		ID: id, ClusterID: cluster.ID, NodeID: nodeID, OwnerNodeID: nodeID, DesiredNodeID: nodeID,
		Name: "ct", Kind: lxc.KindSystemContainer, Status: lxc.StatusStopped,
		ImagePin: "alpine/3.21/amd64/default", DesiredPower: "stopped",
	})
	s.Workloads = &fakeWorkloads{err: errBadRequest("agent apply failed")}
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)
	req, _ := http.NewRequest("PATCH", ts.URL+"/api/v1/workloads/"+id, strings.NewReader(`{"cpus":2}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("patch apply %d %s", res.StatusCode, raw)
	}
}

func TestCTPatchFailsClosedWhenAgentUnavailable(t *testing.T) {
	s, mem, token := testServer(t)
	cluster, _ := mem.GetCluster(context.Background())
	nodeID := uuid.NewString()
	_ = mem.UpsertNode(context.Background(), appdb.Node{ID: nodeID, ClusterID: cluster.ID, Name: "local"})
	id := uuid.NewString()
	if err := mem.CreateWorkload(context.Background(), appdb.Workload{
		ID: id, ClusterID: cluster.ID, NodeID: nodeID, OwnerNodeID: nodeID, DesiredNodeID: nodeID,
		Name: "ct", Kind: lxc.KindSystemContainer, Status: lxc.StatusStopped,
		ImagePin: "alpine/3.21/amd64/default", DesiredPower: "stopped", CPUs: 1,
	}); err != nil {
		t.Fatal(err)
	}
	s.Workloads = nil
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)
	req, _ := http.NewRequest("PATCH", ts.URL+"/api/v1/workloads/"+id, strings.NewReader(`{"cpus":2}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusBadGateway {
		t.Fatalf("unavailable patch %d %s", res.StatusCode, raw)
	}
	if !strings.Contains(string(raw), "workload agent is unavailable") {
		t.Fatalf("unavailable patch body %s", raw)
	}
	got, err := mem.GetWorkload(context.Background(), cluster.ID, id)
	if err != nil || got == nil || got.CPUs != 1 {
		t.Fatalf("unavailable patch must not rewrite CPUs %+v %v", got, err)
	}
}

func TestOCIPatchDoesNotCreateCT(t *testing.T) {
	s, mem, token := testServer(t)
	cluster, _ := mem.GetCluster(context.Background())
	nodeID := uuid.NewString()
	_ = mem.UpsertNode(context.Background(), appdb.Node{ID: nodeID, ClusterID: cluster.ID, Name: "local"})
	id := uuid.NewString()
	_ = mem.CreateWorkload(context.Background(), appdb.Workload{
		ID: id, ClusterID: cluster.ID, NodeID: nodeID, OwnerNodeID: nodeID, DesiredNodeID: nodeID,
		Name: "app", Kind: oci.KindOCI, Status: oci.StatusStopped, DesiredPower: "stopped",
		ImagePin: "busybox:1",
	})
	fw := &fakeWorkloads{}
	s.Workloads = fw
	s.OCI = &fakeOCI{runtime: &oci.FakeRuntime{}}
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)
	req, _ := http.NewRequest("PATCH", ts.URL+"/api/v1/workloads/"+id, strings.NewReader(`{"cpus":2,"desired_power":"running"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("oci patch %d %s", res.StatusCode, raw)
	}
	if fw.creates != 0 {
		t.Fatal("OCI patch must not call CreateCT")
	}
}

func TestWorkloadCreateFailsClosedWhenDiskPersistFails(t *testing.T) {
	s, mem, token := testServer(t)
	cluster, _ := mem.GetCluster(context.Background())
	nodeID := uuid.NewString()
	_ = mem.UpsertNode(context.Background(), appdb.Node{ID: nodeID, ClusterID: cluster.ID, Name: "local"})
	poolID, netID := seedCompute(t, mem, cluster.ID, nodeID)
	s.Workloads = &fakeWorkloads{}
	s.Storage = fakeStorage{vol: storage.CreateVolumeResult{Handle: storage.VolumeHandle{
		BackendType: storage.BackendDirectory, BackendRef: "volumes/container-root/x",
		Kind: storage.KindFilesystem, Class: storage.ClassContainerRoot, Format: storage.FormatDirectory,
	}}}
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)
	s.Store = failCreateWorkloadDiskStore{Store: mem}

	body := `{"name":"alpine-a","kind":"system-container","image_pin":"alpine/3.21/amd64/default","pool_id":"` + poolID + `","network_id":"` + netID + `"}`
	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/workloads", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ := ts.Client().Do(req)
	raw, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusInternalServerError {
		t.Fatalf("disk persist %d %s", res.StatusCode, raw)
	}
	if !strings.Contains(string(raw), "could not record container disk") {
		t.Fatalf("disk persist body %s", raw)
	}
}

func TestWorkloadCreateFailsClosedWhenNICPersistFails(t *testing.T) {
	s, mem, token := testServer(t)
	cluster, _ := mem.GetCluster(context.Background())
	nodeID := uuid.NewString()
	_ = mem.UpsertNode(context.Background(), appdb.Node{ID: nodeID, ClusterID: cluster.ID, Name: "local"})
	poolID, netID := seedCompute(t, mem, cluster.ID, nodeID)
	s.Workloads = &fakeWorkloads{}
	s.Storage = fakeStorage{vol: storage.CreateVolumeResult{Handle: storage.VolumeHandle{
		BackendType: storage.BackendDirectory, BackendRef: "volumes/container-root/x",
		Kind: storage.KindFilesystem, Class: storage.ClassContainerRoot, Format: storage.FormatDirectory,
	}}}
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)
	s.Store = failCreateWorkloadNICStore{Store: mem}

	body := `{"name":"alpine-a","kind":"system-container","image_pin":"alpine/3.21/amd64/default","pool_id":"` + poolID + `","network_id":"` + netID + `"}`
	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/workloads", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ := ts.Client().Do(req)
	raw, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusInternalServerError {
		t.Fatalf("nic persist %d %s", res.StatusCode, raw)
	}
	if !strings.Contains(string(raw), "could not record container NIC") {
		t.Fatalf("nic persist body %s", raw)
	}
}

func createTestSystemContainer(t *testing.T, ts *httptest.Server, cookie, poolID, netID, name string) string {
	t.Helper()
	body := `{"name":"` + name + `","kind":"system-container","image_pin":"alpine/3.21/amd64/default","pool_id":"` + poolID + `","network_id":"` + netID + `"}`
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
	if id == "" {
		t.Fatal("create id")
	}
	return id
}

func TestWorkloadCloneStoresDiskAndNIC(t *testing.T) {
	s, mem, token := testServer(t)
	cluster, _ := mem.GetCluster(context.Background())
	nodeID := uuid.NewString()
	_ = mem.UpsertNode(context.Background(), appdb.Node{ID: nodeID, ClusterID: cluster.ID, Name: "local"})
	poolID, netID := seedCompute(t, mem, cluster.ID, nodeID)
	s.Workloads = &fakeWorkloads{}
	s.Storage = fakeStorage{vol: storage.CreateVolumeResult{Handle: storage.VolumeHandle{
		BackendType: storage.BackendDirectory, BackendRef: "volumes/container-root/x",
		Kind: storage.KindFilesystem, Class: storage.ClassContainerRoot, Format: storage.FormatDirectory,
	}}}
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)
	id := createTestSystemContainer(t, ts, cookie, poolID, netID, "alpine-src")

	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/workloads/"+id+"/clone", strings.NewReader(`{"name":"alpine-copy"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ := ts.Client().Do(req)
	raw, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("clone %d %s", res.StatusCode, raw)
	}
	var cloned map[string]any
	if err := json.Unmarshal(raw, &cloned); err != nil {
		t.Fatal(err)
	}
	cloneID, _ := cloned["id"].(string)
	if cloneID == "" || cloneID == id {
		t.Fatalf("clone id %v", cloned["id"])
	}
	disks, _ := mem.ListWorkloadDisks(context.Background(), cluster.ID, cloneID)
	if len(disks) != 1 || disks[0].Role != "root" {
		t.Fatalf("clone disks %+v", disks)
	}
	nics, _ := mem.ListWorkloadNICs(context.Background(), cluster.ID, cloneID)
	if len(nics) != 1 || nics[0].NetworkID != netID {
		t.Fatalf("clone nics %+v", nics)
	}
}

func TestWorkloadCloneFailsClosedWhenDiskPersistFails(t *testing.T) {
	s, mem, token := testServer(t)
	cluster, _ := mem.GetCluster(context.Background())
	nodeID := uuid.NewString()
	_ = mem.UpsertNode(context.Background(), appdb.Node{ID: nodeID, ClusterID: cluster.ID, Name: "local"})
	poolID, netID := seedCompute(t, mem, cluster.ID, nodeID)
	s.Workloads = &fakeWorkloads{}
	s.Storage = fakeStorage{vol: storage.CreateVolumeResult{Handle: storage.VolumeHandle{
		BackendType: storage.BackendDirectory, BackendRef: "volumes/container-root/x",
		Kind: storage.KindFilesystem, Class: storage.ClassContainerRoot, Format: storage.FormatDirectory,
	}}}
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)
	id := createTestSystemContainer(t, ts, cookie, poolID, netID, "alpine-src")
	s.Store = failCreateWorkloadDiskStore{Store: mem}

	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/workloads/"+id+"/clone", strings.NewReader(`{"name":"alpine-copy"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ := ts.Client().Do(req)
	raw, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusInternalServerError {
		t.Fatalf("disk persist %d %s", res.StatusCode, raw)
	}
	if !strings.Contains(string(raw), "could not record container disk") {
		t.Fatalf("disk persist body %s", raw)
	}
}

func TestWorkloadCloneFailsClosedWhenNICPersistFails(t *testing.T) {
	s, mem, token := testServer(t)
	cluster, _ := mem.GetCluster(context.Background())
	nodeID := uuid.NewString()
	_ = mem.UpsertNode(context.Background(), appdb.Node{ID: nodeID, ClusterID: cluster.ID, Name: "local"})
	poolID, netID := seedCompute(t, mem, cluster.ID, nodeID)
	s.Workloads = &fakeWorkloads{}
	s.Storage = fakeStorage{vol: storage.CreateVolumeResult{Handle: storage.VolumeHandle{
		BackendType: storage.BackendDirectory, BackendRef: "volumes/container-root/x",
		Kind: storage.KindFilesystem, Class: storage.ClassContainerRoot, Format: storage.FormatDirectory,
	}}}
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)
	id := createTestSystemContainer(t, ts, cookie, poolID, netID, "alpine-src")
	s.Store = failCreateWorkloadNICStore{Store: mem}

	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/workloads/"+id+"/clone", strings.NewReader(`{"name":"alpine-copy"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ := ts.Client().Do(req)
	raw, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusInternalServerError {
		t.Fatalf("nic persist %d %s", res.StatusCode, raw)
	}
	if !strings.Contains(string(raw), "could not record container NIC") {
		t.Fatalf("nic persist body %s", raw)
	}
}

func TestWorkloadCloneFailsClosedWhenVolumePersistFails(t *testing.T) {
	s, mem, token := testServer(t)
	cluster, _ := mem.GetCluster(context.Background())
	nodeID := uuid.NewString()
	_ = mem.UpsertNode(context.Background(), appdb.Node{ID: nodeID, ClusterID: cluster.ID, Name: "local"})
	poolID, netID := seedCompute(t, mem, cluster.ID, nodeID)
	s.Workloads = &fakeWorkloads{}
	s.Storage = fakeStorage{vol: storage.CreateVolumeResult{Handle: storage.VolumeHandle{
		BackendType: storage.BackendDirectory, BackendRef: "volumes/container-root/x",
		Kind: storage.KindFilesystem, Class: storage.ClassContainerRoot, Format: storage.FormatDirectory,
	}}}
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)
	id := createTestSystemContainer(t, ts, cookie, poolID, netID, "alpine-src")
	s.Store = failCreateVolumeStore{Store: mem}

	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/workloads/"+id+"/clone", strings.NewReader(`{"name":"alpine-copy"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ := ts.Client().Do(req)
	raw, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusInternalServerError {
		t.Fatalf("volume persist %d %s", res.StatusCode, raw)
	}
	if !strings.Contains(string(raw), "could not record clone volume") {
		t.Fatalf("volume persist body %s", raw)
	}
}
