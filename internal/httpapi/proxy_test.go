package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/no-dal/ndl-ce/internal/docker"
	"github.com/no-dal/ndl-ce/internal/rbac"
)

type proxyDocker struct {
	dockerUnavailable
	mu   sync.Mutex
	reqs []docker.ProxyRequest
	fail error
}

func (d *proxyDocker) DockerProxy(_ context.Context, req docker.ProxyRequest) (docker.ProxyResult, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.reqs = append(d.reqs, req)
	if d.fail != nil {
		return docker.ProxyResult{Output: "line1\nError: bad"}, d.fail
	}
	if req.Action == docker.ProxyScan {
		return docker.ProxyResult{
			Instances: []docker.ProxyInstance{{Kind: req.Kind, MachineID: docker.HostMachineID, MachineName: "Host", Active: true, Sites: []docker.ProxySite{
				{Domains: []string{"a.example.com"}, Upstream: "http://10.0.0.9:80", Managed: true, Name: "a.example.com"},
			}}},
			Machines: []docker.ProxyMachine{{ID: docker.HostMachineID, Name: "Host", Installed: true, CanApt: true}},
		}, nil
	}
	return docker.ProxyResult{Message: "saved"}, nil
}

func TestProxyPagesAreGatedAndValidated(t *testing.T) {
	e := newCFEnv(t)
	fake := &proxyDocker{}
	e.s.Docker = fake
	if code, _, _ := e.call(e.admin, "GET", "/api/v1/proxies/caddy", ""); code != http.StatusNotFound {
		t.Fatalf("disabled feature must 404: %d", code)
	}
	if code, _, raw := e.call(e.admin, "POST", "/api/v1/features/caddy/enable", ""); code != 200 {
		t.Fatalf("enable %d %s", code, raw)
	}
	code, _, raw := e.call(e.admin, "GET", "/api/v1/proxies/caddy", "")
	if code != 200 || !strings.Contains(raw, "a.example.com") || !strings.Contains(raw, `"machines"`) {
		t.Fatalf("scan %d %s", code, raw)
	}
	// Cached: a second read does not rescan.
	n := len(fake.reqs)
	_, _, _ = e.call(e.admin, "GET", "/api/v1/proxies/caddy", "")
	if len(fake.reqs) != n {
		t.Fatal("scan was not cached")
	}
	if code, _, _ := e.call(e.admin, "GET", "/api/v1/proxies/nginx", ""); code != http.StatusNotFound {
		t.Fatalf("nginx is a separate feature: %d", code)
	}
	if code, _, _ := e.call(e.admin, "POST", "/api/v1/proxies/caddy/sites", `{"site":{"domains":["x.com }"],"upstream":"10.0.0.1:80"}}`); code != 400 {
		t.Fatalf("injection must be refused: %d", code)
	}
	if code, _, _ := e.call(e.admin, "POST", "/api/v1/proxies/caddy/sites", `{"machine_id":"nope","site":{"domains":["x.com"],"upstream":"10.0.0.1:80"}}`); code != 400 {
		t.Fatalf("unknown machine must be refused: %d", code)
	}
	if code, _, raw := e.call(e.admin, "POST", "/api/v1/proxies/caddy/sites", `{"site":{"domains":["X.com"],"upstream":"10.0.0.1:80"}}`); code != 200 {
		t.Fatalf("apply %d %s", code, raw)
	}
	last := fake.reqs[len(fake.reqs)-1]
	if last.Action != docker.ProxyApply || last.Site.Name != "x.com" || last.Site.Upstream != "http://10.0.0.1:80" || last.MachineID != docker.HostMachineID {
		t.Fatalf("forwarded %+v", last)
	}
	fake.fail = errors.New("caddy rejected the change")
	if code, _, raw := e.call(e.admin, "POST", "/api/v1/proxies/caddy/service", `{"action":"reload"}`); code != 422 || !strings.Contains(raw, "Error: bad") {
		t.Fatalf("failure %d %s", code, raw)
	}
	viewer := loginRole(t, e.ts, e.mem, "proxyviewer", rbac.Viewer)
	if code, _, _ := e.call(viewer, "POST", "/api/v1/proxies/caddy/service", `{"action":"reload"}`); code != http.StatusForbidden {
		t.Fatalf("viewer may not change proxies: %d", code)
	}
	// Turning the feature off hides it without touching the proxy.
	before := len(fake.reqs)
	if code, _, raw := e.call(e.admin, "POST", "/api/v1/features/caddy/disable", ""); code != 200 || !strings.Contains(raw, "nothing was changed") {
		t.Fatalf("disable %d %s", code, raw)
	}
	if len(fake.reqs) != before {
		t.Fatal("disabling must not call the agent")
	}
}
