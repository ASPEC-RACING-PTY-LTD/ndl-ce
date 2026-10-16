package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/no-dal/ndl-ce/internal/appdb"
	"github.com/no-dal/ndl-ce/internal/docker"
)

// opsDocker is a Docker agent whose containers come back healthy when
// started or restarted, with cloudflared tunnels and one-shot exec.
type opsDocker struct {
	mu       sync.Mutex
	inv      docker.Inventory
	actions  []string
	tunnels  []docker.Tunnel
	tunnelOp []string
	execs    [][]string
}

func (f *opsDocker) DockerSnapshot(context.Context, []docker.MachineHint) (docker.Inventory, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	inv := f.inv
	inv.Containers = append([]docker.Container(nil), f.inv.Containers...)
	return inv, nil
}

func (f *opsDocker) DockerAction(_ context.Context, req docker.ActionRequest) (docker.ActionResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := range f.inv.Containers {
		c := &f.inv.Containers[i]
		if c.EngineID == req.ContainerID {
			f.actions = append(f.actions, req.Action+" "+c.Name)
			switch req.Action {
			case "start", "restart":
				c.State, c.Health, c.ExitCode = "running", docker.HealthHealthy, 0
			case "stop":
				c.State = "exited"
			}
		}
	}
	return docker.ActionResult{OK: true}, nil
}

func (f *opsDocker) DockerIdle(context.Context) error { return nil }

func (f *opsDocker) DockerTunnels(context.Context, []docker.MachineHint) ([]docker.Tunnel, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]docker.Tunnel(nil), f.tunnels...), nil
}

func (f *opsDocker) DockerTunnelUnit(_ context.Context, machineID, unit, action string, _ bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.tunnelOp = append(f.tunnelOp, action+" "+machineID+" "+unit)
	for i := range f.tunnels {
		if f.tunnels[i].MachineID == machineID && f.tunnels[i].Unit == unit {
			f.tunnels[i].Active, f.tunnels[i].State = true, "active"
		}
	}
	return nil
}

func (f *opsDocker) DockerExecRun(_ context.Context, machineID, containerID string, argv []string, _ int) (docker.ExecResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.execs = append(f.execs, append([]string{machineID + "/" + containerID}, argv...))
	return docker.ExecResult{Output: "migrated 3 tables", ExitCode: 0}, nil
}

func (f *opsDocker) done() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.actions...)
}

func shopInventory() docker.Inventory {
	cs := []docker.Container{
		{Ref: "ct-1/d1", MachineID: "ct-1", MachineName: "apps", EngineID: "d1", Name: "shop-db-1", Project: "shop", Service: "db", State: "running", Health: docker.HealthHealthy},
		{Ref: "ct-1/c1", MachineID: "ct-1", MachineName: "apps", EngineID: "c1", Name: "shop-cache-1", Project: "shop", Service: "cache", State: "running", Health: docker.HealthHealthy,
			Labels: map[string]string{docker.LabelDependsOn: "db:service_healthy:false"}},
		{Ref: "ct-1/w1", MachineID: "ct-1", MachineName: "apps", EngineID: "w1", Name: "shop-web-1", Project: "shop", Service: "web", State: "exited", ExitCode: 1, Health: docker.HealthDegraded,
			Labels: map[string]string{docker.LabelDependsOn: "db:service_healthy:false,cache:service_started:true"},
			Ports:  []docker.Port{{PrivatePort: 80, PublicPort: 8080}}},
		{Ref: "ct-1/x1", MachineID: "ct-1", MachineName: "apps", EngineID: "x1", Name: "blog-app-1", Project: "blog", Service: "app", State: "running", Health: docker.HealthCritical},
	}
	return docker.Inventory{
		Machines:   []docker.Machine{{ID: "ct-1", Name: "apps", Kind: "system-container", DaemonOK: true, IPv4: "10.0.0.5"}},
		Containers: cs,
	}
}

func opsReady(t *testing.T) (*appdb.Memory, *httptest.Server, string, string, *opsDocker) {
	t.Helper()
	old := opsPoll
	opsPoll = 10 * time.Millisecond
	t.Cleanup(func() { opsPoll = old })
	s, mem, token := testServer(t)
	s.Update = &fakeUpdate{supported: true}
	fd := &opsDocker{inv: shopInventory(), tunnels: []docker.Tunnel{
		{MachineID: "ct-1", MachineName: "apps", Unit: "cloudflared.service", Active: false, State: "failed", SubState: "failed",
			Ingress: []docker.IngressRule{{Hostname: "shop.example.com", Service: "http://localhost:8080"}}},
	}}
	s.Docker = fd
	cluster, _ := mem.GetCluster(context.Background())
	seedNode(t, mem, cluster.ID, debianInv(), false)
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close)
	cookie := claimAdmin(t, ts, token)
	if code, out := ociCall(t, ts, cookie, "POST", "/features/docker/enable", `{}`); code != http.StatusOK {
		t.Fatalf("enable %d %v", code, out["_raw"])
	}
	return mem, ts, cookie, cluster.ID, fd
}

func waitJob(t *testing.T, ts *httptest.Server, cookie, id string) map[string]any {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		_, out := ociCall(t, ts, cookie, "GET", "/ops/jobs/"+id, "")
		if out["status"] != "running" {
			return out
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("job %s did not finish", id)
	return nil
}

func jobText(job map[string]any) string {
	var b strings.Builder
	lines, _ := job["lines"].([]any)
	for _, l := range lines {
		row, _ := l.(map[string]any)
		b.WriteString(row["level"].(string) + ": " + row["message"].(string) + "\n")
	}
	return b.String()
}

func TestDockerFixRestartsDependenciesFirstAndStartsTheTunnel(t *testing.T) {
	mem, ts, cookie, clusterID, fd := opsReady(t)
	_ = mem.PutDockerPref(context.Background(), appdb.DockerPref{ClusterID: clusterID, MachineID: "ct-1", Scope: "project", Name: "blog", Ignored: true})
	code, out := ociCall(t, ts, cookie, "POST", "/docker/bulk", `{"action":"fix"}`)
	if code != http.StatusAccepted {
		t.Fatalf("bulk %d %v", code, out["_raw"])
	}
	job := waitJob(t, ts, cookie, out["id"].(string))
	got := fd.done()
	want := []string{"restart shop-db-1", "restart shop-cache-1", "start shop-web-1"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("order %v, want %v\n%s", got, want, jobText(job))
	}
	for _, a := range got {
		if strings.Contains(a, "blog") {
			t.Fatal("an ignored group must not be touched by Fix")
		}
	}
	if len(fd.tunnelOp) != 1 || !strings.Contains(fd.tunnelOp[0], "cloudflared.service") {
		t.Fatalf("the stopped tunnel must be started: %v", fd.tunnelOp)
	}
	if job["status"] != "succeeded" {
		t.Fatalf("job %v\n%s", job["status"], jobText(job))
	}
}

func TestDockerStopAllStopsDependentsFirst(t *testing.T) {
	_, ts, cookie, _, fd := opsReady(t)
	code, out := ociCall(t, ts, cookie, "POST", "/docker/bulk", `{"action":"stop-all","project":"shop"}`)
	if code != http.StatusAccepted {
		t.Fatalf("bulk %d %v", code, out["_raw"])
	}
	waitJob(t, ts, cookie, out["id"].(string))
	got := strings.Join(fd.done(), ",")
	if got != "stop shop-cache-1,stop shop-db-1" {
		t.Fatalf("stop order %s (web was already stopped, db must stop last)", got)
	}
}

func TestDockerScriptTestRunsNothingAndRunExecutes(t *testing.T) {
	_, ts, cookie, _, fd := opsReady(t)
	script := `{"script":{"name":"deploy","steps":[` +
		`{"action":"restart","machine":"apps","container":"shop-db-1"},` +
		`{"action":"exec","machine":"apps","container":"shop-db-1","command":["sh","-c","migrate"]},` +
		`{"action":"wait","seconds":1},` +
		`{"action":"tunnel-restart","machine":"apps"}]}}`
	code, out := ociCall(t, ts, cookie, "POST", "/docker/script", strings.Replace(script, `{"script"`, `{"dry_run":true,"script"`, 1))
	if code != http.StatusOK || out["valid"] != true {
		t.Fatalf("test %d %v", code, out["_raw"])
	}
	if len(fd.done()) != 0 || len(fd.execs) != 0 {
		t.Fatal("Test must not change anything")
	}
	if !strings.Contains(jobText(out), "Would restart shop-db-1") {
		t.Fatalf("plan:\n%s", jobText(out))
	}
	code, out = ociCall(t, ts, cookie, "POST", "/docker/script", script)
	if code != http.StatusAccepted {
		t.Fatalf("run %d %v", code, out["_raw"])
	}
	job := waitJob(t, ts, cookie, out["id"].(string))
	if job["status"] != "succeeded" || len(fd.execs) != 1 || fd.execs[0][3] != "migrate" {
		t.Fatalf("run %v %v\n%s", job["status"], fd.execs, jobText(job))
	}
	if !strings.Contains(jobText(job), "migrated 3 tables") {
		t.Fatalf("exec output must be in the job log:\n%s", jobText(job))
	}
	for _, bad := range []string{
		`{"script":{"steps":[{"action":"format-disk"}]}}`,
		`{"script":{"steps":[{"action":"restart"}]}}`,
		`{"script":{"steps":[{"action":"tunnel-restart","machine":"apps","unit":"sshd.service"}]}}`,
		`{"script":{"steps":[],"extra":1}}`,
	} {
		if code, out := ociCall(t, ts, cookie, "POST", "/docker/script", bad); code != http.StatusUnprocessableEntity {
			t.Fatalf("%s accepted: %d %v", bad, code, out["_raw"])
		}
	}
}

func TestDockerTunnelsAreLinkedToContainers(t *testing.T) {
	_, ts, cookie, _, _ := opsReady(t)
	code, out := ociCall(t, ts, cookie, "GET", "/docker/tunnels", "")
	if code != http.StatusOK || out["supported"] != true {
		t.Fatalf("tunnels %d %v", code, out["_raw"])
	}
	items := out["items"].([]any)
	tun := items[0].(map[string]any)
	if tun["health"] != docker.HealthCritical {
		t.Fatalf("a failed tunnel is critical: %v", tun)
	}
	links, _ := tun["links"].([]any)
	if len(links) != 1 || links[0].(map[string]any)["name"] != "shop-web-1" {
		t.Fatalf("localhost:8080 must link to shop-web-1: %v", tun["links"])
	}
}
