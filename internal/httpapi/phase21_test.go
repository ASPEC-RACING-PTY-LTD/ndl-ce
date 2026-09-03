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
	"github.com/no-dal/ndl-ce/internal/gpu"
	"github.com/no-dal/ndl-ce/internal/inventory"
	"github.com/no-dal/ndl-ce/internal/ndnet"
	"github.com/no-dal/ndl-ce/internal/oci"
	"github.com/no-dal/ndl-ce/internal/rbac"
	"github.com/no-dal/ndl-ce/internal/storage"
	"time"
)

type fakeOCI struct {
	lastSpec oci.Spec
	lastLife oci.LifecycleRequest
	runtime  *oci.FakeRuntime
	err      error
}

func (f *fakeOCI) CreateOCI(_ context.Context, spec oci.Spec) (oci.Result, error) {
	f.lastSpec = spec
	if f.err != nil {
		return oci.Result{}, f.err
	}
	rt := f.runtime
	if rt == nil {
		rt = &oci.FakeRuntime{}
		f.runtime = rt
	}
	digest, err := rt.Pull(context.Background(), oci.PullRequest{
		Image: spec.ImagePin,
		Creds: &oci.RegistryCreds{Username: spec.PullUsername, Password: spec.PullPassword},
	})
	if err != nil {
		return oci.Result{}, err
	}
	_ = rt.Run(context.Background(), spec)
	health := oci.Health{Status: oci.StatusNotConfigured, Message: "healthcheck not configured"}
	if spec.Health != nil && (spec.Health.HTTPPath != "" || spec.Health.Port > 0) {
		health = oci.Health{Status: oci.StatusCollecting, Message: "healthcheck configured; awaiting observation"}
	}
	return oci.Result{WorkloadID: spec.WorkloadID, ImageDigest: digest, Status: oci.StatusRunning, Health: health}, nil
}

func (f *fakeOCI) LifecycleOCI(_ context.Context, req oci.LifecycleRequest) (oci.Result, error) {
	f.lastLife = req
	if f.err != nil {
		return oci.Result{}, f.err
	}
	return oci.Result{WorkloadID: req.WorkloadID, Status: oci.StatusStopped, Health: oci.Health{Status: oci.StatusStopped}}, nil
}

func phase21Ready(t *testing.T) (*Server, *appdb.Memory, *httptest.Server, string, string, *fakeOCI) {
	t.Helper()
	s, mem, token := testServer(t)
	cluster, _ := mem.GetCluster(context.Background())
	nodeID := uuid.NewString()
	_ = mem.UpsertNode(context.Background(), appdb.Node{ID: nodeID, ClusterID: cluster.ID, Name: "local"})
	_, _ = seedCompute(t, mem, cluster.ID, nodeID)
	fo := &fakeOCI{runtime: &oci.FakeRuntime{}}
	s.OCI = fo
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close)
	cookie := claimAdmin(t, ts, token)
	return s, mem, ts, cookie, cluster.ID, fo
}

func TestPhase21RegistryNeverReturnsPassword(t *testing.T) {
	_, _, ts, cookie, _, _ := phase21Ready(t)
	body := `{"name":"private","url":"https://registry.example","username":"u","password":"s3cret"}`
	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/registries", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("%d %s", res.StatusCode, raw)
	}
	if strings.Contains(string(raw), "s3cret") || strings.Contains(string(raw), `"password"`) {
		t.Fatalf("password leaked: %s", raw)
	}
	var created map[string]any
	_ = json.Unmarshal(raw, &created)
	if created["has_credentials"] != true {
		t.Fatalf("has_credentials: %+v", created)
	}
	list, err := ts.Client().Do(func() *http.Request {
		r, _ := http.NewRequest("GET", ts.URL+"/api/v1/registries", nil)
		r.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
		return r
	}())
	if err != nil {
		t.Fatal(err)
	}
	defer list.Body.Close()
	lb, _ := io.ReadAll(list.Body)
	if strings.Contains(string(lb), "s3cret") {
		t.Fatal("list leaked password")
	}
}

func TestPhase21RegistryURLRefusesCredentials(t *testing.T) {
	_, _, ts, cookie, _, _ := phase21Ready(t)
	body := `{"name":"private","url":"https://u:s3cret@registry.example","username":"u","password":"s3cret"}`
	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/registries", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("status %d %s", res.StatusCode, raw)
	}
	if strings.Contains(string(raw), "s3cret") {
		t.Fatalf("secret echoed %s", raw)
	}
}

func TestPhase21OCICreatePullsWithCredsAndShowsHealth(t *testing.T) {
	_, mem, ts, cookie, clusterID, fo := phase21Ready(t)
	regID := uuid.NewString()
	_ = mem.CreateRegistry(context.Background(), appdb.Registry{
		ID: regID, ClusterID: clusterID, Name: "priv", URL: "https://registry.example",
		HasCredentials: true, Status: appdb.RegistryConfigured,
	}, "user", "pass")

	body := `{"name":"app","kind":"oci","image_pin":"registry.example/app:1","registry_id":"` + regID + `","health":{"http_path":"/healthz","port":8080}}`
	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/workloads", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("create %d %s", res.StatusCode, raw)
	}
	if fo.lastSpec.PullUsername != "user" || fo.lastSpec.PullPassword != "pass" {
		t.Fatalf("creds not passed to runtime: %+v", fo.lastSpec)
	}
	creds := fo.runtime.LastPullCreds("registry.example/app:1")
	if creds == nil || creds.Password != "pass" {
		t.Fatal("fake runtime did not pull with creds")
	}
	var created map[string]any
	_ = json.Unmarshal(raw, &created)
	if created["kind"] != oci.KindOCI {
		t.Fatalf("kind %v", created["kind"])
	}
	health, _ := created["health"].(map[string]any)
	if health == nil || health["status"] != oci.StatusCollecting {
		t.Fatalf("health %+v", created["health"])
	}
	if strings.Contains(string(raw), "pass") && strings.Contains(string(raw), "pull_password") {
		t.Fatal("pull password leaked in workload JSON")
	}
	unit, _ := created["unit"].(string)
	if !strings.HasPrefix(unit, "nodal-oci@") {
		t.Fatalf("unit %s", unit)
	}
}

func TestPhase21RejectHostBindRootAndPrivilegedDefault(t *testing.T) {
	_, _, ts, cookie, _, _ := phase21Ready(t)
	body := `{"name":"bad","kind":"oci","image_pin":"busybox:1","host_path":"/"}`
	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/workloads", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode == http.StatusCreated {
		t.Fatal("must reject host_path /")
	}

	hash, _ := auth.HashPassword("password1")
	s, mem, _ := testServer(t)
	cluster, _ := mem.GetCluster(context.Background())
	nodeID := uuid.NewString()
	_ = mem.UpsertNode(context.Background(), appdb.Node{ID: nodeID, ClusterID: cluster.ID, Name: "local"})
	opID := uuid.NewString()
	_ = mem.CreateUser(context.Background(), appdb.User{ID: opID, ClusterID: cluster.ID, Username: "op", PasswordHash: hash})
	_ = mem.BindRole(context.Background(), cluster.ID, opID, rbac.Operator)
	s.OCI = &fakeOCI{runtime: &oci.FakeRuntime{}}
	ts2 := httptest.NewServer(s.Handler())
	t.Cleanup(ts2.Close)
	login, err := ts2.Client().Post(ts2.URL+"/api/v1/auth/login", "application/json", strings.NewReader(`{"username":"op","password":"password1"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer login.Body.Close()
	var opCookie string
	for _, c := range login.Cookies() {
		if c.Name == sessionCookie {
			opCookie = c.Value
		}
	}
	req, _ = http.NewRequest("POST", ts2.URL+"/api/v1/workloads", strings.NewReader(`{"name":"priv","kind":"oci","image_pin":"busybox:1","privileged":true}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: opCookie})
	res, err = ts2.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusForbidden {
		b, _ := io.ReadAll(res.Body)
		t.Fatalf("operator privileged %d %s", res.StatusCode, b)
	}
}

func TestPhase21GPUAssignOCIRender(t *testing.T) {
	s, mem, ts, cookie, clusterID, _ := phase21Ready(t)
	node, _ := mem.GetNode(context.Background(), clusterID)
	inv := inventory.Inventory{
		SchemaVersion: inventory.SchemaVersion, ObservedAt: time.Now().UTC(),
		GPUs: []inventory.GPU{{ID: "0000:02:00.0", PCI: "0000:02:00.0", Vendor: "NVIDIA", IOMMUGroup: "12", Hint: "/dev/dri/by-path/pci-0000:02:00.0-render"}},
		IOMMU: inventory.IOMMU{Status: inventory.StatusAvailable, Groups: []inventory.IOMMUGroup{
			{ID: "12", Devices: []string{"0000:02:00.0"}},
		}},
	}
	body, _ := json.Marshal(inv)
	_ = mem.UpsertInventory(context.Background(), appdb.HardwareInventory{
		NodeID: node.ID, ClusterID: clusterID, Payload: body, ObservedAt: time.Now().UTC(),
	})
	s.GPU = gpuStub{}
	wl := appdb.Workload{ID: uuid.NewString(), ClusterID: clusterID, Name: "oci-gpu", Kind: oci.KindOCI, Status: oci.StatusStopped}
	_ = mem.CreateWorkload(context.Background(), wl)

	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/gpus/assign", strings.NewReader(`{"gpu_id":"0000:02:00.0","workload_id":"`+wl.ID+`","mode":"render"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusCreated {
		b, _ := io.ReadAll(res.Body)
		t.Fatalf("assign %d %s", res.StatusCode, b)
	}

	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/gpus/assign", strings.NewReader(`{"gpu_id":"0000:02:00.0","workload_id":"`+wl.ID+`","mode":"vfio"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, err = ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode == http.StatusCreated {
		t.Fatal("VFIO must remain VM-only")
	}
}

func TestPhase21OCIWiresPortsResourcesAndBridge(t *testing.T) {
	_, mem, ts, cookie, clusterID, fo := phase21Ready(t)
	nets, err := mem.ListNetworks(context.Background(), clusterID)
	if err != nil || len(nets) == 0 {
		t.Fatalf("network fixture: %v %d", err, len(nets))
	}
	body := `{"name":"web","kind":"oci","image_pin":"busybox:1","cpus":2,"memory_bytes":268435456,"network_id":"` + nets[0].ID + `","ports":[{"container_port":80,"host_port":8080,"protocol":"tcp"}]}`
	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/workloads", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("create %d %s", res.StatusCode, raw)
	}
	if fo.lastSpec.Resources.CPUs != 2 || fo.lastSpec.Resources.MemoryBytes != 268435456 {
		t.Fatalf("resources not wired: %+v", fo.lastSpec.Resources)
	}
	if len(fo.lastSpec.Ports) != 1 || fo.lastSpec.Ports[0].ContainerPort != 80 || fo.lastSpec.Ports[0].HostPort != 8080 {
		t.Fatalf("ports not wired: %+v", fo.lastSpec.Ports)
	}
	if fo.lastSpec.NetworkID != nets[0].ID || fo.lastSpec.BridgeName != nets[0].BridgeName {
		t.Fatalf("bridge not wired: net=%s bridge=%s", fo.lastSpec.NetworkID, fo.lastSpec.BridgeName)
	}
	if fo.lastSpec.Privileged {
		t.Fatal("privileged must stay default false")
	}
	var created map[string]any
	if err := json.Unmarshal(raw, &created); err != nil {
		t.Fatal(err)
	}
	id, _ := created["id"].(string)
	nics, _ := mem.ListWorkloadNICs(context.Background(), clusterID, id)
	if len(nics) != 1 || nics[0].NetworkID != nets[0].ID {
		t.Fatalf("nics %+v", nics)
	}
}

func TestPhase21OCICreateFailsClosedWhenNICPersistFails(t *testing.T) {
	s, mem, ts, cookie, clusterID, _ := phase21Ready(t)
	nets, err := mem.ListNetworks(context.Background(), clusterID)
	if err != nil || len(nets) == 0 {
		t.Fatalf("network fixture: %v %d", err, len(nets))
	}
	s.Store = failCreateWorkloadNICStore{Store: mem}
	body := `{"name":"web","kind":"oci","image_pin":"busybox:1","network_id":"` + nets[0].ID + `"}`
	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/workloads", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ := ts.Client().Do(req)
	raw, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusInternalServerError {
		t.Fatalf("nic persist %d %s", res.StatusCode, raw)
	}
	if !strings.Contains(string(raw), "could not record OCI NIC") {
		t.Fatalf("nic persist body %s", raw)
	}
}

func TestPhase21OCICreateFailsClosedWhenDiskPersistFails(t *testing.T) {
	s, mem, ts, cookie, clusterID, _ := phase21Ready(t)
	pools, err := mem.ListStoragePools(context.Background(), clusterID)
	if err != nil || len(pools) == 0 {
		t.Fatalf("pool fixture: %v %d", err, len(pools))
	}
	volID := uuid.NewString()
	if err := mem.CreateVolume(context.Background(), appdb.Volume{
		ID: volID, ClusterID: clusterID, NodeID: pools[0].NodeID, PoolID: pools[0].ID,
		Class: storage.ClassContainerRoot, Kind: storage.KindFilesystem, Format: storage.FormatDirectory,
		Status: storage.StatusAvailable, BackendType: storage.BackendDirectory, BackendRef: "volumes/container-root/" + volID,
	}); err != nil {
		t.Fatal(err)
	}
	s.Store = failCreateWorkloadDiskStore{Store: mem}
	body := `{"name":"web","kind":"oci","image_pin":"busybox:1","volume_ids":["` + volID + `"]}`
	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/workloads", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ := ts.Client().Do(req)
	raw, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusInternalServerError {
		t.Fatalf("disk persist %d %s", res.StatusCode, raw)
	}
	if !strings.Contains(string(raw), "could not record OCI disk") {
		t.Fatalf("disk persist body %s", raw)
	}
}

func TestPhase21OCICreateFailsClosedForUnavailableVolume(t *testing.T) {
	_, mem, ts, cookie, clusterID, _ := phase21Ready(t)
	pools, err := mem.ListStoragePools(context.Background(), clusterID)
	if err != nil || len(pools) == 0 {
		t.Fatalf("pool fixture: %v %d", err, len(pools))
	}
	volID := uuid.NewString()
	if err := mem.CreateVolume(context.Background(), appdb.Volume{
		ID: volID, ClusterID: clusterID, NodeID: pools[0].NodeID, PoolID: pools[0].ID,
		Class: storage.ClassContainerRoot, Kind: storage.KindFilesystem, Format: storage.FormatDirectory,
		Status: storage.StatusUnavailable, BackendType: storage.BackendDirectory, BackendRef: "volumes/container-root/" + volID,
	}); err != nil {
		t.Fatal(err)
	}
	body := `{"name":"web","kind":"oci","image_pin":"busybox:1","volume_ids":["` + volID + `"]}`
	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/workloads", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ := ts.Client().Do(req)
	raw, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusConflict {
		t.Fatalf("unavailable volume %d %s", res.StatusCode, raw)
	}
	if !strings.Contains(string(raw), "storage is unavailable") {
		t.Fatalf("unavailable volume body %s", raw)
	}
	items, _ := mem.ListWorkloads(context.Background(), clusterID)
	if len(items) != 0 {
		t.Fatalf("GET must not list an OCI workload whose volume apply cannot mount: %+v", items)
	}
}

func TestPhase21OCICreateFailsClosedForVMDisk(t *testing.T) {
	_, mem, ts, cookie, clusterID, fo := phase21Ready(t)
	pools, err := mem.ListStoragePools(context.Background(), clusterID)
	if err != nil || len(pools) == 0 {
		t.Fatalf("pool fixture: %v %d", err, len(pools))
	}
	volID := uuid.NewString()
	if err := mem.CreateVolume(context.Background(), appdb.Volume{
		ID: volID, ClusterID: clusterID, NodeID: pools[0].NodeID, PoolID: pools[0].ID,
		Class: storage.ClassVMDisk, Kind: storage.KindBlock, Format: storage.FormatQCOW2,
		Status: storage.StatusAvailable, BackendType: storage.BackendDirectory,
		BackendRef: "volumes/vm-disk/" + volID + ".qcow2", SizeBytes: 1 << 30,
	}); err != nil {
		t.Fatal(err)
	}
	body := `{"name":"web","kind":"oci","image_pin":"busybox:1","volume_ids":["` + volID + `"]}`
	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/workloads", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ := ts.Client().Do(req)
	raw, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusConflict {
		t.Fatalf("vm-disk volume %d %s", res.StatusCode, raw)
	}
	if !strings.Contains(string(raw), "volume is not a container-root") {
		t.Fatalf("vm-disk volume body %s", raw)
	}
	if fo.lastSpec.WorkloadID != "" {
		t.Fatal("CreateOCI must not run for a vm-disk bind")
	}
	items, _ := mem.ListWorkloads(context.Background(), clusterID)
	if len(items) != 0 {
		t.Fatalf("GET must not list an OCI workload whose volume apply cannot bind: %+v", items)
	}
}

func TestPhase21OCICreateFailsClosedForUnavailableVolumePool(t *testing.T) {
	_, mem, ts, cookie, clusterID, _ := phase21Ready(t)
	pools, err := mem.ListStoragePools(context.Background(), clusterID)
	if err != nil || len(pools) == 0 {
		t.Fatalf("pool fixture: %v %d", err, len(pools))
	}
	offlinePool := uuid.NewString()
	if err := mem.CreateStoragePool(context.Background(), appdb.StoragePool{
		ID: offlinePool, ClusterID: clusterID, NodeID: pools[0].NodeID, Name: "oci-offline",
		BackendType: storage.BackendDirectory, Status: storage.StatusUnavailable,
		RootPath: "/var/lib/ndl/storage/oci-offline",
	}); err != nil {
		t.Fatal(err)
	}
	volID := uuid.NewString()
	if err := mem.CreateVolume(context.Background(), appdb.Volume{
		ID: volID, ClusterID: clusterID, NodeID: pools[0].NodeID, PoolID: offlinePool,
		Class: storage.ClassContainerRoot, Kind: storage.KindFilesystem, Format: storage.FormatDirectory,
		Status: storage.StatusAvailable, BackendType: storage.BackendDirectory, BackendRef: "volumes/container-root/" + volID,
	}); err != nil {
		t.Fatal(err)
	}
	body := `{"name":"web","kind":"oci","image_pin":"busybox:1","volume_ids":["` + volID + `"]}`
	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/workloads", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ := ts.Client().Do(req)
	raw, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusConflict {
		t.Fatalf("unavailable volume pool %d %s", res.StatusCode, raw)
	}
	if !strings.Contains(string(raw), "storage is unavailable") {
		t.Fatalf("unavailable volume pool body %s", raw)
	}
	items, _ := mem.ListWorkloads(context.Background(), clusterID)
	if len(items) != 0 {
		t.Fatalf("GET must not list an OCI workload whose volume pool apply cannot mount: %+v", items)
	}
}

func TestPhase21OCICreateFailsClosedForUnavailableNetwork(t *testing.T) {
	_, mem, ts, cookie, clusterID, _ := phase21Ready(t)
	nets, err := mem.ListNetworks(context.Background(), clusterID)
	if err != nil || len(nets) == 0 {
		t.Fatalf("network fixture: %v %d", err, len(nets))
	}
	offlineNet := uuid.NewString()
	if err := mem.CreateNetwork(context.Background(), appdb.Network{
		ID: offlineNet, ClusterID: clusterID, NodeID: nets[0].NodeID, Name: "iso-offline",
		Kind: ndnet.KindIsolated, Status: ndnet.StatusUnavailable, BridgeName: "ndlcafe00ff",
	}); err != nil {
		t.Fatal(err)
	}
	body := `{"name":"web","kind":"oci","image_pin":"busybox:1","network_id":"` + offlineNet + `"}`
	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/workloads", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ := ts.Client().Do(req)
	raw, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusConflict {
		t.Fatalf("unavailable network %d %s", res.StatusCode, raw)
	}
	if !strings.Contains(string(raw), "an available network is required") {
		t.Fatalf("unavailable network body %s", raw)
	}
	items, _ := mem.ListWorkloads(context.Background(), clusterID)
	if len(items) != 0 {
		t.Fatalf("GET must not list an OCI workload whose network apply cannot attach: %+v", items)
	}
}

func TestPhase21OCIStartFailsClosedForUnavailableVolume(t *testing.T) {
	_, mem, ts, cookie, clusterID, fo := phase21Ready(t)
	pools, err := mem.ListStoragePools(context.Background(), clusterID)
	if err != nil || len(pools) == 0 {
		t.Fatalf("pool fixture: %v %d", err, len(pools))
	}
	volID := uuid.NewString()
	if err := mem.CreateVolume(context.Background(), appdb.Volume{
		ID: volID, ClusterID: clusterID, NodeID: pools[0].NodeID, PoolID: pools[0].ID,
		Class: storage.ClassContainerRoot, Kind: storage.KindFilesystem, Format: storage.FormatDirectory,
		Status: storage.StatusAvailable, BackendType: storage.BackendDirectory, BackendRef: "volumes/container-root/" + volID,
	}); err != nil {
		t.Fatal(err)
	}
	body := `{"name":"web","kind":"oci","image_pin":"busybox:1","volume_ids":["` + volID + `"],"desired_power":"stopped"}`
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
	if err := mem.UpdateVolumeObserved(context.Background(), appdb.Volume{ID: volID, Status: storage.StatusUnavailable}); err != nil {
		t.Fatal(err)
	}
	fo.lastLife = oci.LifecycleRequest{}
	start, _ := http.NewRequest("POST", ts.URL+"/api/v1/workloads/"+id+"/start", strings.NewReader("{}"))
	start.Header.Set("Content-Type", "application/json")
	start.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(start)
	raw, _ = io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusConflict {
		t.Fatalf("unavailable volume start %d %s", res.StatusCode, raw)
	}
	if !strings.Contains(string(raw), "storage is unavailable") {
		t.Fatalf("unavailable volume start body %s", raw)
	}
	if fo.lastLife.Action == "start" {
		t.Fatal("start must not call the agent when volume apply cannot mount")
	}
	wl, _ := mem.GetWorkload(context.Background(), clusterID, id)
	if wl == nil || wl.DesiredPower == "running" {
		t.Fatalf("GET must not claim desired_power running when start cannot use storage: %+v", wl)
	}
}

func TestPhase21OCIStartFailsClosedForUnavailableExtraVolume(t *testing.T) {
	_, mem, ts, cookie, clusterID, fo := phase21Ready(t)
	pools, err := mem.ListStoragePools(context.Background(), clusterID)
	if err != nil || len(pools) == 0 {
		t.Fatalf("pool fixture: %v %d", err, len(pools))
	}
	volA := uuid.NewString()
	volB := uuid.NewString()
	for _, id := range []string{volA, volB} {
		if err := mem.CreateVolume(context.Background(), appdb.Volume{
			ID: id, ClusterID: clusterID, NodeID: pools[0].NodeID, PoolID: pools[0].ID,
			Class: storage.ClassContainerRoot, Kind: storage.KindFilesystem, Format: storage.FormatDirectory,
			Status: storage.StatusAvailable, BackendType: storage.BackendDirectory, BackendRef: "volumes/container-root/" + id,
		}); err != nil {
			t.Fatal(err)
		}
	}
	body := `{"name":"web","kind":"oci","image_pin":"busybox:1","volume_ids":["` + volA + `","` + volB + `"],"desired_power":"stopped"}`
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
	disks, _ := mem.ListWorkloadDisks(context.Background(), clusterID, id)
	if len(disks) != 2 {
		t.Fatalf("disks %+v", disks)
	}
	if err := mem.UpdateVolumeObserved(context.Background(), appdb.Volume{ID: disks[1].VolumeID, Status: storage.StatusUnavailable}); err != nil {
		t.Fatal(err)
	}
	fo.lastLife = oci.LifecycleRequest{}
	start, _ := http.NewRequest("POST", ts.URL+"/api/v1/workloads/"+id+"/start", strings.NewReader("{}"))
	start.Header.Set("Content-Type", "application/json")
	start.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(start)
	raw, _ = io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusConflict {
		t.Fatalf("unavailable extra volume start %d %s", res.StatusCode, raw)
	}
	if !strings.Contains(string(raw), "storage is unavailable") {
		t.Fatalf("unavailable extra volume start body %s", raw)
	}
	if fo.lastLife.Action == "start" {
		t.Fatal("start must not call the agent when an extra volume apply cannot mount")
	}
	wl, _ := mem.GetWorkload(context.Background(), clusterID, id)
	if wl == nil || wl.DesiredPower == "running" {
		t.Fatalf("GET must not claim desired_power running when start cannot use extra storage: %+v", wl)
	}
}

func TestPhase21UnitPathIndependentOfCP(t *testing.T) {
	unit := oci.UnitName(uuid.NewString())
	if strings.Contains(unit, "ndl-control") || strings.Contains(unit, "ndl-agent") {
		t.Fatal(unit)
	}
	_ = storage.FormatDirectory
	_ = gpu.ModeRender
}

type gpuStub struct{}

func (gpuStub) GPUAssign(context.Context, gpu.AssignRequest) (gpu.AssignResult, error) {
	return gpu.AssignResult{Status: gpu.StatusAssigned}, nil
}
