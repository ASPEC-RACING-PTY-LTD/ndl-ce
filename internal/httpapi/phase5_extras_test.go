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
	"time"

	"github.com/google/uuid"
	"github.com/no-dal/ndl-ce/internal/appdb"
	"github.com/no-dal/ndl-ce/internal/lxc"
	"github.com/no-dal/ndl-ce/internal/storage"
)

func TestWorkloadCreateExtrasComposeSelectsDockerNesting(t *testing.T) {
	s, mem, token := testServer(t)
	cluster, _ := mem.GetCluster(context.Background())
	nodeID := uuid.NewString()
	_ = mem.UpsertNode(context.Background(), appdb.Node{ID: nodeID, ClusterID: cluster.ID, Name: "local"})
	poolID, netID := seedCompute(t, mem, cluster.ID, nodeID)
	fw := &fakeWorkloads{}
	s.Workloads = fw
	s.Storage = fakeStorage{
		vol: storage.CreateVolumeResult{Handle: storage.VolumeHandle{
			BackendType: storage.BackendDirectory, BackendRef: "volumes/container-root/x",
			Kind: storage.KindFilesystem, Class: storage.ClassContainerRoot, Format: storage.FormatDirectory,
		}},
	}
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)
	body := `{"name":"extras-dock","kind":"system-container","image_pin":"alpine/3.21/amd64/default","pool_id":"` + poolID + `","network_id":"` + netID + `","extras":["compose"]}`
	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/workloads", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("create %d %s", res.StatusCode, b)
	}
	var created map[string]any
	_ = json.Unmarshal(b, &created)
	waitExtras(t, s, created["id"].(string))
	if fw.lastSpec.Nesting == nil || !*fw.lastSpec.Nesting {
		t.Fatal("docker extras must request nesting")
	}
	if fw.setups != 1 {
		t.Fatalf("guest-setup calls %d", fw.setups)
	}
	if len(fw.lastLife.Extras) < 2 {
		t.Fatalf("expected docker+compose extras, got %v", fw.lastLife.Extras)
	}
	if len(fw.deleted) != 0 {
		t.Fatalf("workload deleted after extras: %v", fw.deleted)
	}
}

func TestWorkloadCreateKeepsWorkloadWhenExtrasFail(t *testing.T) {
	s, mem, token := testServer(t)
	cluster, _ := mem.GetCluster(context.Background())
	nodeID := uuid.NewString()
	_ = mem.UpsertNode(context.Background(), appdb.Node{ID: nodeID, ClusterID: cluster.ID, Name: "local"})
	poolID, netID := seedCompute(t, mem, cluster.ID, nodeID)
	fw := &fakeWorkloads{
		setupErr:      errors.New("apk add docker failed"),
		setupWarnings: []lxc.SetupWarning{{Extra: "docker", Message: "package install failed"}},
	}
	s.Workloads = fw
	s.Storage = fakeStorage{
		vol: storage.CreateVolumeResult{Handle: storage.VolumeHandle{
			BackendType: storage.BackendDirectory, BackendRef: "volumes/container-root/y",
			Kind: storage.KindFilesystem, Class: storage.ClassContainerRoot, Format: storage.FormatDirectory,
		}},
	}
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)
	body := `{"name":"extras-fail","kind":"system-container","image_pin":"alpine/3.21/amd64/default","pool_id":"` + poolID + `","network_id":"` + netID + `","extras":["docker"],"codehold_cli":false}`
	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/workloads", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("create must succeed when extras fail: %d %s", res.StatusCode, b)
	}
	var got map[string]any
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	if got["setup_status"] != "installing" || got["setup_operation_id"] == "" {
		t.Fatalf("extras must install in the background: %s", b)
	}
	id, _ := got["id"].(string)
	if id == "" {
		t.Fatal("missing workload id")
	}
	waitExtras(t, s, id)
	exts, _ := mem.ListWorkloadExtensions(context.Background(), cluster.ID, id)
	if len(exts) != 1 || exts[0].ExtraID != "docker" || exts[0].State != appdb.ExtensionFailed || exts[0].Message != "package install failed" {
		t.Fatalf("the failure and its reason must be recorded per extension: %+v", exts)
	}
	row, err := mem.GetWorkload(context.Background(), cluster.ID, id)
	if err != nil || row == nil {
		t.Fatal("workload must remain after extra failure")
	}
	if len(fw.deleted) != 0 {
		t.Fatalf("extras failure deleted workload: %v", fw.deleted)
	}
}

func TestSetupExtrasRetry(t *testing.T) {
	s, mem, token := testServer(t)
	cluster, _ := mem.GetCluster(context.Background())
	nodeID := uuid.NewString()
	_ = mem.UpsertNode(context.Background(), appdb.Node{ID: nodeID, ClusterID: cluster.ID, Name: "local"})
	id := uuid.NewString()
	_ = mem.CreateWorkload(context.Background(), appdb.Workload{
		ID: id, ClusterID: cluster.ID, NodeID: nodeID, OwnerNodeID: nodeID, DesiredNodeID: nodeID,
		Name: "retry-me", Kind: lxc.KindSystemContainer, Status: lxc.StatusRunning,
		ImagePin: "alpine/3.21/amd64/default", DesiredPower: "running",
	})
	fw := &fakeWorkloads{}
	s.Workloads = fw
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)
	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/workloads/"+id+"/setup-extras", strings.NewReader(`{"extras":["git"]}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != http.StatusAccepted {
		b, _ := io.ReadAll(res.Body)
		t.Fatalf("retry %d %s", res.StatusCode, b)
	}
	_ = res.Body.Close()
	waitExtras(t, s, id)
	if fw.setups != 1 || fw.lastLife.Action != lxc.ActionGuestSetup {
		t.Fatalf("retry lifecycle %+v setups %d", fw.lastLife, fw.setups)
	}
	row, _ := mem.GetWorkload(context.Background(), cluster.ID, id)
	if row == nil {
		t.Fatal("retry must not delete the workload")
	}
}

// waitExtras waits for a background extensions job on a workload to finish.
func waitExtras(t *testing.T, s *Server, id string) {
	t.Helper()
	for i := 0; i < 500; i++ {
		s.extrasMu.Lock()
		busy := s.extrasBusy[id]
		s.extrasMu.Unlock()
		if !busy {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("extensions job did not finish")
}

// slowWorkloads blocks guest setup until released and records whether the
// job's context was cancelled, as a closed browser request used to do.
type slowWorkloads struct {
	fakeWorkloads
	release   chan struct{}
	cancelled bool
	status    []lxc.ExtraStatus
}

func (f *slowWorkloads) LifecycleCT(ctx context.Context, req lxc.LifecycleRequest) (lxc.Result, error) {
	switch req.Action {
	case lxc.ActionGuestSetup, lxc.ActionGuestExtrasUpdate, lxc.ActionGuestExtrasReinstall:
		<-f.release
		f.cancelled = ctx.Err() != nil
		f.lastLife = req
		return lxc.Result{WorkloadID: req.WorkloadID, Extras: f.status}, nil
	case lxc.ActionGuestExtrasStatus:
		return lxc.Result{WorkloadID: req.WorkloadID, Extras: f.status}, nil
	}
	return f.fakeWorkloads.LifecycleCT(ctx, req)
}

func TestExtensionsInstallOutlivesTheRequestThatStartedIt(t *testing.T) {
	s, mem, token := testServer(t)
	cluster, _ := mem.GetCluster(context.Background())
	nodeID := uuid.NewString()
	id := uuid.NewString()
	_ = mem.CreateWorkload(context.Background(), appdb.Workload{
		ID: id, ClusterID: cluster.ID, NodeID: nodeID, Name: "vault", Kind: lxc.KindSystemContainer,
		Status: lxc.StatusRunning, ImagePin: "debian/13/amd64/default", DesiredPower: "running",
	})
	fw := &slowWorkloads{release: make(chan struct{}), status: []lxc.ExtraStatus{
		{ID: "docker", Installed: true, Version: "26.1.5", Package: "docker.io"},
		{ID: "compose", Installed: true, Version: "2.32.4"},
		{ID: "git", Installed: false},
	}}
	s.Workloads = fw
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)
	do := func(method, path, body string) (int, []byte) {
		req, _ := http.NewRequest(method, ts.URL+path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
		res, err := ts.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		b, _ := io.ReadAll(res.Body)
		return res.StatusCode, b
	}
	code, b := do("POST", "/api/v1/workloads/"+id+"/extensions", `{"action":"install","extras":["compose"]}`)
	if code != http.StatusAccepted {
		t.Fatalf("install %d %s", code, b)
	}
	// The request has returned (as a proxy timing out would end it); the
	// install is still running and must not be cancelled.
	code, b = do("GET", "/api/v1/workloads/"+id+"/extensions", "")
	if code != 200 || !strings.Contains(string(b), `"busy":true`) || !strings.Contains(string(b), `"state":"installing"`) {
		t.Fatalf("running job must show: %d %s", code, b)
	}
	if code, _ = do("POST", "/api/v1/workloads/"+id+"/extensions", `{"action":"update","all":true}`); code != http.StatusConflict {
		t.Fatalf("a second job on the same container must wait: %d", code)
	}
	close(fw.release)
	waitExtras(t, s, id)
	if fw.cancelled {
		t.Fatal("the install was cancelled when its request ended")
	}
	if strings.Join(fw.lastLife.Extras, ",") != "docker,compose" {
		t.Fatalf("compose must pull in docker: %v", fw.lastLife.Extras)
	}
	exts, _ := mem.ListWorkloadExtensions(context.Background(), cluster.ID, id)
	states := map[string]string{}
	for _, x := range exts {
		states[x.ExtraID] = x.State + " " + x.Version
	}
	if states["docker"] != "installed 26.1.5" || states["compose"] != "installed 2.32.4" || states["git"] != "" {
		t.Fatalf("recorded %v", states)
	}

	fw.release = make(chan struct{})
	close(fw.release)
	if code, b = do("POST", "/api/v1/workloads/"+id+"/extensions", `{"action":"update","all":true}`); code != http.StatusAccepted {
		t.Fatalf("update all %d %s", code, b)
	}
	waitExtras(t, s, id)
	if fw.lastLife.Action != lxc.ActionGuestExtrasUpdate || len(fw.lastLife.Extras) != 2 {
		t.Fatalf("update all must cover the installed extensions: %+v", fw.lastLife)
	}
	if code, _ = do("POST", "/api/v1/workloads/"+id+"/extensions", `{"action":"remove","extras":["git"]}`); code != http.StatusBadRequest {
		t.Fatal("unknown actions must be refused")
	}
	if code, _ = do("POST", "/api/v1/workloads/"+id+"/extensions", `{"action":"install","extras":["nope"]}`); code != http.StatusBadRequest {
		t.Fatal("unknown extensions must be refused")
	}
	code, b = do("GET", "/api/v1/workloads/"+id+"/extensions?refresh=1", "")
	if code != 200 || !strings.Contains(string(b), `"id":"gh"`) || strings.Contains(string(b), `"busy":true`) {
		t.Fatalf("list %d %s", code, b)
	}
}
