package httpapi

import (
	"context"
	"encoding/json"
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

// mountWorkloads keeps a container's mount list like the agent does.
type mountWorkloads struct {
	*fakeWorkloads
	mounts  []lxc.Mount
	sets    [][]lxc.Mount
	actions []string
}

func (m *mountWorkloads) LifecycleCT(ctx context.Context, req lxc.LifecycleRequest) (lxc.Result, error) {
	m.actions = append(m.actions, req.Action)
	switch req.Action {
	case lxc.ActionMountsGet:
		return lxc.Result{WorkloadID: req.WorkloadID, Mounts: m.mounts, MappedRootUID: 100000}, nil
	case lxc.ActionMountsSet:
		clean, err := lxc.NormalizeMounts(req.Mounts)
		if err != nil {
			return lxc.Result{}, err
		}
		m.sets = append(m.sets, req.Mounts)
		for i := range clean {
			clean[i].Create = false
		}
		m.mounts = clean
		return lxc.Result{WorkloadID: req.WorkloadID, Mounts: clean, MappedRootUID: 100000, RestartRequired: true}, nil
	}
	return m.fakeWorkloads.LifecycleCT(ctx, req)
}

func storageCall(t *testing.T, ts *httptest.Server, cookie, method, path, body string) (int, map[string]any) {
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
	var out map[string]any
	_ = json.Unmarshal(raw, &out)
	if out == nil {
		out = map[string]any{"raw": string(raw)}
	}
	return res.StatusCode, out
}

func TestWorkloadStorageMountsPoolFolderAndHostFolder(t *testing.T) {
	s, mem, token := testServer(t)
	ctx := context.Background()
	cluster, _ := mem.GetCluster(ctx)
	nodeID := uuid.NewString()
	_ = mem.UpsertNode(ctx, appdb.Node{ID: nodeID, ClusterID: cluster.ID, Name: "local"})
	poolID, _ := seedCompute(t, mem, cluster.ID, nodeID)
	var creates int
	s.Storage = fakeStorage{volCreates: &creates, vol: storage.CreateVolumeResult{Handle: storage.VolumeHandle{
		BackendType: storage.BackendDirectory, BackendRef: "/mnt/hdd/volumes/container-root/media",
		Kind: storage.KindFilesystem, Class: storage.ClassContainerRoot, Format: storage.FormatDirectory,
	}}}
	wlf := &mountWorkloads{fakeWorkloads: &fakeWorkloads{}}
	s.Workloads = wlf
	wlID := uuid.NewString()
	if err := mem.CreateWorkload(ctx, appdb.Workload{ID: wlID, ClusterID: cluster.ID, NodeID: nodeID, Name: "ViewDock", Kind: "system-container", Status: "running", CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)

	code, body := storageCall(t, ts, cookie, "GET", "/workloads/"+wlID+"/storage", "")
	if code != http.StatusOK || len(body["mounts"].([]any)) != 0 || len(body["pools"].([]any)) == 0 {
		t.Fatalf("%d %v", code, body)
	}

	put := `{"mounts":[
		{"pool_id":"` + poolID + `","size_bytes":10737418240,"target":"/mnt/media","label":"Media"},
		{"source":"/srv/existing-library","target":"/mnt/library","read_only":true}
	]}`
	code, body = storageCall(t, ts, cookie, "PUT", "/workloads/"+wlID+"/storage/mounts", put)
	if code != http.StatusOK || body["restart_required"] != true {
		t.Fatalf("%d %v", code, body)
	}
	if creates != 1 {
		t.Fatalf("one pool volume must be created, got %d", creates)
	}
	sent := wlf.sets[0]
	if sent[0].Source != "/mnt/hdd/volumes/container-root/media" || !sent[0].Create || sent[0].PoolID != poolID {
		t.Fatalf("pool mount: %+v", sent[0])
	}
	if sent[1].Source != "/srv/existing-library" || sent[1].Create || !sent[1].ReadOnly {
		t.Fatalf("existing folders must never be created or re-owned: %+v", sent[1])
	}
	for _, a := range wlf.actions {
		if a == "stop" || a == "restart" || a == "start" || a == "delete" {
			t.Fatalf("storage changes must not touch the running container: %v", wlf.actions)
		}
	}

	// The new volume is operator-owned, so orphan cleanup never removes it.
	vols, _ := mem.ListVolumes(ctx, cluster.ID, "")
	var vol *appdb.Volume
	for i := range vols {
		if vols[i].BackendRef == sent[0].Source {
			vol = &vols[i]
		}
	}
	if vol == nil || vol.OwnerKind != storage.VolumeKindOperator {
		t.Fatalf("data volume must be operator-owned: %+v", vol)
	}
	if orphans := appdb.ClassifyOrphanVolumes(appdb.VolumeFacts{Volumes: []appdb.Volume{*vol}, Now: time.Now()}); len(orphans) != 0 {
		t.Fatalf("data volume must never be swept: %+v", orphans)
	}

	// Removing a mount sends the shorter list only; no storage is destroyed.
	keep := `{"mounts":[{"source":"/mnt/hdd/volumes/container-root/media","target":"/mnt/media","pool_id":"` + poolID + `","label":"Media"}]}`
	code, body = storageCall(t, ts, cookie, "PUT", "/workloads/"+wlID+"/storage/mounts", keep)
	if code != http.StatusOK || len(body["mounts"].([]any)) != 1 || creates != 1 {
		t.Fatalf("%d %v creates=%d", code, body, creates)
	}
	if after, _ := mem.ListVolumes(ctx, cluster.ID, ""); len(after) != len(vols) {
		t.Fatal("removing a mount must not remove its volume")
	}

	for _, bad := range []string{
		`{"mounts":[{"source":"/etc","target":"/mnt/x"}]}`,
		`{"mounts":[{"target":"/mnt/x"}]}`,
		`{"mounts":[{"source":"/srv/a","target":"/proc"}]}`,
		`{"mounts":[{"pool_id":"` + poolID + `","size_bytes":1073741824,"target":"/mnt/a"},{"source":"/srv/b","target":"/mnt/a"}]}`,
	} {
		if code, _ := storageCall(t, ts, cookie, "PUT", "/workloads/"+wlID+"/storage/mounts", bad); code < 400 {
			t.Fatalf("must be refused: %s", bad)
		}
	}
	if creates != 1 {
		t.Fatalf("a refused request must not create storage, got %d creates", creates)
	}
}

func TestWorkloadStorageIsForSystemContainers(t *testing.T) {
	s, mem, token := testServer(t)
	ctx := context.Background()
	cluster, _ := mem.GetCluster(ctx)
	s.Workloads = &mountWorkloads{fakeWorkloads: &fakeWorkloads{}}
	vmID := uuid.NewString()
	_ = mem.CreateWorkload(ctx, appdb.Workload{ID: vmID, ClusterID: cluster.ID, Name: "vm", Kind: "vm", CreatedAt: time.Now()})
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)
	if code, _ := storageCall(t, ts, cookie, "GET", "/workloads/"+vmID+"/storage", ""); code != http.StatusUnprocessableEntity {
		t.Fatalf("vm storage must be refused, got %d", code)
	}
}
