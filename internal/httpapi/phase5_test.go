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
	failIDs  map[string]error
	creates  int
	vols     []string
	deleted  []string
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
	if f.failIDs != nil {
		if err := f.failIDs[req.WorkloadID]; err != nil {
			return lxc.Result{}, err
		}
	}
	if f.err != nil {
		return lxc.Result{}, f.err
	}
	if req.Action == "delete" {
		f.deleted = append(f.deleted, req.WorkloadID)
	}
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
	return res, nil
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
	if nics[0].MAC != lxc.MACFromUUID(first["id"].(string)) || fw.lastSpec.MAC != nics[0].MAC {
		t.Fatalf("generated mac nic=%s spec=%s", nics[0].MAC, fw.lastSpec.MAC)
	}
	if fw.lastSpec.IP.IPv4Mode != lxc.IPModeDHCP || fw.lastSpec.IP.IPv6Mode != lxc.IPModeDisabled {
		t.Fatalf("default IP %+v", fw.lastSpec.IP)
	}
	if nics[0].IPv4Mode != lxc.IPModeDHCP || nics[0].IPv6Mode != lxc.IPModeDisabled {
		t.Fatalf("stored IP %+v", nics[0])
	}
	vols, _ := mem.ListVolumes(context.Background(), cluster.ID, "")
	if len(vols) != 1 {
		t.Fatalf("second volume created: %d", len(vols))
	}
	if len(fw.vols) > 0 && fw.vols[0] != disks[0].VolumeID {
		t.Fatalf("volume mismatch %s %s", fw.vols[0], disks[0].VolumeID)
	}
}

func TestWorkloadCreateStaticIP(t *testing.T) {
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
	body := `{"name":"static-ct","kind":"system-container","image_pin":"alpine/3.21/amd64/default","pool_id":"` + poolID + `","network_id":"` + netID + `","ipv4_mode":"static","ipv4_address":"10.8.0.20/24","ipv4_gateway":"10.8.0.1","ipv6_mode":"disabled","dns":["1.1.1.1"]}`
	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/workloads", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != http.StatusCreated {
		b, _ := io.ReadAll(res.Body)
		t.Fatalf("create %d %s", res.StatusCode, b)
	}
	var created map[string]any
	_ = json.NewDecoder(res.Body).Decode(&created)
	_ = res.Body.Close()
	if fw.lastSpec.IP.IPv4Mode != lxc.IPModeStatic || fw.lastSpec.IP.IPv4Address != "10.8.0.20/24" || fw.lastSpec.IP.IPv6Mode != lxc.IPModeDisabled {
		t.Fatalf("agent spec %+v", fw.lastSpec.IP)
	}
	nics, _ := mem.ListWorkloadNICs(context.Background(), cluster.ID, created["id"].(string))
	if len(nics) != 1 || nics[0].IPv4Address != "10.8.0.20/24" || nics[0].IPv4Gateway != "10.8.0.1" {
		t.Fatalf("nics %+v", nics)
	}
	patch, _ := http.NewRequest("PATCH", ts.URL+"/api/v1/workloads/"+created["id"].(string), strings.NewReader(`{"ipv6_mode":"dhcp"}`))
	patch.Header.Set("Content-Type", "application/json")
	patch.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	pres, err := ts.Client().Do(patch)
	if err != nil {
		t.Fatal(err)
	}
	if pres.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(pres.Body)
		t.Fatalf("patch %d %s", pres.StatusCode, b)
	}
	_ = pres.Body.Close()
	if fw.lastLife.Action != lxc.ActionApplySpec || !fw.lastLife.IPSet || fw.lastLife.IP.IPv6Mode != lxc.IPModeDHCP || fw.lastLife.IP.IPv4Mode != lxc.IPModeStatic {
		t.Fatalf("patched spec action=%s ipset=%v ip=%+v", fw.lastLife.Action, fw.lastLife.IPSet, fw.lastLife.IP)
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

func TestBulkDeleteWorkloads(t *testing.T) {
	s, mem, token := testServer(t)
	cluster, _ := mem.GetCluster(context.Background())
	nodeID := uuid.NewString()
	_ = mem.UpsertNode(context.Background(), appdb.Node{ID: nodeID, ClusterID: cluster.ID, Name: "local"})
	a := uuid.NewString()
	b := uuid.NewString()
	c := uuid.NewString()
	vmID := uuid.NewString()
	for _, w := range []appdb.Workload{
		{ID: a, ClusterID: cluster.ID, NodeID: nodeID, OwnerNodeID: nodeID, DesiredNodeID: nodeID, Name: "AspecRacing", Kind: lxc.KindSystemContainer, Status: lxc.StatusStopped, ImagePin: "imported"},
		{ID: b, ClusterID: cluster.ID, NodeID: nodeID, OwnerNodeID: nodeID, DesiredNodeID: nodeID, Name: "SoundDock", Kind: lxc.KindSystemContainer, Status: lxc.StatusStopped, ImagePin: "imported"},
		{ID: c, ClusterID: cluster.ID, NodeID: nodeID, OwnerNodeID: nodeID, DesiredNodeID: nodeID, Name: "Failing", Kind: lxc.KindSystemContainer, Status: lxc.StatusStopped, ImagePin: "imported"},
		{ID: vmID, ClusterID: cluster.ID, NodeID: nodeID, OwnerNodeID: nodeID, DesiredNodeID: nodeID, Name: "Ubuntu", Kind: "vm", Status: "stopped"},
	} {
		if err := mem.CreateWorkload(context.Background(), w); err != nil {
			t.Fatal(err)
		}
	}
	fw := &fakeWorkloads{failIDs: map[string]error{c: errors.New("agent refused delete")}}
	s.Workloads = fw
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)

	view := appdb.User{ID: uuid.NewString(), ClusterID: cluster.ID, Username: "view-bulk"}
	_ = mem.CreateUser(context.Background(), view)
	_ = mem.BindRole(context.Background(), cluster.ID, view.ID, rbac.Viewer)
	vtok := "ndl_view_bulk"
	_ = mem.CreateToken(context.Background(), appdb.APIToken{
		ID: uuid.NewString(), ClusterID: cluster.ID, UserID: view.ID, Name: "v",
		TokenHash: secutil.HashSHA256(vtok), Prefix: "ndl_vb",
	})
	denied, _ := http.NewRequest("POST", ts.URL+"/api/v1/workloads/bulk-delete", strings.NewReader(`{"ids":["`+a+`"]}`))
	denied.Header.Set("Content-Type", "application/json")
	denied.Header.Set("Authorization", "Bearer "+vtok)
	dres, err := ts.Client().Do(denied)
	if err != nil {
		t.Fatal(err)
	}
	if dres.StatusCode != http.StatusForbidden {
		b, _ := io.ReadAll(dres.Body)
		t.Fatalf("viewer bulk delete %d %s", dres.StatusCode, b)
	}
	_ = dres.Body.Close()

	life := "ndl_life_bulk"
	admin, err := mem.GetUserByName(context.Background(), cluster.ID, "admin")
	if err != nil || admin == nil {
		t.Fatal("admin")
	}
	if err := mem.CreateToken(context.Background(), appdb.APIToken{
		ID: uuid.NewString(), ClusterID: cluster.ID, UserID: admin.ID, Name: "life",
		TokenHash: secutil.HashSHA256(life), Prefix: "ndl_lb",
		Permissions: []string{rbac.ComputeLifecycle, rbac.ComputeRead},
	}); err != nil {
		t.Fatal(err)
	}
	lifeReq, _ := http.NewRequest("POST", ts.URL+"/api/v1/workloads/bulk-delete", strings.NewReader(`{"ids":["`+a+`"]}`))
	lifeReq.Header.Set("Content-Type", "application/json")
	lifeReq.Header.Set("Authorization", "Bearer "+life)
	lres, err := ts.Client().Do(lifeReq)
	if err != nil {
		t.Fatal(err)
	}
	if lres.StatusCode != http.StatusForbidden {
		raw, _ := io.ReadAll(lres.Body)
		t.Fatalf("lifecycle must not bulk delete %d %s", lres.StatusCode, raw)
	}
	_ = lres.Body.Close()

	body := `{"ids":["` + a + `","` + b + `","` + c + `","` + vmID + `","missing"]}`
	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/workloads/bulk-delete", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("bulk delete %d %s", res.StatusCode, raw)
	}
	var out struct {
		Results []struct {
			ID    string `json:"id"`
			Name  string `json:"name"`
			OK    bool   `json:"ok"`
			Error string `json:"error"`
		} `json:"results"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Results) != 5 {
		t.Fatalf("results %s", raw)
	}
	byID := map[string]struct {
		OK    bool
		Error string
		Name  string
	}{}
	for _, r := range out.Results {
		byID[r.ID] = struct {
			OK    bool
			Error string
			Name  string
		}{r.OK, r.Error, r.Name}
	}
	if !byID[a].OK || byID[a].Name != "AspecRacing" || !byID[b].OK {
		t.Fatalf("success rows %s", raw)
	}
	if byID[c].OK || !strings.Contains(byID[c].Error, "agent refused") {
		t.Fatalf("partial fail %s", raw)
	}
	if byID[vmID].OK || !strings.Contains(byID[vmID].Error, "system containers") {
		t.Fatalf("vm refused %s", raw)
	}
	if byID["missing"].OK || !strings.Contains(byID["missing"].Error, "not found") {
		t.Fatalf("missing %s", raw)
	}
	if gone, _ := mem.GetWorkload(context.Background(), cluster.ID, a); gone != nil {
		t.Fatal("deleted container must leave the catalog")
	}
	if gone, _ := mem.GetWorkload(context.Background(), cluster.ID, b); gone != nil {
		t.Fatal("deleted container must leave the catalog")
	}
	if stay, _ := mem.GetWorkload(context.Background(), cluster.ID, c); stay == nil {
		t.Fatal("failed delete must remain")
	}
	if stay, _ := mem.GetWorkload(context.Background(), cluster.ID, vmID); stay == nil {
		t.Fatal("vm must remain")
	}
}

func TestBulkDeleteWorkloadsEmptyIDs(t *testing.T) {
	s, _, token := testServer(t)
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)
	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/workloads/bulk-delete", strings.NewReader(`{"ids":[]}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != http.StatusBadRequest {
		b, _ := io.ReadAll(res.Body)
		t.Fatalf("empty ids %d %s", res.StatusCode, b)
	}
	_ = res.Body.Close()
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

func TestCTPatchRunningCPUDoesNotCreateOrStart(t *testing.T) {
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
	id := createTestSystemContainer(t, ts, cookie, poolID, netID, "alpine-cpu")
	_ = mem.UpdateWorkloadObserved(context.Background(), appdb.Workload{ID: id, Status: lxc.StatusRunning, UnitActive: true})
	createsAfterCreate := fw.creates
	req, _ := http.NewRequest("PATCH", ts.URL+"/api/v1/workloads/"+id, strings.NewReader(`{"cpus":4}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("patch %d %s", res.StatusCode, raw)
	}
	if fw.creates != createsAfterCreate {
		t.Fatalf("running CPU patch must not CreateCT: %d -> %d", createsAfterCreate, fw.creates)
	}
	if fw.lastLife.Action != lxc.ActionApplySpec {
		t.Fatalf("want apply-spec, got %+v", fw.lastLife)
	}
	if fw.lastLife.CPUs != 4 {
		t.Fatalf("apply cpus %+v", fw.lastLife)
	}
	got, err := mem.GetWorkload(context.Background(), cluster.ID, id)
	if err != nil || got == nil || got.CPUs != 4 {
		t.Fatalf("cpus %+v %v", got, err)
	}
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatal(err)
	}
	apply, _ := body["apply"].([]any)
	if len(apply) == 0 {
		t.Fatalf("apply classes missing: %s", raw)
	}
}

func TestCTPatchDoesNotStartWhenAlreadyRunning(t *testing.T) {
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
	id := createTestSystemContainer(t, ts, cookie, poolID, netID, "alpine-run")
	_ = mem.UpdateWorkloadObserved(context.Background(), appdb.Workload{ID: id, Status: lxc.StatusRunning, UnitActive: true, DesiredPower: "running"})
	fw.lastLife = lxc.LifecycleRequest{}
	req, _ := http.NewRequest("PATCH", ts.URL+"/api/v1/workloads/"+id, strings.NewReader(`{"cpus":2,"desired_power":"running"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ := ts.Client().Do(req)
	raw, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("patch %d %s", res.StatusCode, raw)
	}
	if fw.lastLife.Action == "start" {
		t.Fatal("already-running patch must not start")
	}
	if fw.lastLife.Action != lxc.ActionApplySpec {
		t.Fatalf("last action %q", fw.lastLife.Action)
	}
}

func TestCTPatchNameSetsPendingRestart(t *testing.T) {
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
	id := createTestSystemContainer(t, ts, cookie, poolID, netID, "alpine-host")
	_ = mem.UpdateWorkloadObserved(context.Background(), appdb.Workload{ID: id, Status: lxc.StatusRunning, UnitActive: true, DesiredPower: "running"})
	req, _ := http.NewRequest("PATCH", ts.URL+"/api/v1/workloads/"+id, strings.NewReader(`{"name":"alpine-renamed"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ := ts.Client().Do(req)
	raw, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("patch %d %s", res.StatusCode, raw)
	}
	if fw.lastLife.Action != lxc.ActionApplySpec {
		t.Fatalf("want apply-spec, got %q", fw.lastLife.Action)
	}
	got, err := mem.GetWorkload(context.Background(), cluster.ID, id)
	if err != nil || got == nil || got.Name != "alpine-renamed" {
		t.Fatalf("name %+v %v", got, err)
	}
	if !got.PendingRestart {
		t.Fatal("hostname change while running must set pending_restart")
	}
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatal(err)
	}
	if body["pending_restart"] != true {
		t.Fatalf("pending_restart missing: %s", raw)
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

func TestWorkloadCloneFailsClosedForUnavailableSourceVolume(t *testing.T) {
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
	disks, _ := mem.ListWorkloadDisks(context.Background(), cluster.ID, id)
	if len(disks) != 1 {
		t.Fatalf("source disks %+v", disks)
	}
	if err := mem.UpdateVolumeObserved(context.Background(), appdb.Volume{ID: disks[0].VolumeID, Status: storage.StatusUnavailable}); err != nil {
		t.Fatal(err)
	}
	volsBefore, _ := mem.ListVolumes(context.Background(), cluster.ID, "")
	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/workloads/"+id+"/clone", strings.NewReader(`{"name":"alpine-copy"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ := ts.Client().Do(req)
	raw, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusConflict {
		t.Fatalf("unavailable source volume %d %s", res.StatusCode, raw)
	}
	if !strings.Contains(string(raw), "source volume is unavailable") {
		t.Fatalf("unavailable source volume body %s", raw)
	}
	items, _ := mem.ListWorkloads(context.Background(), cluster.ID)
	if len(items) != 1 || items[0].ID != id {
		t.Fatalf("GET must not list a clone whose source volume apply cannot copy: %+v", items)
	}
	volsAfter, _ := mem.ListVolumes(context.Background(), cluster.ID, "")
	if len(volsAfter) != len(volsBefore) {
		t.Fatalf("clone must not persist a volume apply cannot copy: %d -> %d", len(volsBefore), len(volsAfter))
	}
}

func TestWorkloadCloneFailsClosedForUnavailableSourcePool(t *testing.T) {
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
	if err := mem.UpdateStoragePoolObserved(context.Background(), appdb.StoragePool{ID: poolID, Status: storage.StatusUnavailable}); err != nil {
		t.Fatal(err)
	}
	volsBefore, _ := mem.ListVolumes(context.Background(), cluster.ID, "")
	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/workloads/"+id+"/clone", strings.NewReader(`{"name":"alpine-copy"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ := ts.Client().Do(req)
	raw, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusConflict {
		t.Fatalf("unavailable source pool %d %s", res.StatusCode, raw)
	}
	if !strings.Contains(string(raw), "source pool is unavailable") {
		t.Fatalf("unavailable source pool body %s", raw)
	}
	items, _ := mem.ListWorkloads(context.Background(), cluster.ID)
	if len(items) != 1 || items[0].ID != id {
		t.Fatalf("GET must not list a clone whose source pool apply cannot copy: %+v", items)
	}
	volsAfter, _ := mem.ListVolumes(context.Background(), cluster.ID, "")
	if len(volsAfter) != len(volsBefore) {
		t.Fatalf("clone must not persist a volume apply cannot copy: %d -> %d", len(volsBefore), len(volsAfter))
	}
}

func TestCTStartFailsClosedForUnavailableRootVolume(t *testing.T) {
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
	body := `{"name":"alpine-src","kind":"system-container","image_pin":"alpine/3.21/amd64/default","pool_id":"` + poolID + `","network_id":"` + netID + `","desired_power":"stopped"}`
	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/workloads", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ := ts.Client().Do(req)
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
		t.Fatalf("source disks %+v", disks)
	}
	if err := mem.UpdateVolumeObserved(context.Background(), appdb.Volume{ID: disks[0].VolumeID, Status: storage.StatusUnavailable}); err != nil {
		t.Fatal(err)
	}
	start, _ := http.NewRequest("POST", ts.URL+"/api/v1/workloads/"+id+"/start", strings.NewReader("{}"))
	start.Header.Set("Content-Type", "application/json")
	start.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(start)
	raw, _ = io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusConflict {
		t.Fatalf("unavailable root volume start %d %s", res.StatusCode, raw)
	}
	if !strings.Contains(string(raw), "storage is unavailable") {
		t.Fatalf("unavailable root volume start body %s", raw)
	}
	if fw.lastLife.Action == "start" {
		t.Fatal("start must not call the agent when root storage apply cannot mount")
	}
	wl, _ := mem.GetWorkload(context.Background(), cluster.ID, id)
	if wl == nil || wl.DesiredPower == "running" {
		t.Fatalf("GET must not claim desired_power running when start cannot use storage: %+v", wl)
	}
}

func TestCTStartAllowsThinOvercommitWarningPool(t *testing.T) {
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
	body := `{"name":"alpine-src","kind":"system-container","image_pin":"alpine/3.21/amd64/default","pool_id":"` + poolID + `","network_id":"` + netID + `","desired_power":"stopped"}`
	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/workloads", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ := ts.Client().Do(req)
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
	if err := mem.UpdateStoragePoolObserved(context.Background(), appdb.StoragePool{
		ID: poolID, Status: storage.StatusWarning, Warnings: []string{storage.WarnThinOvercommit},
	}); err != nil {
		t.Fatal(err)
	}
	start, _ := http.NewRequest("POST", ts.URL+"/api/v1/workloads/"+id+"/start", strings.NewReader("{}"))
	start.Header.Set("Content-Type", "application/json")
	start.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(start)
	raw, _ = io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("thin overcommit warning must not block start %d %s", res.StatusCode, raw)
	}
	if fw.lastLife.Action != "start" {
		t.Fatalf("start must reach the agent: %+v", fw.lastLife)
	}
}

func TestCTDeleteDestroysExclusiveRootVolume(t *testing.T) {
	s, mem, token := testServer(t)
	cluster, _ := mem.GetCluster(context.Background())
	nodeID := uuid.NewString()
	_ = mem.UpsertNode(context.Background(), appdb.Node{ID: nodeID, ClusterID: cluster.ID, Name: "local"})
	poolID, netID := seedCompute(t, mem, cluster.ID, nodeID)
	fw := &fakeWorkloads{}
	destroyed := []string{}
	s.Workloads = fw
	s.Storage = fakeStorage{
		vol: storage.CreateVolumeResult{Handle: storage.VolumeHandle{
			BackendType: storage.BackendDirectory, BackendRef: "volumes/container-root/x",
			Kind: storage.KindFilesystem, Class: storage.ClassContainerRoot, Format: storage.FormatDirectory,
		}},
		destroyed: &destroyed,
	}
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)
	body := `{"name":"alpine-del","kind":"system-container","image_pin":"alpine/3.21/amd64/default","pool_id":"` + poolID + `","network_id":"` + netID + `","desired_power":"stopped"}`
	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/workloads", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ := ts.Client().Do(req)
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
	volID := disks[0].VolumeID
	del, _ := http.NewRequest("POST", ts.URL+"/api/v1/workloads/"+id+"/delete", strings.NewReader("{}"))
	del.Header.Set("Content-Type", "application/json")
	del.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(del)
	raw, _ = io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("delete %d %s", res.StatusCode, raw)
	}
	if len(destroyed) != 1 || destroyed[0] != volID {
		t.Fatalf("delete must destroy exclusive container-root: %v want %s", destroyed, volID)
	}
	if got, _ := mem.GetVolume(context.Background(), cluster.ID, volID); got != nil {
		t.Fatal("volume row must be removed")
	}
	if got, _ := mem.GetWorkload(context.Background(), cluster.ID, id); got != nil {
		t.Fatal("workload row must be removed")
	}
}

func TestCTStartFailsClosedForUnavailableNetwork(t *testing.T) {
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
	body := `{"name":"alpine-src","kind":"system-container","image_pin":"alpine/3.21/amd64/default","pool_id":"` + poolID + `","network_id":"` + netID + `","desired_power":"stopped"}`
	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/workloads", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ := ts.Client().Do(req)
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
	if err := mem.UpdateNetworkObserved(context.Background(), appdb.Network{ID: netID, Status: ndnet.StatusUnavailable}); err != nil {
		t.Fatal(err)
	}
	start, _ := http.NewRequest("POST", ts.URL+"/api/v1/workloads/"+id+"/start", strings.NewReader("{}"))
	start.Header.Set("Content-Type", "application/json")
	start.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(start)
	raw, _ = io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusConflict {
		t.Fatalf("unavailable network start %d %s", res.StatusCode, raw)
	}
	if !strings.Contains(string(raw), "an available network is required") {
		t.Fatalf("unavailable network start body %s", raw)
	}
	if fw.lastLife.Action == "start" {
		t.Fatal("start must not call the agent when the NIC apply cannot attach")
	}
	wl, _ := mem.GetWorkload(context.Background(), cluster.ID, id)
	if wl == nil || wl.DesiredPower == "running" {
		t.Fatalf("GET must not claim desired_power running when start cannot use the network: %+v", wl)
	}
}

func TestCTPatchFailsClosedForUnavailableRootVolume(t *testing.T) {
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
	body := `{"name":"alpine-src","kind":"system-container","image_pin":"alpine/3.21/amd64/default","pool_id":"` + poolID + `","network_id":"` + netID + `","desired_power":"stopped"}`
	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/workloads", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ := ts.Client().Do(req)
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
	if created["cpus"] != float64(1) {
		t.Fatalf("create cpus %+v", created["cpus"])
	}
	disks, _ := mem.ListWorkloadDisks(context.Background(), cluster.ID, id)
	if len(disks) != 1 {
		t.Fatalf("source disks %+v", disks)
	}
	createsAfterCreate := fw.creates
	if err := mem.UpdateVolumeObserved(context.Background(), appdb.Volume{ID: disks[0].VolumeID, Status: storage.StatusUnavailable}); err != nil {
		t.Fatal(err)
	}
	patch, _ := http.NewRequest("PATCH", ts.URL+"/api/v1/workloads/"+id, strings.NewReader(`{"cpus":2}`))
	patch.Header.Set("Content-Type", "application/json")
	patch.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(patch)
	raw, _ = io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusConflict {
		t.Fatalf("unavailable root volume patch %d %s", res.StatusCode, raw)
	}
	if !strings.Contains(string(raw), "storage is unavailable") {
		t.Fatalf("unavailable root volume patch body %s", raw)
	}
	if fw.creates != createsAfterCreate {
		t.Fatalf("PATCH must not call CreateCT when root storage apply cannot jail: creates %d -> %d", createsAfterCreate, fw.creates)
	}
	if fw.lastSpec.CPUs != 1 {
		t.Fatalf("CreateCT must not receive patched CPUs when storage apply cannot jail: %+v", fw.lastSpec)
	}
	got, err := mem.GetWorkload(context.Background(), cluster.ID, id)
	if err != nil || got == nil || got.CPUs != 1 {
		t.Fatalf("GET must not claim patched CPUs when apply cannot jail: %+v %v", got, err)
	}
}

func TestCTPatchFailsClosedForEscapingRootVolume(t *testing.T) {
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
	body := `{"name":"alpine-src","kind":"system-container","image_pin":"alpine/3.21/amd64/default","pool_id":"` + poolID + `","network_id":"` + netID + `","desired_power":"stopped"}`
	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/workloads", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ := ts.Client().Do(req)
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
		t.Fatalf("source disks %+v", disks)
	}
	createsAfterCreate := fw.creates
	if err := mem.UpdateVolumeLocator(context.Background(), cluster.ID, disks[0].VolumeID, "../../etc"); err != nil {
		t.Fatal(err)
	}
	patch, _ := http.NewRequest("PATCH", ts.URL+"/api/v1/workloads/"+id, strings.NewReader(`{"cpus":2}`))
	patch.Header.Set("Content-Type", "application/json")
	patch.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(patch)
	raw, _ = io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusConflict {
		t.Fatalf("escaping root volume patch %d %s", res.StatusCode, raw)
	}
	if !strings.Contains(string(raw), "volume locator is invalid") {
		t.Fatalf("escaping root volume patch body %s", raw)
	}
	if fw.creates != createsAfterCreate {
		t.Fatalf("PATCH must not call CreateCT with an escaped rootfs: creates %d -> %d", createsAfterCreate, fw.creates)
	}
	if fw.lastSpec.CPUs != 1 {
		t.Fatalf("CreateCT must not receive patched CPUs when the locator apply cannot join: %+v", fw.lastSpec)
	}
	got, err := mem.GetWorkload(context.Background(), cluster.ID, id)
	if err != nil || got == nil || got.CPUs != 1 {
		t.Fatalf("GET must not claim patched CPUs when the locator apply cannot join: %+v %v", got, err)
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

func postSystemContainerWithVolume(t *testing.T, ts *httptest.Server, cookie, poolID, netID, volID, name string) (int, []byte) {
	t.Helper()
	body := `{"name":"` + name + `","kind":"system-container","image_pin":"alpine/3.21/amd64/default","pool_id":"` + poolID + `","network_id":"` + netID + `","volume_id":"` + volID + `"}`
	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/workloads", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	return res.StatusCode, raw
}

func TestCTCreateFailsClosedForUnavailableRootVolume(t *testing.T) {
	s, mem, token := testServer(t)
	cluster, _ := mem.GetCluster(context.Background())
	nodeID := uuid.NewString()
	_ = mem.UpsertNode(context.Background(), appdb.Node{ID: nodeID, ClusterID: cluster.ID, Name: "local"})
	poolID, netID := seedCompute(t, mem, cluster.ID, nodeID)
	volID := uuid.NewString()
	if err := mem.CreateVolume(context.Background(), appdb.Volume{
		ID: volID, ClusterID: cluster.ID, NodeID: nodeID, PoolID: poolID,
		Class: storage.ClassContainerRoot, Kind: storage.KindFilesystem, Format: storage.FormatDirectory,
		Status: storage.StatusUnavailable, BackendType: storage.BackendDirectory,
		BackendRef: "volumes/container-root/" + volID,
	}); err != nil {
		t.Fatal(err)
	}
	fw := &fakeWorkloads{}
	s.Workloads = fw
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)
	code, raw := postSystemContainerWithVolume(t, ts, cookie, poolID, netID, volID, "alpine-offline")
	if code != http.StatusConflict {
		t.Fatalf("unavailable root volume %d %s", code, raw)
	}
	if !strings.Contains(string(raw), "storage is unavailable") {
		t.Fatalf("unavailable root volume body %s", raw)
	}
	if fw.creates != 0 {
		t.Fatal("CreateCT must not run for an unavailable root volume")
	}
	items, _ := mem.ListWorkloads(context.Background(), cluster.ID)
	if len(items) != 0 {
		t.Fatalf("GET must not list a container whose root volume apply cannot mount: %+v", items)
	}
}

func TestCTCreateFailsClosedForUnavailableRootPool(t *testing.T) {
	s, mem, token := testServer(t)
	cluster, _ := mem.GetCluster(context.Background())
	nodeID := uuid.NewString()
	_ = mem.UpsertNode(context.Background(), appdb.Node{ID: nodeID, ClusterID: cluster.ID, Name: "local"})
	poolID, netID := seedCompute(t, mem, cluster.ID, nodeID)
	offlinePool := uuid.NewString()
	if err := mem.CreateStoragePool(context.Background(), appdb.StoragePool{
		ID: offlinePool, ClusterID: cluster.ID, NodeID: nodeID, Name: "ct-offline",
		BackendType: storage.BackendDirectory, Status: storage.StatusUnavailable,
		RootPath: "/var/lib/ndl/storage/ct-offline",
	}); err != nil {
		t.Fatal(err)
	}
	volID := uuid.NewString()
	if err := mem.CreateVolume(context.Background(), appdb.Volume{
		ID: volID, ClusterID: cluster.ID, NodeID: nodeID, PoolID: offlinePool,
		Class: storage.ClassContainerRoot, Kind: storage.KindFilesystem, Format: storage.FormatDirectory,
		Status: storage.StatusAvailable, BackendType: storage.BackendDirectory,
		BackendRef: "volumes/container-root/" + volID,
	}); err != nil {
		t.Fatal(err)
	}
	fw := &fakeWorkloads{}
	s.Workloads = fw
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)
	code, raw := postSystemContainerWithVolume(t, ts, cookie, poolID, netID, volID, "alpine-pool-offline")
	if code != http.StatusConflict {
		t.Fatalf("unavailable root pool %d %s", code, raw)
	}
	if !strings.Contains(string(raw), "storage is unavailable") {
		t.Fatalf("unavailable root pool body %s", raw)
	}
	if fw.creates != 0 {
		t.Fatal("CreateCT must not run for an unavailable root pool")
	}
	items, _ := mem.ListWorkloads(context.Background(), cluster.ID)
	if len(items) != 0 {
		t.Fatalf("GET must not list a container whose root pool apply cannot mount: %+v", items)
	}
}

func TestCTCreateFailsClosedForNonContainerRootVolume(t *testing.T) {
	s, mem, token := testServer(t)
	cluster, _ := mem.GetCluster(context.Background())
	nodeID := uuid.NewString()
	_ = mem.UpsertNode(context.Background(), appdb.Node{ID: nodeID, ClusterID: cluster.ID, Name: "local"})
	poolID, netID := seedCompute(t, mem, cluster.ID, nodeID)
	volID := uuid.NewString()
	if err := mem.CreateVolume(context.Background(), appdb.Volume{
		ID: volID, ClusterID: cluster.ID, NodeID: nodeID, PoolID: poolID,
		Class: storage.ClassVMDisk, Kind: storage.KindBlock, Format: storage.FormatQCOW2,
		Status: storage.StatusAvailable, BackendType: storage.BackendDirectory,
		BackendRef: "volumes/vm-disk/" + volID + ".qcow2", SizeBytes: 1 << 30,
	}); err != nil {
		t.Fatal(err)
	}
	fw := &fakeWorkloads{}
	s.Workloads = fw
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)
	code, raw := postSystemContainerWithVolume(t, ts, cookie, poolID, netID, volID, "alpine-vmdisk")
	if code != http.StatusConflict {
		t.Fatalf("non container-root volume %d %s", code, raw)
	}
	if !strings.Contains(string(raw), "volume is not a container-root") {
		t.Fatalf("non container-root volume body %s", raw)
	}
	if fw.creates != 0 {
		t.Fatal("CreateCT must not run for a vm-disk root")
	}
	items, _ := mem.ListWorkloads(context.Background(), cluster.ID)
	if len(items) != 0 {
		t.Fatalf("GET must not list a container whose root volume apply cannot mount: %+v", items)
	}
}

func TestCTCreateJoinsExistingRootUnderVolumePool(t *testing.T) {
	s, mem, token := testServer(t)
	cluster, _ := mem.GetCluster(context.Background())
	nodeID := uuid.NewString()
	_ = mem.UpsertNode(context.Background(), appdb.Node{ID: nodeID, ClusterID: cluster.ID, Name: "local"})
	poolID, netID := seedCompute(t, mem, cluster.ID, nodeID)
	extraPool := uuid.NewString()
	if err := mem.CreateStoragePool(context.Background(), appdb.StoragePool{
		ID: extraPool, ClusterID: cluster.ID, NodeID: nodeID, Name: "ct-extra",
		BackendType: storage.BackendDirectory, Status: storage.StatusAvailable,
		RootPath: "/var/lib/ndl/storage/ct-extra",
	}); err != nil {
		t.Fatal(err)
	}
	volID := uuid.NewString()
	if err := mem.CreateVolume(context.Background(), appdb.Volume{
		ID: volID, ClusterID: cluster.ID, NodeID: nodeID, PoolID: extraPool,
		Class: storage.ClassContainerRoot, Kind: storage.KindFilesystem, Format: storage.FormatDirectory,
		Status: storage.StatusAvailable, BackendType: storage.BackendDirectory,
		BackendRef: "volumes/container-root/" + volID,
	}); err != nil {
		t.Fatal(err)
	}
	fw := &fakeWorkloads{}
	s.Workloads = fw
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)
	code, raw := postSystemContainerWithVolume(t, ts, cookie, poolID, netID, volID, "alpine-extra-pool")
	if code != http.StatusCreated {
		t.Fatalf("existing root volume %d %s", code, raw)
	}
	if fw.creates != 1 {
		t.Fatalf("CreateCT calls %d", fw.creates)
	}
	if !strings.Contains(fw.lastSpec.RootfsPath, "/var/lib/ndl/storage/ct-extra/") {
		t.Fatalf("CreateCT must join under the volume pool, not the request pool: %s", fw.lastSpec.RootfsPath)
	}
	if strings.Contains(fw.lastSpec.RootfsPath, "/var/lib/ndl/storage/local/") {
		t.Fatalf("CreateCT must not join the request pool root: %s", fw.lastSpec.RootfsPath)
	}
	if fw.lastSpec.VolumeID != volID {
		t.Fatalf("CreateCT volume_id %s", fw.lastSpec.VolumeID)
	}
	var created map[string]any
	if err := json.Unmarshal(raw, &created); err != nil {
		t.Fatal(err)
	}
	id, _ := created["id"].(string)
	disks, _ := mem.ListWorkloadDisks(context.Background(), cluster.ID, id)
	if len(disks) != 1 || disks[0].VolumeID != volID {
		t.Fatalf("GET disks must list the existing root volume: %+v", disks)
	}
}

func TestWorkloadCreateDefaultDiskAndMemory(t *testing.T) {
	s, mem, token := testServer(t)
	cluster, _ := mem.GetCluster(context.Background())
	nodeID := uuid.NewString()
	_ = mem.UpsertNode(context.Background(), appdb.Node{ID: nodeID, ClusterID: cluster.ID, Name: "local"})
	poolID, netID := seedCompute(t, mem, cluster.ID, nodeID)
	var size int64
	s.Workloads = &fakeWorkloads{}
	s.Storage = fakeStorage{
		vol: storage.CreateVolumeResult{Handle: storage.VolumeHandle{
			BackendType: storage.BackendDirectory, BackendRef: "volumes/container-root/x",
			Kind: storage.KindFilesystem, Class: storage.ClassContainerRoot, Format: storage.FormatDirectory,
		}},
		lastSize: &size,
	}
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)
	body := `{"name":"disk-default","kind":"system-container","image_pin":"alpine/3.21/amd64/default","pool_id":"` + poolID + `","network_id":"` + netID + `"}`
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
	if size != lxc.DefaultRootSize {
		t.Fatalf("default disk %d", size)
	}
	var created map[string]any
	if err := json.Unmarshal(raw, &created); err != nil {
		t.Fatal(err)
	}
	if created["disk_bytes"] != float64(lxc.DefaultRootSize) {
		t.Fatalf("disk_bytes %v", created["disk_bytes"])
	}
	if created["memory_bytes"] != float64(lxc.DefaultMemoryBytes) {
		t.Fatalf("memory_bytes %v", created["memory_bytes"])
	}
	ops, _ := mem.ListOperations(context.Background(), cluster.ID, 20)
	found := false
	for _, op := range ops {
		if op.Kind == "workload.create" && op.State == "succeeded" && op.Progress != nil && *op.Progress == 100 {
			found = true
		}
	}
	if !found {
		t.Fatalf("create task must reach succeeded: %+v", ops)
	}
}

func TestWorkloadCreatePersistsTarErrorAndCleansVolume(t *testing.T) {
	s, mem, token := testServer(t)
	cluster, _ := mem.GetCluster(context.Background())
	nodeID := uuid.NewString()
	_ = mem.UpsertNode(context.Background(), appdb.Node{ID: nodeID, ClusterID: cluster.ID, Name: "local"})
	poolID, netID := seedCompute(t, mem, cluster.ID, nodeID)
	destroyed := []string{}
	s.Workloads = &fakeWorkloads{err: errors.New("failed_precondition: exit status 2: /usr/bin/tar: ./var/lib/apt/lists/auxfiles: Cannot change mode")}
	s.Storage = fakeStorage{
		vol: storage.CreateVolumeResult{Handle: storage.VolumeHandle{
			BackendType: storage.BackendDirectory, BackendRef: "volumes/container-root/x",
			Kind: storage.KindFilesystem, Class: storage.ClassContainerRoot, Format: storage.FormatDirectory,
		}},
		destroyed: &destroyed,
	}
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)
	body := `{"name":"debian-fail","kind":"system-container","image_pin":"debian/trixie/amd64/default","pool_id":"` + poolID + `","network_id":"` + netID + `","disk_bytes":8589934592}`
	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/workloads", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", "retry-debian")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode == http.StatusCreated {
		t.Fatalf("create must fail: %s", raw)
	}
	if !strings.Contains(string(raw), "Cannot change mode") {
		t.Fatalf("api must return tar error: %s", raw)
	}
	ops, _ := mem.ListOperations(context.Background(), cluster.ID, 20)
	var failed *appdb.Operation
	for i := range ops {
		if ops[i].Kind == "workload.create" && ops[i].State == "failed" {
			failed = &ops[i]
		}
	}
	if failed == nil || !strings.Contains(failed.Message, "Cannot change mode") {
		t.Fatalf("failed task must persist tar error: %+v", ops)
	}
	if !strings.Contains(failed.Message, `"workload_id"`) {
		t.Fatal("failed task must keep create ids for retry")
	}
	if len(destroyed) != 1 {
		t.Fatalf("failed create must destroy the dest volume: %v", destroyed)
	}
	vols, _ := mem.ListVolumes(context.Background(), cluster.ID, "")
	if len(vols) != 0 {
		t.Fatalf("volume row must be removed: %+v", vols)
	}
	req2, _ := http.NewRequest("POST", ts.URL+"/api/v1/workloads", strings.NewReader(body))
	req2.Header.Set("Content-Type", "application/json")
	req2.Header.Set("Idempotency-Key", "retry-debian")
	req2.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	s.Workloads = &fakeWorkloads{}
	res2, err := ts.Client().Do(req2)
	if err != nil {
		t.Fatal(err)
	}
	if res2.StatusCode != http.StatusCreated {
		b, _ := io.ReadAll(res2.Body)
		t.Fatalf("retry after cleanup %d %s", res2.StatusCode, b)
	}
	_ = res2.Body.Close()
}

func TestWorkloadCreateRejectsTinyDisk(t *testing.T) {
	s, mem, token := testServer(t)
	cluster, _ := mem.GetCluster(context.Background())
	nodeID := uuid.NewString()
	_ = mem.UpsertNode(context.Background(), appdb.Node{ID: nodeID, ClusterID: cluster.ID, Name: "local"})
	poolID, netID := seedCompute(t, mem, cluster.ID, nodeID)
	s.Workloads = &fakeWorkloads{}
	s.Storage = fakeStorage{vol: storage.CreateVolumeResult{Handle: storage.VolumeHandle{
		BackendType: storage.BackendDirectory, BackendRef: "volumes/container-root/x",
		Class: storage.ClassContainerRoot, Format: storage.FormatDirectory,
	}}}
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)
	body := `{"name":"tiny","kind":"system-container","image_pin":"alpine/3.21/amd64/default","pool_id":"` + poolID + `","network_id":"` + netID + `","disk_bytes":1048576}`
	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/workloads", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode == http.StatusCreated {
		t.Fatal("1 MiB disk must be rejected")
	}
	_ = res.Body.Close()
}

func seedCTServer(t *testing.T) (*Server, *appdb.Memory, string, string, string, *fakeWorkloads) {
	t.Helper()
	s, mem, token := testServer(t)
	cluster, _ := mem.GetCluster(context.Background())
	nodeID := uuid.NewString()
	_ = mem.UpsertNode(context.Background(), appdb.Node{ID: nodeID, ClusterID: cluster.ID, Name: "local"})
	poolID, netID := seedCompute(t, mem, cluster.ID, nodeID)
	fw := &fakeWorkloads{}
	s.Workloads = fw
	s.Storage = fakeStorage{vol: storage.CreateVolumeResult{Handle: storage.VolumeHandle{
		BackendType: storage.BackendDirectory, BackendRef: "volumes/container-root/x",
		Class: storage.ClassContainerRoot, Format: storage.FormatDirectory,
	}}}
	return s, mem, token, poolID, netID, fw
}

func postCT(t *testing.T, ts *httptest.Server, cookie, body string) (int, []byte) {
	t.Helper()
	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/workloads", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	return res.StatusCode, raw
}

func TestWorkloadCreateExplicitMAC(t *testing.T) {
	s, mem, token, poolID, netID, fw := seedCTServer(t)
	cluster, _ := mem.GetCluster(context.Background())
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)
	want := "bc:24:11:00:00:aa"
	body := `{"name":"keep-mac","kind":"system-container","image_pin":"alpine/3.21/amd64/default","pool_id":"` + poolID + `","network_id":"` + netID + `","mac":"BC:24:11:00:00:AA"}`
	code, raw := postCT(t, ts, cookie, body)
	if code != http.StatusCreated {
		t.Fatalf("create %d %s", code, raw)
	}
	var created map[string]any
	if err := json.Unmarshal(raw, &created); err != nil {
		t.Fatal(err)
	}
	if created["mac"] != want {
		t.Fatalf("response mac %v", created["mac"])
	}
	if spec, _ := created["spec"].(map[string]any); spec["schema_version"] == "ndl.vm.spec.v1" {
		t.Fatalf("system-container must not serialize VM defaults: %v", created["spec"])
	}
	if fw.lastSpec.MAC != want {
		t.Fatalf("agent spec mac %s", fw.lastSpec.MAC)
	}
	nics, _ := mem.ListWorkloadNICs(context.Background(), cluster.ID, created["id"].(string))
	if len(nics) != 1 || nics[0].MAC != want {
		t.Fatalf("stored nic %+v", nics)
	}
}

func TestWorkloadCreateRejectsInvalidAndDuplicateMAC(t *testing.T) {
	s, _, token, poolID, netID, _ := seedCTServer(t)
	var volCreates int
	s.Storage = fakeStorage{
		vol: storage.CreateVolumeResult{Handle: storage.VolumeHandle{
			BackendType: storage.BackendDirectory, BackendRef: "volumes/container-root/x",
			Class: storage.ClassContainerRoot, Format: storage.FormatDirectory,
		}},
		volCreates: &volCreates,
	}
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)
	bad := `{"name":"bad-mac","kind":"system-container","image_pin":"alpine/3.21/amd64/default","pool_id":"` + poolID + `","network_id":"` + netID + `","mac":"not-a-mac"}`
	code, raw := postCT(t, ts, cookie, bad)
	if code != http.StatusBadRequest || !strings.Contains(string(raw), "mac must") {
		t.Fatalf("invalid mac must fail: %d %s", code, raw)
	}
	if volCreates != 0 {
		t.Fatalf("invalid mac must not allocate storage: %d", volCreates)
	}
	ok := `{"name":"first-mac","kind":"system-container","image_pin":"alpine/3.21/amd64/default","pool_id":"` + poolID + `","network_id":"` + netID + `","mac":"bc:24:11:00:00:01"}`
	code, raw = postCT(t, ts, cookie, ok)
	if code != http.StatusCreated {
		t.Fatalf("first %d %s", code, raw)
	}
	if volCreates != 1 {
		t.Fatalf("first create volumes %d", volCreates)
	}
	dup := `{"name":"dup-mac","kind":"system-container","image_pin":"alpine/3.21/amd64/default","pool_id":"` + poolID + `","network_id":"` + netID + `","mac":"BC-24-11-00-00-01"}`
	code, raw = postCT(t, ts, cookie, dup)
	if code == http.StatusCreated {
		t.Fatalf("duplicate mac must fail: %s", raw)
	}
	if !strings.Contains(string(raw), "already used") {
		t.Fatalf("duplicate message %s", raw)
	}
	if volCreates != 1 {
		t.Fatalf("duplicate mac must not allocate another volume: %d", volCreates)
	}
}

func TestWorkloadCreatePersistsVolumeMountError(t *testing.T) {
	s, mem, token, poolID, netID, _ := seedCTServer(t)
	s.Storage = fakeStorage{err: errors.New("failed_precondition: exit status 32: mount: failed to setup loop device")}
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)
	body := `{"name":"loop-fail","kind":"system-container","image_pin":"debian/trixie/amd64/default","pool_id":"` + poolID + `","network_id":"` + netID + `"}`
	code, raw := postCT(t, ts, cookie, body)
	if code == http.StatusCreated {
		t.Fatalf("volume mount failure must fail create: %s", raw)
	}
	if !strings.Contains(string(raw), "loop device") {
		t.Fatalf("api must return mount error: %s", raw)
	}
	cluster, _ := mem.GetCluster(context.Background())
	ops, _ := mem.ListOperations(context.Background(), cluster.ID, 20)
	var failed *appdb.Operation
	for i := range ops {
		if ops[i].Kind == "workload.create" && ops[i].State == "failed" {
			failed = &ops[i]
		}
	}
	if failed == nil || !strings.Contains(failed.Message, "loop device") {
		t.Fatalf("failed task must persist mount error: %+v", ops)
	}
}

func TestWorkloadPatchMACRequiresStopAndPersists(t *testing.T) {
	s, mem, token, poolID, netID, fw := seedCTServer(t)
	cluster, _ := mem.GetCluster(context.Background())
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)
	body := `{"name":"mac-edit","kind":"system-container","image_pin":"alpine/3.21/amd64/default","pool_id":"` + poolID + `","network_id":"` + netID + `","desired_power":"stopped"}`
	code, raw := postCT(t, ts, cookie, body)
	if code != http.StatusCreated {
		t.Fatalf("create %d %s", code, raw)
	}
	var created map[string]any
	if err := json.Unmarshal(raw, &created); err != nil {
		t.Fatal(err)
	}
	id := created["id"].(string)
	row, _ := mem.GetWorkload(context.Background(), cluster.ID, id)
	if row == nil {
		t.Fatal("missing workload")
	}
	row.Status = lxc.StatusRunning
	if err := mem.UpdateWorkloadObserved(context.Background(), *row); err != nil {
		t.Fatal(err)
	}
	patch, _ := http.NewRequest("PATCH", ts.URL+"/api/v1/workloads/"+id, strings.NewReader(`{"mac":"bc:24:11:00:00:22"}`))
	patch.Header.Set("Content-Type", "application/json")
	patch.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	pres, err := ts.Client().Do(patch)
	if err != nil {
		t.Fatal(err)
	}
	if pres.StatusCode == http.StatusOK {
		t.Fatal("running container must refuse MAC change")
	}
	_ = pres.Body.Close()
	row.Status = lxc.StatusStopped
	if err := mem.UpdateWorkloadObserved(context.Background(), *row); err != nil {
		t.Fatal(err)
	}
	patch2, _ := http.NewRequest("PATCH", ts.URL+"/api/v1/workloads/"+id, strings.NewReader(`{"mac":"bc:24:11:00:00:22"}`))
	patch2.Header.Set("Content-Type", "application/json")
	patch2.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	pres2, err := ts.Client().Do(patch2)
	if err != nil {
		t.Fatal(err)
	}
	if pres2.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(pres2.Body)
		t.Fatalf("stopped patch %d %s", pres2.StatusCode, b)
	}
	_ = pres2.Body.Close()
	if fw.lastLife.MAC != "bc:24:11:00:00:22" {
		t.Fatalf("applied mac action=%s mac=%s", fw.lastLife.Action, fw.lastLife.MAC)
	}
	nics, _ := mem.ListWorkloadNICs(context.Background(), cluster.ID, id)
	if len(nics) != 1 || nics[0].MAC != "bc:24:11:00:00:22" {
		t.Fatalf("persisted nic %+v", nics)
	}
	patch3, _ := http.NewRequest("PATCH", ts.URL+"/api/v1/workloads/"+id, strings.NewReader(`{"mac":"bc:24:11:00:00:22"}`))
	patch3.Header.Set("Content-Type", "application/json")
	patch3.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	row.Status = lxc.StatusRunning
	_ = mem.UpdateWorkloadObserved(context.Background(), *row)
	pres3, err := ts.Client().Do(patch3)
	if err != nil {
		t.Fatal(err)
	}
	if pres3.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(pres3.Body)
		t.Fatalf("same mac while running must be allowed %d %s", pres3.StatusCode, b)
	}
	_ = pres3.Body.Close()
}

func TestResolveRootDiskAllowsSparseOvercommit(t *testing.T) {
	usable := int64(4 << 30)
	pool := &appdb.StoragePool{UsableBytes: &usable, Status: storage.StatusWarning}
	size, err := resolveRootDiskBytes(createWorkloadRequest{DiskBytes: 50 << 30}, pool)
	if err != nil {
		t.Fatal(err)
	}
	if size != 50<<30 {
		t.Fatalf("size %d", size)
	}
}

func TestResolveRootDiskRejectsPhysicallyExhausted(t *testing.T) {
	usable := int64(4096)
	pool := &appdb.StoragePool{UsableBytes: &usable, Status: storage.StatusWarning}
	if _, err := resolveRootDiskBytes(createWorkloadRequest{DiskBytes: 8 << 30}, pool); err == nil {
		t.Fatal("expected physical free-space error")
	}
}
