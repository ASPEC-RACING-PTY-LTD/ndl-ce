package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/no-dal/ndl-ce/internal/appdb"
	"github.com/no-dal/ndl-ce/internal/auth"
	"github.com/no-dal/ndl-ce/internal/gpu"
	"github.com/no-dal/ndl-ce/internal/inventory"
	"github.com/no-dal/ndl-ce/internal/lxc"
	"github.com/no-dal/ndl-ce/internal/rbac"
)

type fakeGPU struct {
	calls []gpu.AssignRequest
}

func (f *fakeGPU) GPUAssign(_ context.Context, req gpu.AssignRequest) (gpu.AssignResult, error) {
	f.calls = append(f.calls, req)
	return gpu.AssignResult{Status: gpu.StatusAssigned, PCIDevices: req.PCIDevices, DeviceNodes: req.DeviceNodes}, nil
}

func gpuInv() inventory.Inventory {
	return inventory.Inventory{
		SchemaVersion: inventory.SchemaVersion,
		ObservedAt:    time.Now().UTC(),
		Host:          inventory.Host{Status: inventory.StatusAvailable, ID: "debian", VersionID: "13", Architecture: "amd64"},
		GPUs: []inventory.GPU{{
			ID: "0000:02:00.0", PCI: "0000:02:00.0", Vendor: "NVIDIA", IOMMUGroup: "12", Driver: "nvidia",
			Hint: "/dev/dri/by-path/pci-0000:02:00.0-render",
		}},
		PCI: []inventory.PCIDevice{
			{Address: "0000:02:00.0", Class: "0x030000", Driver: "nvidia", IOMMUGroup: "12"},
			{Address: "0000:02:00.1", Class: "0x040300", Driver: "snd_hda_intel", IOMMUGroup: "12"},
		},
		IOMMU: inventory.IOMMU{Status: inventory.StatusAvailable, Groups: []inventory.IOMMUGroup{
			{ID: "12", Devices: []string{"0000:02:00.0", "0000:02:00.1"}},
		}},
		Capabilities: []inventory.Capability{{ID: "gpu", Status: inventory.StatusAvailable}, {ID: "iommu", Status: inventory.StatusAvailable}},
	}
}

func TestGPUListIncludesHDMIAudioAndRejectsAll(t *testing.T) {
	s, mem, token := testServer(t)
	s.GPU = &fakeGPU{}
	cluster, _ := mem.GetCluster(context.Background())
	seedNode(t, mem, cluster.ID, gpuInv(), false)
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)

	req, _ := http.NewRequest("GET", ts.URL+"/api/v1/gpus", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ := ts.Client().Do(req)
	b, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatalf("%d %s", res.StatusCode, b)
	}
	if !strings.Contains(string(b), `"kind":"audio"`) || !strings.Contains(string(b), "0000:02:00.1") {
		t.Fatalf("hdmi audio missing: %s", b)
	}
	if !strings.Contains(string(b), `/dev/dri`) && !strings.Contains(string(b), "do not receive") {
		t.Fatal("honest empty default")
	}

	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/gpus/assign", strings.NewReader(`{"gpu_id":"all","workload_id":"`+uuid.NewString()+`","mode":"render"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(req)
	if res.StatusCode != http.StatusBadRequest {
		b, _ = io.ReadAll(res.Body)
		t.Fatalf("gpu=all %d %s", res.StatusCode, b)
	}
	_ = res.Body.Close()
}

func TestGPUExclusiveClaimsAndCTRender(t *testing.T) {
	s, mem, token := testServer(t)
	fg := &fakeGPU{}
	s.GPU = fg
	cluster, _ := mem.GetCluster(context.Background())
	seedNode(t, mem, cluster.ID, gpuInv(), false)
	ct := appdb.Workload{ID: uuid.NewString(), ClusterID: cluster.ID, Name: "ct", Kind: lxc.KindSystemContainer, Status: "stopped"}
	if err := mem.CreateWorkload(context.Background(), ct); err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)

	body := `{"gpu_id":"0000:02:00.0","workload_id":"` + ct.ID + `","mode":"render","exclusive":true}`
	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/gpus/assign", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ := ts.Client().Do(req)
	b, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("assign %d %s", res.StatusCode, b)
	}
	if strings.Contains(string(b), "renderD128") || strings.Contains(string(b), "/dev/nvidia0") {
		t.Fatalf("must not invent default device nodes: %s", b)
	}
	if !strings.Contains(string(b), "/dev/dri/by-path/pci-0000:02:00.0-render") {
		t.Fatalf("must use inventory locator: %s", b)
	}
	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/gpus/assign", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(req)
	if res.StatusCode != http.StatusConflict {
		b, _ = io.ReadAll(res.Body)
		t.Fatalf("second exclusive %d %s", res.StatusCode, b)
	}
	_ = res.Body.Close()

	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/gpus/assign", strings.NewReader(`{"gpu_id":"0000:02:00.0","workload_id":"`+ct.ID+`","mode":"render","acs_override":true}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(req)
	if res.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("acs %d", res.StatusCode)
	}
	_ = res.Body.Close()
}

func TestGPUViewerDeniedAndVFIONeedsSnapshot(t *testing.T) {
	s, mem, token := testServer(t)
	s.GPU = &fakeGPU{}
	cluster, _ := mem.GetCluster(context.Background())
	seedNode(t, mem, cluster.ID, gpuInv(), false)
	vm := appdb.Workload{ID: uuid.NewString(), ClusterID: cluster.ID, Name: "vm", Kind: "vm", Status: "stopped"}
	_ = mem.CreateWorkload(context.Background(), vm)
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)

	hash, _ := auth.HashPassword("password1")
	u := appdb.User{ID: uuid.NewString(), ClusterID: cluster.ID, Username: "view", PasswordHash: hash}
	_ = mem.CreateUser(context.Background(), u)
	_ = mem.BindRole(context.Background(), cluster.ID, u.ID, rbac.Viewer)
	vlogin, _ := ts.Client().Post(ts.URL+"/api/v1/auth/login", "application/json", strings.NewReader(`{"username":"view","password":"password1"}`))
	var viewCookie string
	for _, c := range vlogin.Cookies() {
		if c.Name == sessionCookie {
			viewCookie = c.Value
		}
	}
	_ = vlogin.Body.Close()
	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/gpus/assign", strings.NewReader(`{"gpu_id":"0000:02:00.0","workload_id":"`+vm.ID+`","mode":"vfio"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: viewCookie})
	res, _ := ts.Client().Do(req)
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("viewer %d", res.StatusCode)
	}
	_ = res.Body.Close()

	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/gpus/assign", strings.NewReader(`{"gpu_id":"0000:02:00.0","workload_id":"`+vm.ID+`","mode":"vfio"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(req)
	if res.StatusCode != http.StatusUnprocessableEntity {
		b, _ := io.ReadAll(res.Body)
		t.Fatalf("vfio no snap %d %s", res.StatusCode, b)
	}
	_ = res.Body.Close()
	_ = mem.CreateSnapshot(context.Background(), appdb.Snapshot{ID: uuid.NewString(), ClusterID: cluster.ID, WorkloadID: vm.ID, Name: "pre-vfio", Status: appdb.SnapshotAvailable})
	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/gpus/assign", strings.NewReader(`{"gpu_id":"0000:02:00.0","workload_id":"`+vm.ID+`","mode":"vfio"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(req)
	b, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("vfio %d %s", res.StatusCode, b)
	}
	var assigned map[string]any
	_ = json.Unmarshal(b, &assigned)
	devs, _ := assigned["pci_devices"].([]any)
	if len(devs) != 2 {
		t.Fatalf("group completeness %s", b)
	}

	running := vm
	running.Status = lxc.StatusRunning
	_ = mem.UpdateWorkloadObserved(context.Background(), running)
	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/gpus/unassign", strings.NewReader(`{"id":"`+assigned["id"].(string)+`"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(req)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("unassign %d", res.StatusCode)
	}
	_ = mem.CreateSnapshot(context.Background(), appdb.Snapshot{ID: uuid.NewString(), ClusterID: cluster.ID, WorkloadID: vm.ID, Name: "pre-vfio-2", Status: appdb.SnapshotAvailable})
	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/gpus/assign", strings.NewReader(`{"gpu_id":"0000:02:00.0","workload_id":"`+vm.ID+`","mode":"vfio"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(req)
	if res.StatusCode != http.StatusUnprocessableEntity {
		b, _ := io.ReadAll(res.Body)
		t.Fatalf("vfio running %d %s", res.StatusCode, b)
	}
	_ = res.Body.Close()
}

func TestGPUAssignWithoutLocatorIsUnavailable(t *testing.T) {
	s, mem, token := testServer(t)
	s.GPU = &fakeGPU{}
	cluster, _ := mem.GetCluster(context.Background())
	inv := gpuInv()
	inv.GPUs[0].Hint = ""
	seedNode(t, mem, cluster.ID, inv, false)
	ct := appdb.Workload{ID: uuid.NewString(), ClusterID: cluster.ID, Name: "ct", Kind: lxc.KindSystemContainer, Status: "stopped"}
	if err := mem.CreateWorkload(context.Background(), ct); err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)
	body := `{"gpu_id":"0000:02:00.0","workload_id":"` + ct.ID + `","mode":"render"}`
	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/gpus/assign", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ := ts.Client().Do(req)
	b, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("missing locator %d %s", res.StatusCode, b)
	}
	if strings.Contains(string(b), "renderD128") || strings.Contains(string(b), "nvidia0") {
		t.Fatalf("must not invent nodes: %s", b)
	}
}

func TestGPURuntimeUnsupportedOnEmptyInventory(t *testing.T) {
	s, mem, token := testServer(t)
	s.GPU = &fakeGPU{}
	cluster, _ := mem.GetCluster(context.Background())
	seedNode(t, mem, cluster.ID, inventory.Inventory{Host: inventory.Host{ID: "ubuntu", VersionID: "24.04", Architecture: "amd64"}}, false)
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)
	req, _ := http.NewRequest("GET", ts.URL+"/api/v1/gpus/runtime", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ := ts.Client().Do(req)
	b, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != 200 || !strings.Contains(string(b), `"host_supported":false`) {
		t.Fatalf("%d %s", res.StatusCode, b)
	}
	if strings.Contains(string(b), "NVIDIA_VISIBLE_DEVICES=all") {
		t.Fatal("must never advertise all")
	}
}
