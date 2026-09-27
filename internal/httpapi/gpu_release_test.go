package httpapi

import (
	"context"
	"encoding/json"
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

var amdRenderNodes = []string{"/dev/dri/by-path/pci-0000:13:00.0-render"}

// twoGPUInv is the NVIDIA card from gpuInv plus an AMD render GPU.
func twoGPUInv() inventory.Inventory {
	inv := gpuInv()
	inv.GPUs = append(inv.GPUs, inventory.GPU{
		ID: "0000:13:00.0", PCI: "0000:13:00.0", Vendor: "AMD", IOMMUGroup: "20", Driver: "amdgpu",
		Hint: amdRenderNodes[0],
	})
	inv.PCI = append(inv.PCI, inventory.PCIDevice{Address: "0000:13:00.0", Class: "0x030000", Driver: "amdgpu", IOMMUGroup: "20"})
	return inv
}

// releaseGPU is an agent fake for ActionRelease. It reports a diagnosis
// like a system container, and can fail, apply a different list, or leave
// the config stale.
type releaseGPU struct {
	calls     []gpu.AssignRequest
	running   bool
	fail      string
	stale     bool
	wrongList bool
}

func (f *releaseGPU) GPUAssign(_ context.Context, req gpu.AssignRequest) (gpu.AssignResult, error) {
	f.calls = append(f.calls, req)
	rollback := len(f.calls) > 1
	if f.fail != "" && !rollback {
		return gpu.AssignResult{Status: gpu.StatusFailed, Reason: f.fail}, nil
	}
	nodes := req.DeviceNodes
	if f.wrongList && !rollback {
		nodes = append(append([]string{}, nodes...), "/dev/dri/renderD200")
	}
	diag, _ := json.Marshal(lxc.GPUDiagnosis{
		Saved: nodes, Running: f.running, ConfigPresent: true, ConfigCurrent: !(f.stale && !rollback),
		Nodes: []lxc.GPUNodeDiagnosis{}, Issues: []string{},
	})
	return gpu.AssignResult{Status: gpu.StatusReleased, DeviceNodes: nodes, Diagnosis: diag}, nil
}

type releaseFixture struct {
	mem     *appdb.Memory
	ts      *httptest.Server
	cookie  string
	cluster string
	wl      appdb.Workload
	nvidia  appdb.GPUAssignment
	amd     appdb.GPUAssignment
}

func newReleaseFixture(t *testing.T, fg GPURPC, inv inventory.Inventory, status string, nvidiaNodes []string) releaseFixture {
	t.Helper()
	s, mem, token := testServer(t)
	s.GPU = fg
	cluster, _ := mem.GetCluster(context.Background())
	seedNode(t, mem, cluster.ID, inv, false)
	wl := appdb.Workload{ID: uuid.NewString(), ClusterID: cluster.ID, Name: "viewdock-test", Kind: lxc.KindSystemContainer, Status: status}
	if err := mem.CreateWorkload(context.Background(), wl); err != nil {
		t.Fatal(err)
	}
	nv := appdb.GPUAssignment{
		ID: uuid.NewString(), ClusterID: cluster.ID, GPUID: "0000:02:00.0", WorkloadID: wl.ID,
		Mode: gpu.ModeEncode, IOMMUGroup: "12", PCIDevices: []string{"0000:02:00.0"},
		DeviceNodes: nvidiaNodes, Status: gpu.StatusAssigned,
	}
	amd := appdb.GPUAssignment{
		ID: uuid.NewString(), ClusterID: cluster.ID, GPUID: "0000:13:00.0", WorkloadID: wl.ID,
		Mode: gpu.ModeRender, IOMMUGroup: "20", PCIDevices: []string{"0000:13:00.0"},
		DeviceNodes: amdRenderNodes, Status: gpu.StatusAssigned,
	}
	for _, a := range []appdb.GPUAssignment{nv, amd} {
		if err := mem.CreateGPUAssignment(context.Background(), a); err != nil {
			t.Fatal(err)
		}
	}
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close)
	return releaseFixture{mem: mem, ts: ts, cookie: claimAdmin(t, ts, token), cluster: cluster.ID, wl: wl, nvidia: nv, amd: amd}
}

func (f releaseFixture) unassign(t *testing.T, cookie, id string) jsonResult {
	t.Helper()
	return doJSON(t, f.ts, cookie, "POST", "/api/v1/gpus/unassign", `{"id":"`+id+`"}`)
}

func (f releaseFixture) claim(t *testing.T, id string) *appdb.GPUAssignment {
	t.Helper()
	got, err := f.mem.GetGPUAssignment(context.Background(), f.cluster, id)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func TestUnassignOneGPUKeepsOtherGPUs(t *testing.T) {
	fg := &releaseGPU{running: true}
	f := newReleaseFixture(t, fg, twoGPUInv(), "running", nvidiaEncodeNodes)

	res := f.unassign(t, f.cookie, f.nvidia.ID)
	if res.code != http.StatusOK || !strings.Contains(res.body, `"restart_required":true`) {
		t.Fatalf("unassign %d %s", res.code, res.body)
	}
	if len(fg.calls) != 1 {
		t.Fatalf("want one agent apply, got %+v", fg.calls)
	}
	call := fg.calls[0]
	if call.Action != gpu.ActionRelease || call.WorkloadID != f.wl.ID || !slices.Equal(call.DeviceNodes, amdRenderNodes) {
		t.Fatalf("agent must receive the remaining GPU's nodes, got %+v", call)
	}
	if f.claim(t, f.nvidia.ID) != nil {
		t.Fatal("released claim must be deleted")
	}
	if got := f.claim(t, f.amd.ID); got == nil || !slices.Equal(got.DeviceNodes, amdRenderNodes) {
		t.Fatalf("remaining claim must be untouched: %+v", got)
	}

	res = f.unassign(t, f.cookie, f.amd.ID)
	if res.code != http.StatusOK {
		t.Fatalf("last unassign %d %s", res.code, res.body)
	}
	last := fg.calls[len(fg.calls)-1]
	if last.Action != gpu.ActionRelease || len(last.DeviceNodes) != 0 {
		t.Fatalf("last GPU removal must apply an empty list: %+v", last)
	}
	if f.claim(t, f.amd.ID) != nil {
		t.Fatal("last claim must be deleted")
	}
}

func TestUnassignSingleGPUOnStoppedContainer(t *testing.T) {
	fg := &releaseGPU{}
	f := newReleaseFixture(t, fg, twoGPUInv(), "stopped", nvidiaEncodeNodes)
	if err := f.mem.DeleteGPUAssignment(context.Background(), f.cluster, f.amd.ID); err != nil {
		t.Fatal(err)
	}
	res := f.unassign(t, f.cookie, f.nvidia.ID)
	if res.code != http.StatusOK || !strings.Contains(res.body, `"restart_required":false`) {
		t.Fatalf("unassign %d %s", res.code, res.body)
	}
	if len(fg.calls) != 1 || len(fg.calls[0].DeviceNodes) != 0 {
		t.Fatalf("agent calls %+v", fg.calls)
	}
}

func TestUnassignRebuildsRemainingFromSavedAssignment(t *testing.T) {
	fg := &releaseGPU{}
	stale := []string{"/dev/dri/by-path/pci-0000:02:00.0-render"}
	f := newReleaseFixture(t, fg, twoGPUInv(), "stopped", stale)

	if res := f.unassign(t, f.cookie, f.amd.ID); res.code != http.StatusOK {
		t.Fatalf("unassign %d %s", res.code, res.body)
	}
	if !slices.Equal(fg.calls[0].DeviceNodes, nvidiaEncodeNodes) {
		t.Fatalf("remaining NVIDIA claim must be rebuilt from inventory: %v", fg.calls[0].DeviceNodes)
	}
	if got := f.claim(t, f.nvidia.ID); !slices.Equal(got.DeviceNodes, nvidiaEncodeNodes) {
		t.Fatalf("stored claim must match what was applied: %v", got.DeviceNodes)
	}
}

func TestUnassignRestoresStateWhenAgentFails(t *testing.T) {
	stale := []string{"/dev/dri/by-path/pci-0000:02:00.0-render"}
	cases := []struct {
		name      string
		fg        *releaseGPU
		rollbacks int
	}{
		{"agent failure", &releaseGPU{fail: "system container last-applied is missing"}, 0},
		{"config not regenerated", &releaseGPU{stale: true}, 1},
		{"different list applied", &releaseGPU{wrongList: true}, 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newReleaseFixture(t, c.fg, twoGPUInv(), "running", stale)
			res := f.unassign(t, f.cookie, f.amd.ID)
			if res.code != http.StatusBadGateway {
				t.Fatalf("unassign %d %s", res.code, res.body)
			}
			if got := f.claim(t, f.amd.ID); got == nil {
				t.Fatal("claim must be kept when the release is not applied")
			}
			if got := f.claim(t, f.nvidia.ID); !slices.Equal(got.DeviceNodes, stale) {
				t.Fatalf("remaining claim must be restored: %v", got.DeviceNodes)
			}
			if len(c.fg.calls) != 1+c.rollbacks {
				t.Fatalf("agent calls %+v", c.fg.calls)
			}
			if c.rollbacks > 0 {
				want := append(append([]string{}, stale...), amdRenderNodes...)
				if back := c.fg.calls[1]; back.Action != gpu.ActionRelease || !slices.Equal(back.DeviceNodes, want) {
					t.Fatalf("rollback must reapply the previous list %v, got %+v", want, back)
				}
			}
		})
	}
}

func TestUnassignRequiresGPUAssignGrant(t *testing.T) {
	fg := &releaseGPU{}
	f := newReleaseFixture(t, fg, twoGPUInv(), "running", nvidiaEncodeNodes)
	hash, _ := auth.HashPassword("password1")
	u := appdb.User{ID: uuid.NewString(), ClusterID: f.cluster, Username: "view", PasswordHash: hash}
	_ = f.mem.CreateUser(context.Background(), u)
	_ = f.mem.BindRole(context.Background(), f.cluster, u.ID, rbac.Viewer)
	login, err := f.ts.Client().Post(f.ts.URL+"/api/v1/auth/login", "application/json", strings.NewReader(`{"username":"view","password":"password1"}`))
	if err != nil {
		t.Fatal(err)
	}
	var viewCookie string
	for _, c := range login.Cookies() {
		if c.Name == sessionCookie {
			viewCookie = c.Value
		}
	}
	_ = login.Body.Close()
	if res := f.unassign(t, viewCookie, f.nvidia.ID); res.code != http.StatusForbidden {
		t.Fatalf("viewer unassign %d %s", res.code, res.body)
	}
	if len(fg.calls) != 0 || f.claim(t, f.nvidia.ID) == nil {
		t.Fatal("denied unassign must not touch the agent or the claim")
	}
}

func TestRemovedGPUIsListedAndCanBeUnassigned(t *testing.T) {
	fg := &releaseGPU{}
	inv := twoGPUInv()
	inv.GPUs = inv.GPUs[1:]
	inv.PCI = inv.PCI[1:]
	f := newReleaseFixture(t, fg, inv, "stopped", nvidiaEncodeNodes)

	list := doJSON(t, f.ts, f.cookie, "GET", "/api/v1/gpus", "")
	var body struct {
		Orphaned []map[string]any `json:"orphaned_assignments"`
	}
	if err := json.Unmarshal([]byte(list.body), &body); err != nil || len(body.Orphaned) != 1 || body.Orphaned[0]["id"] != f.nvidia.ID {
		t.Fatalf("removed GPU claim must be listed: %s", list.body)
	}

	diag := doJSON(t, f.ts, f.cookie, "GET", "/api/v1/workloads/"+f.wl.ID+"/diagnostics/gpu", "")
	if !strings.Contains(diag.body, "gpu is not present on this node; if it was removed, unassign it") {
		t.Fatalf("diagnostics must name the removed GPU: %s", diag.body)
	}
	reapply := doJSON(t, f.ts, f.cookie, "POST", "/api/v1/workloads/"+f.wl.ID+"/gpus/reapply", "")
	if reapply.code != http.StatusUnprocessableEntity {
		t.Fatalf("reapply with a removed GPU must be refused: %d %s", reapply.code, reapply.body)
	}
	for _, c := range fg.calls {
		if c.Action == gpu.ActionReapply {
			t.Fatalf("refused reapply must not reach the agent: %+v", c)
		}
	}

	fg.calls = nil
	if res := f.unassign(t, f.cookie, f.nvidia.ID); res.code != http.StatusOK {
		t.Fatalf("unassign removed GPU %d %s", res.code, res.body)
	}
	if !slices.Equal(fg.calls[0].DeviceNodes, amdRenderNodes) || f.claim(t, f.nvidia.ID) != nil {
		t.Fatalf("release must keep the present GPU: %+v", fg.calls)
	}
}

func TestDiagnosticsReportsAppliedDrift(t *testing.T) {
	fg := &diagGPU{diag: lxc.GPUDiagnosis{Saved: amdRenderNodes, ConfigPresent: true, ConfigCurrent: true, Nodes: []lxc.GPUNodeDiagnosis{}, Issues: []string{}}}
	f := newReleaseFixture(t, fg, twoGPUInv(), "stopped", nvidiaEncodeNodes)
	diag := doJSON(t, f.ts, f.cookie, "GET", "/api/v1/workloads/"+f.wl.ID+"/diagnostics/gpu", "")
	if diag.code != http.StatusOK || !strings.Contains(diag.body, `"applied_matches_saved":false`) {
		t.Fatalf("drift must be reported: %d %s", diag.code, diag.body)
	}
	fg.diag.Saved = append(append([]string{}, nvidiaEncodeNodes...), amdRenderNodes...)
	diag = doJSON(t, f.ts, f.cookie, "GET", "/api/v1/workloads/"+f.wl.ID+"/diagnostics/gpu", "")
	if !strings.Contains(diag.body, `"applied_matches_saved":true`) {
		t.Fatalf("matching devices: %s", diag.body)
	}
}

func TestReapplyOutcomeFailsWhenHostDeviceMissing(t *testing.T) {
	for _, running := range []bool{true, false} {
		guest := lxc.GuestNodePresent
		if !running {
			guest = lxc.GuestNodeNotRunning
		}
		d := lxc.GPUDiagnosis{Running: running, ConfigPresent: true, ConfigCurrent: true, Nodes: diagNodes(guest), DevicesMissing: true}
		d.Nodes[1].HostRule = ""
		if got, msg := reapplyOutcome(d); got != gpuReapplyFailed || !strings.Contains(msg, "/dev/nvidia1 is missing on the host") {
			t.Fatalf("running=%v: %s %q", running, got, msg)
		}
	}
	optional := lxc.GPUDiagnosis{Running: true, ConfigPresent: true, ConfigCurrent: true, Nodes: diagNodes(lxc.GuestNodePresent)}
	optional.Nodes[4].HostRule = ""
	optional.Nodes[4].Optional = true
	if got, _ := reapplyOutcome(optional); got != gpuReapplyApplied {
		t.Fatalf("missing optional node must not fail: %s", got)
	}
}
