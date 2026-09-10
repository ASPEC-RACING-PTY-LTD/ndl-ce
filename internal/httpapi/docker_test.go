package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/no-dal/ndl-ce/internal/appdb"
	"github.com/no-dal/ndl-ce/internal/docker"
	"github.com/no-dal/ndl-ce/internal/features"
	"github.com/no-dal/ndl-ce/internal/hostos"
	"github.com/no-dal/ndl-ce/internal/inventory"
)

type fakeDocker struct {
	inv     docker.Inventory
	actions []docker.ActionRequest
	idle    int
}

func (f *fakeDocker) DockerSnapshot(context.Context, []docker.MachineHint) (docker.Inventory, error) {
	return f.inv, nil
}
func (f *fakeDocker) DockerAction(_ context.Context, req docker.ActionRequest) (docker.ActionResult, error) {
	f.actions = append(f.actions, req)
	if req.Action == "logs" {
		return docker.ActionResult{OK: true, Logs: "line1\n"}, nil
	}
	return docker.ActionResult{OK: true, Message: req.Action}, nil
}
func (f *fakeDocker) DockerIdle(context.Context) error {
	f.idle++
	return nil
}

func TestDockerAPIDisabledByDefault(t *testing.T) {
	s, mem, token := testServer(t)
	s.Docker = &fakeDocker{inv: docker.Inventory{Containers: []docker.Container{{Ref: "host/abc"}}}}
	cluster, _ := mem.GetCluster(context.Background())
	seedNode(t, mem, cluster.ID, debianInv(), false)
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)
	req, _ := http.NewRequest("GET", ts.URL+"/api/v1/docker", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ := ts.Client().Do(req)
	raw, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusNotFound {
		t.Fatalf("disabled GET %d %s", res.StatusCode, raw)
	}
}

func TestDockerAPIEnabledSnapshotAndAction(t *testing.T) {
	s, mem, token := testServer(t)
	fu := &fakeUpdate{supported: true}
	s.Update = fu
	fd := &fakeDocker{inv: docker.Inventory{
		Summary:    docker.Summary{Machines: 1, Containers: 1, Healthy: 1},
		Machines:   []docker.Machine{{ID: "host", Name: "Host", Kind: "host", Health: "healthy", DaemonOK: true}},
		Containers: []docker.Container{{Ref: "host/abc123abc123", MachineID: "host", EngineID: "abc123abc123", Name: "web", State: "running", Health: "healthy", StatusLabel: "Running"}},
	}}
	s.Docker = fd
	cluster, _ := mem.GetCluster(context.Background())
	inv := debianInv()
	inv.Memory = inventory.Memory{TotalBytes: 16 << 30}
	seedNode(t, mem, cluster.ID, inv, false)
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)

	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/features/docker/enable", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ := ts.Client().Do(req)
	raw, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("enable %d %s", res.StatusCode, raw)
	}
	if len(fu.calls) != 1 || fu.calls[0].Action != hostos.UpdateFeatureInstall || fu.calls[0].PackageName != "nodal-feature-docker" {
		t.Fatalf("install %+v", fu.calls)
	}

	req, _ = http.NewRequest("GET", ts.URL+"/api/v1/docker", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(req)
	raw, _ = io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("get %d %s", res.StatusCode, raw)
	}
	if !strings.Contains(string(raw), `"web"`) {
		t.Fatalf("inventory %s", raw)
	}

	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/docker/machines/host/containers/abc123abc123/actions", strings.NewReader(`{"action":"restart"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(req)
	raw, _ = io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("restart %d %s", res.StatusCode, raw)
	}
	if len(fd.actions) != 1 || fd.actions[0].Action != "restart" {
		t.Fatalf("actions %+v", fd.actions)
	}

	req, _ = http.NewRequest("GET", ts.URL+"/api/v1/docker/machines/host/containers/abc123abc123/logs", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(req)
	raw, _ = io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK || !strings.Contains(string(raw), "line1") {
		t.Fatalf("logs %d %s", res.StatusCode, raw)
	}

	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/docker/machines/host/containers/abc123abc123/terminal/sessions", strings.NewReader(`{"cwd":"/"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(req)
	raw, _ = io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("term %d %s", res.StatusCode, raw)
	}
	var sess map[string]any
	if err := json.Unmarshal(raw, &sess); err != nil {
		t.Fatal(err)
	}
	if sess["target_kind"] != appdb.IOTargetDocker || sess["target_id"] != "host/abc123abc123" {
		t.Fatalf("session %s", raw)
	}

	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/features/docker/disable", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(req)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("disable without confirm %d", res.StatusCode)
	}
	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/features/docker/disable", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Nodal-Confirm", "disable-feature")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(req)
	raw, _ = io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("disable %d %s", res.StatusCode, raw)
	}
	if fd.idle != 1 {
		t.Fatalf("idle calls %d", fd.idle)
	}
	row, _ := mem.GetFeature(context.Background(), cluster.ID, features.IDDocker)
	if row == nil || row.Enabled {
		t.Fatal("docker still enabled")
	}
}
