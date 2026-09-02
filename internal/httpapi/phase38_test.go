package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/no-dal/ndl-ce/internal/hostos"
	"github.com/no-dal/ndl-ce/internal/inventory"
	"github.com/no-dal/ndl-ce/internal/k8s"
)

func TestPhase38DefaultHasNoKubeProcessAndVMsDoNotNeedIt(t *testing.T) {
	s, mem, token := testServer(t)
	s.K8sProcs = func() []string { return []string{"ndl-control", "qemu-system-x86_64"} }
	cluster, _ := mem.GetCluster(context.Background())
	inv := debianInv()
	inv.Memory = inventory.Memory{TotalBytes: 16 << 30}
	seedNode(t, mem, cluster.ID, inv, false)
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)

	req, _ := http.NewRequest("GET", ts.URL+"/api/v1/kubernetes", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ := ts.Client().Do(req)
	raw, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status %d %s", res.StatusCode, raw)
	}
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatal(err)
	}
	if body["kube_process"] != false || body["kubelet_started"] != false || body["enabled"] != false {
		t.Fatalf("%s", raw)
	}
	if body["vm_requires_k8s"] != false || body["ct_requires_k8s"] != false {
		t.Fatalf("%s", raw)
	}

	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/kubernetes/start", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(confirmHeader, k8s.StartConfirm)
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(req)
	raw, _ = io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusUnprocessableEntity || !strings.Contains(string(raw), "enable") {
		t.Fatalf("start disabled %d %s", res.StatusCode, raw)
	}

	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/features/kubernetes/enable", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(req)
	raw, _ = io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("enable kubernetes alias %d %s", res.StatusCode, raw)
	}
	var feat map[string]any
	if err := json.Unmarshal(raw, &feat); err != nil {
		t.Fatal(err)
	}
	if feat["id"] != "k8s" || feat["kubelet_started"] != false {
		t.Fatalf("enable must not start kubelet %s", raw)
	}

	fu := &fakeUpdate{}
	s.Update = fu
	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/kubernetes/start", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(confirmHeader, k8s.StartConfirm)
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(req)
	raw, _ = io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("start %d %s", res.StatusCode, raw)
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatal(err)
	}
	if body["kubelet_started"] != false || body["kube_process"] != false {
		t.Fatalf("unsupported host must not claim kubelet started %s", raw)
	}
	if len(fu.calls) != 1 || fu.calls[0].Action != hostos.UpdateK8sRuntimeStart {
		t.Fatalf("calls %+v", fu.calls)
	}

	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/kubernetes/stop", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(req)
	raw, _ = io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK || (!strings.Contains(string(raw), "Virtual machines") && !strings.Contains(string(raw), "not stopped")) {
		t.Fatalf("stop %d %s", res.StatusCode, raw)
	}
}

type claimStartUpdate struct {
	calls []hostos.UpdateRequest
}

func (c *claimStartUpdate) HostUpdate(_ context.Context, req hostos.UpdateRequest) (hostos.UpdateResult, error) {
	c.calls = append(c.calls, req)
	return hostos.UpdateResult{Supported: true, Status: "ok", Reason: "typed start recorded"}, nil
}

func TestPhase38StartDoesNotInventKubeProcess(t *testing.T) {
	s, mem, token := testServer(t)
	s.K8sProcs = func() []string { return []string{"ndl-control", "qemu-system-x86_64"} }
	fu := &claimStartUpdate{}
	s.Update = fu
	cluster, _ := mem.GetCluster(context.Background())
	inv := debianInv()
	inv.Memory = inventory.Memory{TotalBytes: 16 << 30}
	seedNode(t, mem, cluster.ID, inv, false)
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)

	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/features/k8s/enable", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ := ts.Client().Do(req)
	raw, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("enable %d %s", res.StatusCode, raw)
	}

	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/kubernetes/start", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(confirmHeader, k8s.StartConfirm)
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(req)
	raw, _ = io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("start %d %s", res.StatusCode, raw)
	}
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatal(err)
	}
	if body["kubelet_started"] != false || body["kube_process"] != false {
		t.Fatalf("supported updater must not invent kubelet %s", raw)
	}
	if len(fu.calls) < 1 || fu.calls[len(fu.calls)-1].Action != hostos.UpdateK8sRuntimeStart {
		t.Fatalf("calls %+v", fu.calls)
	}
	for _, c := range fu.calls {
		if c.Action == hostos.UpdateK8sRuntimeStart && c.PackageName != "" {
			t.Fatalf("start must not name a package %+v", c)
		}
	}
}
