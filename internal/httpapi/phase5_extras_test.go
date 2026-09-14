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
	body := `{"name":"extras-fail","kind":"system-container","image_pin":"alpine/3.21/amd64/default","pool_id":"` + poolID + `","network_id":"` + netID + `","extras":["docker"]}`
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
	if got["setup_status"] != "warnings" {
		t.Fatalf("expected setup warnings: %s", b)
	}
	id, _ := got["id"].(string)
	if id == "" {
		t.Fatal("missing workload id")
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
	if res.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(res.Body)
		t.Fatalf("retry %d %s", res.StatusCode, b)
	}
	_ = res.Body.Close()
	if fw.setups != 1 || fw.lastLife.Action != lxc.ActionGuestSetup {
		t.Fatalf("retry lifecycle %+v setups %d", fw.lastLife, fw.setups)
	}
	row, _ := mem.GetWorkload(context.Background(), cluster.ID, id)
	if row == nil {
		t.Fatal("retry must not delete the workload")
	}
}
