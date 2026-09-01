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
	"github.com/no-dal/ndl-ce/internal/features"
	"github.com/no-dal/ndl-ce/internal/hostos"
	"github.com/no-dal/ndl-ce/internal/inventory"
	"github.com/no-dal/ndl-ce/internal/rbac"
)

func TestPhase35FreshInstallLightAndK8sNeedsConfirm(t *testing.T) {
	s, mem, token := testServer(t)
	fu := &fakeUpdate{supported: true}
	s.Update = fu
	cluster, _ := mem.GetCluster(context.Background())
	inv := debianInv()
	inv.Memory = inventory.Memory{TotalBytes: 2 << 30}
	seedNode(t, mem, cluster.ID, inv, false)
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)

	req, _ := http.NewRequest("GET", ts.URL+"/api/v1/features", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ := ts.Client().Do(req)
	raw, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("list %d %s", res.StatusCode, raw)
	}
	var listed map[string]any
	if err := json.Unmarshal(raw, &listed); err != nil {
		t.Fatal(err)
	}
	if listed["base_install"] != "light" || listed["gpu_optional"] != true {
		t.Fatalf("%s", raw)
	}
	lower := strings.ToLower(string(raw))
	if strings.Contains(lower, "apt-get") || strings.Contains(lower, "kubeadm") || strings.Contains(lower, `"kubelet_started": true`) {
		t.Fatalf("leaked runtime: %s", raw)
	}

	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/features/k8s/enable", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(req)
	raw, _ = io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("tiny k8s without confirm %d %s", res.StatusCode, raw)
	}
	if len(fu.calls) != 0 {
		t.Fatalf("must not install packages without confirm: %+v", fu.calls)
	}
	row, _ := mem.GetFeature(context.Background(), cluster.ID, features.IDK8s)
	if row != nil && row.Enabled {
		t.Fatal("k8s enabled without confirm")
	}

	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/features/k8s/enable", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Nodal-Confirm", "enable-k8s")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(req)
	raw, _ = io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("k8s confirm %d %s", res.StatusCode, raw)
	}
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatal(err)
	}
	if body["kubelet_started"] != false || body["starts_runtime"] != false || body["enabled"] != true {
		t.Fatalf("%s", raw)
	}
	if len(fu.calls) != 1 || fu.calls[0].Action != hostos.UpdateFeatureInstall || fu.calls[0].PackageName != "nodal-feature-k8s" {
		t.Fatalf("typed update %+v", fu.calls)
	}
}

func TestPhase35DisableLeavesWorkloads(t *testing.T) {
	s, mem, token := testServer(t)
	fu := &fakeUpdate{supported: true}
	s.Update = fu
	cluster, _ := mem.GetCluster(context.Background())
	seedNode(t, mem, cluster.ID, debianInv(), false)
	if err := mem.CreateWorkload(context.Background(), appdb.Workload{
		ID: uuid.NewString(), ClusterID: cluster.ID, Name: "app", Kind: "oci", Status: "running",
	}); err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)

	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/features/oci/enable", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ := ts.Client().Do(req)
	raw, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("enable oci %d %s", res.StatusCode, raw)
	}

	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/features/oci/disable", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(req)
	raw, _ = io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("disable without confirm %d %s", res.StatusCode, raw)
	}
	wls, _ := mem.ListWorkloads(context.Background(), cluster.ID)
	if len(wls) != 1 || wls[0].Kind != "oci" {
		t.Fatalf("workload deleted without confirm %+v", wls)
	}

	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/features/oci/disable", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Nodal-Confirm", "disable-feature")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(req)
	raw, _ = io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("disable confirm %d %s", res.StatusCode, raw)
	}
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatal(err)
	}
	if body["enabled"] != false || body["workload_count"] != float64(1) {
		t.Fatalf("%s", raw)
	}
	wls, _ = mem.ListWorkloads(context.Background(), cluster.ID)
	if len(wls) != 1 {
		t.Fatal("disable deleted workloads")
	}
}

func TestPhase35ViewerCannotEnableAndCoreStays(t *testing.T) {
	s, mem, token := testServer(t)
	s.Update = &fakeUpdate{supported: true}
	cluster, _ := mem.GetCluster(context.Background())
	seedNode(t, mem, cluster.ID, debianInv(), false)
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)

	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/features/vm/disable", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ := ts.Client().Do(req)
	if res.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("disable vm %d", res.StatusCode)
	}
	_ = res.Body.Close()

	hash, _ := auth.HashPassword("password1")
	view := appdb.User{ID: uuid.NewString(), ClusterID: cluster.ID, Username: "view35", PasswordHash: hash}
	_ = mem.CreateUser(context.Background(), view)
	_ = mem.BindRole(context.Background(), cluster.ID, view.ID, rbac.Viewer)
	login, _ := ts.Client().Post(ts.URL+"/api/v1/auth/login", "application/json", strings.NewReader(`{"username":"view35","password":"password1"}`))
	var viewCookie string
	for _, c := range login.Cookies() {
		if c.Name == sessionCookie {
			viewCookie = c.Value
		}
	}
	_ = login.Body.Close()
	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/features/gpu/enable", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: viewCookie})
	res, _ = ts.Client().Do(req)
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("viewer enable %d", res.StatusCode)
	}
	_ = res.Body.Close()
}

func TestPhase35GPUEnableDoesNotClaimRuntime(t *testing.T) {
	s, mem, token := testServer(t)
	fu := &fakeUpdate{supported: false}
	s.Update = fu
	cluster, _ := mem.GetCluster(context.Background())
	seedNode(t, mem, cluster.ID, debianInv(), false)
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)
	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/features/gpu/enable", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ := ts.Client().Do(req)
	raw, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("gpu %d %s", res.StatusCode, raw)
	}
	if strings.Contains(strings.ToLower(string(raw)), "nvidia") && strings.Contains(strings.ToLower(string(raw)), `"package_status":"installed"`) {
		t.Fatalf("gpu must stay optional/unavailable here: %s", raw)
	}
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatal(err)
	}
	if body["starts_runtime"] != false || body["package_status"] != "unavailable" {
		t.Fatalf("%s", raw)
	}
}
