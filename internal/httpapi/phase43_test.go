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
	"github.com/no-dal/ndl-ce/internal/auth"
	"github.com/no-dal/ndl-ce/internal/license"
	"github.com/no-dal/ndl-ce/internal/rbac"
)

type fakeLicense struct {
	err error
	hit int
}

func (f *fakeLicense) Check(context.Context, string) error {
	f.hit++
	return f.err
}

func TestPhase43LicenseAbsentDoesNothing(t *testing.T) {
	s, mem, token := testServer(t)
	cluster, _ := mem.GetCluster(context.Background())
	node := seedNode(t, mem, cluster.ID, debianInv(), false)
	probe := &fakeLicense{}
	s.LicenseProbe = probe
	_ = mem.CreateWorkload(context.Background(), appdb.Workload{
		ID: uuid.NewString(), ClusterID: cluster.ID, NodeID: node.ID, Name: "keep", Kind: "vm", Status: "running",
	})
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)

	req, _ := http.NewRequest("GET", ts.URL+"/api/v1/settings/license", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ := ts.Client().Do(req)
	raw, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK || !strings.Contains(string(raw), `"status":"absent"`) {
		t.Fatalf("absent %d %s", res.StatusCode, raw)
	}
	if strings.Contains(string(raw), `"workloads_stopped":true`) || strings.Contains(string(raw), `"ee_blobs":true`) {
		t.Fatalf("%s", raw)
	}
	if probe.hit != 0 {
		t.Fatal("must not contact licensing API without a key")
	}

	req, _ = http.NewRequest("GET", ts.URL+"/api/v1/health", nil)
	res, _ = ts.Client().Do(req)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("health %d", res.StatusCode)
	}
	req, _ = http.NewRequest("GET", ts.URL+"/api/v1/workloads", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(req)
	raw, _ = io.ReadAll(res.Body)
	_ = res.Body.Close()
	if !strings.Contains(string(raw), `"status":"running"`) {
		t.Fatalf("workloads %s", raw)
	}
}

func TestPhase43UnreachableGraceKeepsWorkloads(t *testing.T) {
	s, mem, token := testServer(t)
	cluster, _ := mem.GetCluster(context.Background())
	node := seedNode(t, mem, cluster.ID, debianInv(), false)
	probe := &fakeLicense{err: errors.New("dial tcp")}
	s.LicenseProbe = probe
	wlID := "eeeeeeee-eeee-4eee-8eee-eeeeeeeeeeee"
	_ = mem.CreateWorkload(context.Background(), appdb.Workload{
		ID: wlID, ClusterID: cluster.ID, NodeID: node.ID, Name: "keep", Kind: "vm", Status: "running",
	})
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)

	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/settings/license", strings.NewReader(`{"key":"EE-SECRET-LICENSE-VALUE"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ := ts.Client().Do(req)
	raw, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("confirm %d %s", res.StatusCode, raw)
	}

	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/settings/license", strings.NewReader(`{"key":"EE-SECRET-LICENSE-VALUE"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(confirmHeader, license.ActivateConfirm)
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(req)
	raw, _ = io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("activate %d %s", res.StatusCode, raw)
	}
	if strings.Contains(string(raw), "EE-SECRET-LICENSE-VALUE") {
		t.Fatalf("key leaked %s", raw)
	}
	if !strings.Contains(string(raw), `"workloads_stopped":false`) || !strings.Contains(string(raw), `"has_key":true`) {
		t.Fatalf("%s", raw)
	}
	if probe.hit != 1 {
		t.Fatalf("hits %d", probe.hit)
	}

	req, _ = http.NewRequest("GET", ts.URL+"/api/v1/workloads", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(req)
	raw, _ = io.ReadAll(res.Body)
	_ = res.Body.Close()
	if !strings.Contains(string(raw), `"status":"running"`) {
		t.Fatalf("must not stop workloads %s", raw)
	}

	hash, err := auth.HashPassword("password1")
	if err != nil {
		t.Fatal(err)
	}
	u := appdb.User{ID: uuid.NewString(), ClusterID: cluster.ID, Username: "view", PasswordHash: hash}
	_ = mem.CreateUser(context.Background(), u)
	_ = mem.BindRole(context.Background(), cluster.ID, u.ID, rbac.Viewer)
	login, err := ts.Client().Post(ts.URL+"/api/v1/auth/login", "application/json", strings.NewReader(`{"username":"view","password":"password1"}`))
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
	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/settings/license", strings.NewReader(`{"key":"x"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(confirmHeader, license.ActivateConfirm)
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: viewCookie})
	res, _ = ts.Client().Do(req)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("viewer activate %d", res.StatusCode)
	}

	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/settings/license/clear", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(confirmHeader, license.ClearConfirm)
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(req)
	raw, _ = io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK || !strings.Contains(string(raw), `"status":"absent"`) {
		t.Fatalf("clear %d %s", res.StatusCode, raw)
	}
	if strings.Contains(string(raw), `"has_key":true`) || strings.Contains(string(raw), `"workloads_stopped":true`) {
		t.Fatalf("clear %s", raw)
	}
}
