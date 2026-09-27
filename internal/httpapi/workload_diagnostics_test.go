package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/no-dal/ndl-ce/internal/appdb"
	"github.com/no-dal/ndl-ce/internal/auth"
	"github.com/no-dal/ndl-ce/internal/gpu"
	"github.com/no-dal/ndl-ce/internal/inventory"
	"github.com/no-dal/ndl-ce/internal/lxc"
	"github.com/no-dal/ndl-ce/internal/rbac"
)

func intPtr(n int) *int { return &n }

var nvidiaEncodeNodes = []string{
	"/dev/dri/by-path/pci-0000:02:00.0-render",
	"/dev/nvidia1",
	"/dev/nvidiactl",
	"/dev/nvidia-uvm",
	"/dev/nvidia-uvm-tools",
	"/dev/nvidia-modeset",
}

func TestDeviceNodesForModeNVIDIAEncode(t *testing.T) {
	rec := gpuInv().GPUs[0]
	for _, mode := range []string{gpu.ModeRender, gpu.ModeCompute, gpu.ModeEncode} {
		got, err := deviceNodesForMode(mode, rec)
		if err != nil {
			t.Fatalf("%s: %v", mode, err)
		}
		if !slices.Equal(got, nvidiaEncodeNodes) {
			t.Fatalf("%s nodes %v", mode, got)
		}
		for _, n := range got {
			if strings.HasPrefix(n, "/dev/nvidia-caps") || n == "/dev/nvidia0" {
				t.Fatalf("%s must not grant %s", mode, n)
			}
		}
	}
	if nodes, _ := deviceNodesForMode(gpu.ModeVFIO, rec); nodes != nil {
		t.Fatalf("vfio nodes %v", nodes)
	}
	rec.NVIDIAMinor = nil
	if _, err := deviceNodesForMode(gpu.ModeEncode, rec); err == nil || !strings.Contains(err.Error(), "NVIDIA device minor") {
		t.Fatalf("unknown minor must fail closed, got %v", err)
	}
	amd := inventory.GPU{ID: "0000:13:00.0", PCI: "0000:13:00.0", Driver: "amdgpu",
		Hint: "/dev/dri/by-path/pci-0000:13:00.0-render /dev/dri/by-path/pci-0000:13:00.0-card"}
	got, err := deviceNodesForMode(gpu.ModeEncode, amd)
	if err != nil || len(got) != 2 || strings.Contains(strings.Join(got, " "), "nvidia") {
		t.Fatalf("amd nodes %v %v", got, err)
	}
}

func TestGPUAssignEncodeSendsNVIDIANodes(t *testing.T) {
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
	res := doJSON(t, ts, cookie, "POST", "/api/v1/gpus/assign", `{"gpu_id":"0000:02:00.0","workload_id":"`+ct.ID+`","mode":"encode"}`)
	if res.code != http.StatusCreated {
		t.Fatalf("assign %d %s", res.code, res.body)
	}
	if len(fg.calls) != 1 || !slices.Equal(fg.calls[0].DeviceNodes, nvidiaEncodeNodes) {
		t.Fatalf("agent nodes %+v", fg.calls)
	}
}

type diagGPU struct {
	calls []gpu.AssignRequest
	diag  lxc.GPUDiagnosis
	fail  string
}

func (f *diagGPU) GPUAssign(_ context.Context, req gpu.AssignRequest) (gpu.AssignResult, error) {
	f.calls = append(f.calls, req)
	if f.fail != "" && req.Action == gpu.ActionReapply {
		return gpu.AssignResult{Status: gpu.StatusFailed, Reason: f.fail}, nil
	}
	b, _ := json.Marshal(f.diag)
	return gpu.AssignResult{Status: "ok", DeviceNodes: req.DeviceNodes, Diagnosis: b}, nil
}

type jsonResult struct {
	code int
	body string
}

func doJSON(t *testing.T, ts *httptest.Server, cookie, method, path, body string) jsonResult {
	t.Helper()
	var rd io.Reader
	if body != "" {
		rd = strings.NewReader(body)
	}
	req, _ := http.NewRequest(method, ts.URL+path, rd)
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	return jsonResult{code: res.StatusCode, body: string(b)}
}

func diagNodes(guest string) []lxc.GPUNodeDiagnosis {
	var out []lxc.GPUNodeDiagnosis
	for i, p := range nvidiaEncodeNodes {
		out = append(out, lxc.GPUNodeDiagnosis{Path: p, HostRule: "c 195:" + string(rune('0'+i)) + " rwm", Mounted: true, Allowed: true, Guest: guest})
	}
	return out
}

// seedStaleEncode reproduces a claim saved before NVIDIA nodes were derived:
// only the DRI by-path locator is stored.
func seedStaleEncode(t *testing.T, mem *appdb.Memory, clusterID string, kind string) (appdb.Workload, appdb.GPUAssignment) {
	t.Helper()
	id := uuid.NewString()
	wl := appdb.Workload{ID: id, ClusterID: clusterID, Name: "gpu-" + id[:8], Kind: kind, Status: "running"}
	if err := mem.CreateWorkload(context.Background(), wl); err != nil {
		t.Fatal(err)
	}
	a := appdb.GPUAssignment{
		ID: uuid.NewString(), ClusterID: clusterID, GPUID: "0000:02:00.0", WorkloadID: wl.ID,
		Mode: gpu.ModeEncode, IOMMUGroup: "12", PCIDevices: []string{"0000:02:00.0"},
		DeviceNodes: []string{"/dev/dri/by-path/pci-0000:02:00.0-render"}, Status: gpu.StatusAssigned,
	}
	if err := mem.CreateGPUAssignment(context.Background(), a); err != nil {
		t.Fatal(err)
	}
	return wl, a
}

func TestReapplyGPURegeneratesFromSavedAssignment(t *testing.T) {
	s, mem, token := testServer(t)
	fg := &diagGPU{diag: lxc.GPUDiagnosis{Running: true, ConfigPresent: true, ConfigCurrent: true, RestartRequired: true, Nodes: diagNodes(lxc.GuestNodeMissing)}}
	s.GPU = fg
	cluster, _ := mem.GetCluster(context.Background())
	seedNode(t, mem, cluster.ID, gpuInv(), false)
	wl, a := seedStaleEncode(t, mem, cluster.ID, lxc.KindSystemContainer)
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)

	diag := doJSON(t, ts, cookie, "GET", "/api/v1/workloads/"+wl.ID+"/diagnostics/gpu", "")
	if diag.code != http.StatusOK || !strings.Contains(diag.body, `"current":false`) || !strings.Contains(diag.body, `"restart_required":true`) {
		t.Fatalf("diagnostics %d %s", diag.code, diag.body)
	}
	if got, _ := mem.GetGPUAssignment(context.Background(), cluster.ID, a.ID); len(got.DeviceNodes) != 1 {
		t.Fatalf("diagnostics must not write: %v", got.DeviceNodes)
	}

	res := doJSON(t, ts, cookie, "POST", "/api/v1/workloads/"+wl.ID+"/gpus/reapply", "")
	if res.code != http.StatusOK || !strings.Contains(res.body, `"status":"restart_required"`) {
		t.Fatalf("reapply %d %s", res.code, res.body)
	}
	last := fg.calls[len(fg.calls)-1]
	if last.Action != gpu.ActionReapply || last.WorkloadID != wl.ID || !slices.Equal(last.DeviceNodes, nvidiaEncodeNodes) {
		t.Fatalf("agent call %+v", last)
	}
	got, _ := mem.GetGPUAssignment(context.Background(), cluster.ID, a.ID)
	if !slices.Equal(got.DeviceNodes, nvidiaEncodeNodes) || got.Mode != gpu.ModeEncode {
		t.Fatalf("stored claim %+v", got)
	}

	fg.diag = lxc.GPUDiagnosis{Running: true, ConfigPresent: true, ConfigCurrent: true, Nodes: diagNodes(lxc.GuestNodePresent)}
	res = doJSON(t, ts, cookie, "POST", "/api/v1/workloads/"+wl.ID+"/gpus/reapply", "")
	if res.code != http.StatusOK || !strings.Contains(res.body, `"status":"applied"`) {
		t.Fatalf("verified reapply %d %s", res.code, res.body)
	}
}

func TestReapplyGPUNeverClaimsUnverifiedSuccess(t *testing.T) {
	cases := []struct {
		name string
		diag lxc.GPUDiagnosis
		want string
	}{
		{"stale config", lxc.GPUDiagnosis{Running: true, ConfigPresent: true, Nodes: diagNodes(lxc.GuestNodePresent)}, gpuReapplyFailed},
		{"guest unknown", lxc.GPUDiagnosis{Running: true, ConfigPresent: true, ConfigCurrent: true, Nodes: diagNodes(lxc.GuestNodeUnknown)}, gpuReapplyFailed},
		{"stopped", lxc.GPUDiagnosis{ConfigPresent: true, ConfigCurrent: true, Nodes: diagNodes(lxc.GuestNodeNotRunning)}, gpuReapplyApplied},
	}
	for _, c := range cases {
		if got, _ := reapplyOutcome(c.diag); got != c.want {
			t.Fatalf("%s: %s want %s", c.name, got, c.want)
		}
	}
	missingRule := lxc.GPUDiagnosis{Running: true, ConfigPresent: true, ConfigCurrent: true, Nodes: diagNodes(lxc.GuestNodePresent)}
	missingRule.Nodes[1].Allowed = false
	if got, _ := reapplyOutcome(missingRule); got != gpuReapplyFailed {
		t.Fatalf("missing permission: %s", got)
	}
}

func TestReapplyGPURestoresClaimOnAgentFailure(t *testing.T) {
	s, mem, token := testServer(t)
	s.GPU = &diagGPU{fail: "system container last-applied is missing"}
	cluster, _ := mem.GetCluster(context.Background())
	seedNode(t, mem, cluster.ID, gpuInv(), false)
	wl, a := seedStaleEncode(t, mem, cluster.ID, lxc.KindSystemContainer)
	vm, _ := seedStaleEncode(t, mem, cluster.ID, "vm")
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)

	res := doJSON(t, ts, cookie, "POST", "/api/v1/workloads/"+wl.ID+"/gpus/reapply", "")
	if res.code != http.StatusBadGateway || !strings.Contains(res.body, `"status":"failed"`) {
		t.Fatalf("reapply %d %s", res.code, res.body)
	}
	if got, _ := mem.GetGPUAssignment(context.Background(), cluster.ID, a.ID); len(got.DeviceNodes) != 1 {
		t.Fatalf("claim must be restored: %v", got.DeviceNodes)
	}
	if res := doJSON(t, ts, cookie, "POST", "/api/v1/workloads/"+vm.ID+"/gpus/reapply", ""); res.code != http.StatusUnprocessableEntity {
		t.Fatalf("vm reapply %d %s", res.code, res.body)
	}

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
	if res := doJSON(t, ts, viewCookie, "POST", "/api/v1/workloads/"+wl.ID+"/gpus/reapply", ""); res.code != http.StatusForbidden {
		t.Fatalf("viewer reapply %d", res.code)
	}
	if res := doJSON(t, ts, viewCookie, "GET", "/api/v1/workloads/"+wl.ID+"/diagnostics/gpu", ""); res.code != http.StatusOK {
		t.Fatalf("viewer diagnostics %d %s", res.code, res.body)
	}
}
