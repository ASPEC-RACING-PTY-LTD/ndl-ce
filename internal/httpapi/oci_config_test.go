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
	"github.com/no-dal/ndl-ce/internal/oci"
)

type fakeOCIUpdater struct {
	*fakeOCI
	updated []oci.Spec
}

func (f *fakeOCIUpdater) UpdateOCI(_ context.Context, spec oci.Spec) (oci.Result, error) {
	f.updated = append(f.updated, spec)
	return oci.Result{WorkloadID: spec.WorkloadID, ImageDigest: "sha256:new", Status: oci.StatusRunning, Health: oci.Health{Status: oci.StatusCollecting}}, nil
}

func ociReady(t *testing.T) (*appdb.Memory, *httptest.Server, string, string, string, *fakeOCIUpdater) {
	t.Helper()
	s, mem, token := testServer(t)
	cluster, _ := mem.GetCluster(context.Background())
	nodeID := uuid.NewString()
	_ = mem.UpsertNode(context.Background(), appdb.Node{ID: nodeID, ClusterID: cluster.ID, Name: "local"})
	_, netID := seedCompute(t, mem, cluster.ID, nodeID)
	fo := &fakeOCIUpdater{fakeOCI: &fakeOCI{runtime: &oci.FakeRuntime{}}}
	s.OCI = fo
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close)
	cookie := claimAdmin(t, ts, token)
	return mem, ts, cookie, cluster.ID, netID, fo
}

func ociCall(t *testing.T, ts *httptest.Server, cookie, method, path, body string) (int, map[string]any) {
	t.Helper()
	req, _ := http.NewRequest(method, ts.URL+"/api/v1"+path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	out := map[string]any{}
	_ = json.Unmarshal(raw, &out)
	if out == nil {
		out = map[string]any{}
	}
	out["_raw"] = string(raw)
	return res.StatusCode, out
}

func TestOCICreateOnABridgeWithAFixedAddress(t *testing.T) {
	mem, ts, cookie, clusterID, netID, fo := ociReady(t)
	code, out := ociCall(t, ts, cookie, "POST", "/workloads",
		`{"name":"web","kind":"oci","image_pin":"nginx:alpine","network_id":"`+netID+`","ipv4_address":"10.9.0.5/24","ipv4_gateway":"10.9.0.1","dns":["1.1.1.1"],"ports":[{"container_port":80,"host_port":8080}],"env":[{"name":"A","value":"b"}]}`)
	if code != http.StatusCreated {
		t.Fatalf("create %d %v", code, out["_raw"])
	}
	spec := fo.lastSpec
	if spec.NetworkMode != oci.NetworkBridge || spec.BridgeName != "ndldeadbeef" || spec.IPv4Address != "10.9.0.5/24" || spec.IPv4Gateway != "10.9.0.1" {
		t.Fatalf("network not passed to the runtime: %+v", spec)
	}
	if len(spec.Ports) != 1 || spec.Ports[0].HostPort != 8080 || len(spec.Env) != 1 || len(spec.DNS) != 1 {
		t.Fatalf("ports, env or dns lost: %+v", spec)
	}
	nics, _ := mem.ListWorkloadNICs(context.Background(), clusterID, out["id"].(string))
	if len(nics) != 1 || nics[0].IPv4Mode != "static" || nics[0].IPv4 != "10.9.0.5" {
		t.Fatalf("nic %+v", nics)
	}
}

func TestOCIConfigChangesARunningContainer(t *testing.T) {
	mem, ts, cookie, clusterID, netID, fo := ociReady(t)
	code, out := ociCall(t, ts, cookie, "POST", "/workloads", `{"name":"app","kind":"oci","image_pin":"nginx:1"}`)
	if code != http.StatusCreated {
		t.Fatalf("create %d %v", code, out["_raw"])
	}
	id := out["id"].(string)
	code, out = ociCall(t, ts, cookie, "PUT", "/workloads/"+id+"/oci",
		`{"image_pin":"nginx:2","env":[{"name":"MODE","value":"prod"}],"ports":[{"container_port":80,"host_port":8081,"protocol":"tcp"}],"network_id":"`+netID+`","ipv4_mode":"dhcp","memory_bytes":536870912}`)
	if code != http.StatusOK {
		t.Fatalf("update %d %v", code, out["_raw"])
	}
	if len(fo.updated) != 1 {
		t.Fatalf("agent update not called")
	}
	got := fo.updated[0]
	if got.ImagePin != "nginx:2" || got.Env[0].Value != "prod" || got.Ports[0].HostPort != 8081 {
		t.Fatalf("spec %+v", got)
	}
	if got.NetworkMode != oci.NetworkBridge || got.IPv4Address != "" || got.BridgeName == "" {
		t.Fatalf("bridge with DHCP expected: %+v", got)
	}
	if got.Resources.MemoryBytes != 536870912 {
		t.Fatalf("memory %d", got.Resources.MemoryBytes)
	}
	wl, _ := mem.GetWorkload(context.Background(), clusterID, id)
	if wl.ImagePin != "nginx:2" || wl.MemoryBytes != 536870912 {
		t.Fatalf("stored workload not updated: %s %d", wl.ImagePin, wl.MemoryBytes)
	}
	// Fields left out keep their values.
	code, out = ociCall(t, ts, cookie, "PUT", "/workloads/"+id+"/oci", `{"cpus":2}`)
	if code != http.StatusOK {
		t.Fatalf("second update %d %v", code, out["_raw"])
	}
	again := fo.updated[1]
	if again.ImagePin != "nginx:2" || len(again.Env) != 1 || again.NetworkMode != oci.NetworkBridge || again.Resources.CPUs != 2 {
		t.Fatalf("partial update lost fields: %+v", again)
	}
}

func TestOCIConfigRejectsBadNetworks(t *testing.T) {
	_, ts, cookie, _, netID, _ := ociReady(t)
	_, out := ociCall(t, ts, cookie, "POST", "/workloads", `{"name":"app","kind":"oci","image_pin":"nginx:1"}`)
	id := out["id"].(string)
	for _, body := range []string{
		`{"network_mode":"bridge"}`,
		`{"network_id":"` + netID + `","ipv4_address":"not-an-ip"}`,
		`{"network_mode":"host","ports":[{"container_port":80,"host_port":81}]}`,
		`{"network_mode":"carrier-pigeon"}`,
	} {
		if code, out := ociCall(t, ts, cookie, "PUT", "/workloads/"+id+"/oci", body); code < 400 {
			t.Fatalf("%s accepted: %d %v", body, code, out["_raw"])
		}
	}
}

func memberWorkloads(g map[string]any) []string {
	var out []string
	members, _ := g["members"].([]any)
	for _, m := range members {
		row, _ := m.(map[string]any)
		if id, ok := row["workload_id"].(string); ok {
			out = append(out, id)
		}
	}
	return out
}

func TestOCIGroupsMoveContainersWithoutTouchingThem(t *testing.T) {
	mem, ts, cookie, clusterID, _, fo := ociReady(t)
	_, a := ociCall(t, ts, cookie, "POST", "/stacks", `{"name":"web"}`)
	_, b := ociCall(t, ts, cookie, "POST", "/stacks", `{"name":"db"}`)
	_, c := ociCall(t, ts, cookie, "POST", "/workloads", `{"name":"app","kind":"oci","image_pin":"nginx:1"}`)
	ga, gb, cid := a["id"].(string), b["id"].(string), c["id"].(string)

	code, out := ociCall(t, ts, cookie, "POST", "/stacks/"+ga+"/members", `{"workload_id":"`+cid+`"}`)
	if code != http.StatusOK || len(memberWorkloads(out)) != 1 {
		t.Fatalf("add %d %v", code, out["_raw"])
	}
	code, out = ociCall(t, ts, cookie, "POST", "/stacks/"+gb+"/members", `{"workload_id":"`+cid+`"}`)
	if code != http.StatusOK || memberWorkloads(out)[0] != cid {
		t.Fatalf("move %d %v", code, out["_raw"])
	}
	_, list := ociCall(t, ts, cookie, "GET", "/stacks?members=true", "")
	items, _ := list["items"].([]any)
	for _, it := range items {
		g := it.(map[string]any)
		ids := memberWorkloads(g)
		if g["id"] == ga && len(ids) != 0 {
			t.Fatalf("container still in the old group: %v", ids)
		}
		if g["id"] == gb && len(ids) != 1 {
			t.Fatalf("container missing from the new group: %v", ids)
		}
	}

	code, out = ociCall(t, ts, cookie, "POST", "/stacks/"+gb+"/power", `{"action":"stop"}`)
	if code != http.StatusOK || fo.lastLife.Action != "stop" || fo.lastLife.WorkloadID != cid {
		t.Fatalf("group stop %d %v %+v", code, out["_raw"], fo.lastLife)
	}

	members, _ := mem.ListStackMembers(context.Background(), clusterID, gb)
	code, out = ociCall(t, ts, cookie, "DELETE", "/stacks/"+gb+"/members/"+members[0].ID, "")
	if code != http.StatusOK || len(memberWorkloads(out)) != 0 {
		t.Fatalf("remove %d %v", code, out["_raw"])
	}
	code, _ = ociCall(t, ts, cookie, "DELETE", "/stacks/"+gb, "")
	if code != http.StatusOK {
		t.Fatalf("delete group %d", code)
	}
	if wl, _ := mem.GetWorkload(context.Background(), clusterID, cid); wl == nil {
		t.Fatal("grouping must never delete the container")
	}
}

func TestOCIGroupRefusesSystemContainers(t *testing.T) {
	mem, ts, cookie, clusterID, _, _ := ociReady(t)
	_, g := ociCall(t, ts, cookie, "POST", "/stacks", `{"name":"grp"}`)
	ct := appdb.Workload{ID: uuid.NewString(), ClusterID: clusterID, Name: "ct", Kind: "system-container"}
	_ = mem.CreateWorkload(context.Background(), ct)
	if code, out := ociCall(t, ts, cookie, "POST", "/stacks/"+g["id"].(string)+"/members", `{"workload_id":"`+ct.ID+`"}`); code != http.StatusConflict {
		t.Fatalf("%d %v", code, out["_raw"])
	}
}
