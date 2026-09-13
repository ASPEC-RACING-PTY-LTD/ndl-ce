package httpapi

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/no-dal/ndl-ce/internal/appdb"
	"github.com/no-dal/ndl-ce/internal/migrate"
	"github.com/no-dal/ndl-ce/internal/ndnet"
)

func TestDestAgentClientNeverFallsBackToUnix(t *testing.T) {
	s, mem, _ := testServer(t)
	cluster, _ := mem.GetCluster(t.Context())
	control := seedNode(t, mem, cluster.ID, debianInv(), false)
	worker := appdb.Node{ID: uuid.NewString(), ClusterID: cluster.ID, Name: "box-b", Role: "worker", Hostname: "box-b"}
	if err := mem.UpsertNode(t.Context(), worker); err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1_700_000_000, 0).UTC()
	s.Now = func() time.Time { return now }
	remote := appdb.RemoteNode{
		ID: worker.ID, ClusterID: cluster.ID, Name: worker.Name,
		ListenAddr: "10.64.8.2:9444", Status: ndnet.NodeReady,
		LastHandshakeUnix: now.Unix(), LastSeenAt: &now,
	}
	if err := mem.CreateRemoteNode(t.Context(), remote); err != nil {
		t.Fatal(err)
	}

	if _, ok := s.destAgentClient(context.Background(), &control); ok {
		t.Fatal("local dest must not return a TCP dest agent")
	}
	c, ok := s.destAgentClient(context.Background(), &worker)
	if !ok || c.TCPAddr != "10.64.8.2:9444" {
		t.Fatalf("ready worker dest client %+v ok=%v", c, ok)
	}
	if c.TCPAddr == "" {
		t.Fatal("dest agent must not fall back to the control unix agent")
	}

	wl, wlOK := s.destWorkloads(context.Background(), &worker)
	if !wlOK || wl == nil {
		t.Fatal("ready worker must expose dest workloads")
	}
	bk, bkOK := s.destBackup(context.Background(), &worker)
	if !bkOK || bk == nil {
		t.Fatal("ready worker must expose dest backup")
	}
	vm, vmOK := s.destVM(context.Background(), &worker)
	if !vmOK || vm == nil {
		t.Fatal("ready worker must expose dest VM")
	}

	s.Migrate = migrate.NewFake()
	rt, rtOK := s.destRuntime(context.Background(), &worker)
	if !rtOK {
		t.Fatal("ready worker dest runtime")
	}
	if _, ok := rt.(*splitMigrate); !ok {
		t.Fatalf("remote dest must split source/dest execute, got %T", rt)
	}
	if lo, ok := rt.(interface{ LocalAgentOnly() bool }); ok && lo.LocalAgentOnly() {
		t.Fatal("split dest runtime must not be local-agent-only")
	}

	_, localWL := s.destWorkloads(context.Background(), &control)
	if localWL && s.Workloads == nil {
		t.Fatal("local dest workloads without a local engine must stay closed")
	}
}

func TestDestRuntimeStaysClosedWithoutListen(t *testing.T) {
	s, mem, _ := testServer(t)
	cluster, _ := mem.GetCluster(t.Context())
	_ = seedNode(t, mem, cluster.ID, debianInv(), false)
	worker := appdb.Node{ID: uuid.NewString(), ClusterID: cluster.ID, Name: "box-b", Role: "worker"}
	if err := mem.UpsertNode(t.Context(), worker); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.destAgentClient(context.Background(), &worker); ok {
		t.Fatal("worker without a Ready listen addr is not a dest agent")
	}
	s.Migrate = nil
	if s.destAgentReady(context.Background(), &worker) {
		t.Fatal("nil migrate must stay closed")
	}
	s.Migrate = migrateUnavailable{}
	if s.destAgentReady(context.Background(), &worker) {
		t.Fatal("unavailable migrate must stay closed")
	}
	s.Migrate = AdaptMigrate(nil)
	if s.destAgentReady(context.Background(), &worker) {
		t.Fatal("unix-only agent migrate must not start dest incoming")
	}
}

func TestSplitMigratePullsHTTPVolumesOnDest(t *testing.T) {
	var pulled []migrate.VolumeCopy
	m := &splitMigrate{
		src: migrate.NewFake(),
		dst: pullDest{Runtime: migrate.NewFake(), pulled: &pulled},
	}
	vol := migrate.VolumeCopy{VolumeID: "vol", SourcePath: "https://objects.example/pack", DestPath: "/var/lib/ndl/storage/local/vol"}
	if err := m.CopyVolume(context.Background(), vol); err != nil {
		t.Fatal(err)
	}
	if len(pulled) != 1 || pulled[0].SourcePath != vol.SourcePath {
		t.Fatalf("dest must pull object volumes: %+v", pulled)
	}
	local := migrate.VolumeCopy{VolumeID: "vol2", SourcePath: "/var/lib/ndl/a.qcow2", DestPath: "/var/lib/ndl/b.qcow2"}
	if err := m.CopyVolume(context.Background(), local); err != nil {
		t.Fatal(err)
	}
}

type pullDest struct {
	migrate.Runtime
	pulled *[]migrate.VolumeCopy
}

func (p pullDest) PullVolume(_ context.Context, vol migrate.VolumeCopy) error {
	*p.pulled = append(*p.pulled, vol)
	return nil
}

func TestDestAgentReadyReason(t *testing.T) {
	if !strings.Contains(remoteApplyReason, "dest agent") {
		t.Fatalf("remote apply reason must name dest agent: %s", remoteApplyReason)
	}
}

func TestSetDestListenRegistersReadyWorker(t *testing.T) {
	s, mem, token := testServer(t)
	cluster, _ := mem.GetCluster(t.Context())
	control := seedNode(t, mem, cluster.ID, debianInv(), false)
	worker := appdb.Node{ID: uuid.NewString(), ClusterID: cluster.ID, Name: "box-b", Role: "worker", Hostname: "box-b"}
	if err := mem.UpsertNode(t.Context(), worker); err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)

	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/nodes/"+control.ID+"/dest-listen", strings.NewReader(`{"listen_addr":"10.64.8.2:9444"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ := ts.Client().Do(req)
	raw, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("control dest-listen %d %s", res.StatusCode, raw)
	}

	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/nodes/"+worker.ID+"/dest-listen", strings.NewReader(`{"listen_addr":"10.64.8.2:9444"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(req)
	raw, _ = io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK || !strings.Contains(string(raw), `"status":"Ready"`) || !strings.Contains(string(raw), "10.64.8.2:9444") {
		t.Fatalf("worker dest-listen %d %s", res.StatusCode, raw)
	}
	c, ok := s.destAgentClient(context.Background(), &worker)
	if !ok || c.TCPAddr != "10.64.8.2:9444" {
		t.Fatalf("advertised dest listen %+v ok=%v", c, ok)
	}
}
