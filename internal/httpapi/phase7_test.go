package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/no-dal/ndl-ce/internal/appdb"
	"github.com/no-dal/ndl-ce/internal/qemu"
	"github.com/no-dal/ndl-ce/internal/rbac"
	"github.com/no-dal/ndl-ce/internal/secutil"
	"github.com/no-dal/ndl-ce/internal/storage"
)

type fakeQEMU struct {
	start qemu.Result
	stop  qemu.Result
	err   error
	spec  qemu.Spec
	stops int
}

func (f *fakeQEMU) StartQemuProto(_ context.Context, spec qemu.Spec) (qemu.Result, error) {
	f.spec = spec
	res := f.start
	if res.WorkloadID == "" {
		res.WorkloadID = spec.WorkloadID
	}
	if res.Status == "" {
		res.Status = qemu.StatusRunning
	}
	if res.Accel == "" {
		res.Accel = "tcg"
	}
	if res.Machine == "" {
		res.Machine = spec.Machine
	}
	return res, f.err
}

func (f *fakeQEMU) StopQemuProto(_ context.Context, id string) (qemu.Result, error) {
	f.stops++
	res := f.stop
	if res.WorkloadID == "" {
		res.WorkloadID = id
	}
	if res.Status == "" {
		res.Status = qemu.StatusStopped
	}
	return res, f.err
}

func (f *fakeQEMU) KillQemuProto(ctx context.Context, id string) (qemu.Result, error) {
	return f.StopQemuProto(ctx, id)
}

func (f *fakeQEMU) StatusQemuProto(_ context.Context, id string) (qemu.Observed, error) {
	return qemu.Observed{WorkloadID: id, Status: qemu.StatusStopped, Reason: "fixture"}, nil
}

func seedQemuLab(t *testing.T, mem *appdb.Memory, clusterID, nodeID string) string {
	t.Helper()
	poolID := uuid.NewString()
	if err := mem.CreateStoragePool(context.Background(), appdb.StoragePool{
		ID: poolID, ClusterID: clusterID, NodeID: nodeID, Name: "local",
		BackendType: storage.BackendDirectory, Status: storage.StatusAvailable,
		RootPath: "/var/lib/ndl/storage/local",
	}); err != nil {
		t.Fatal(err)
	}
	return poolID
}

func operatorToken(t *testing.T, mem *appdb.Memory, clusterID string) string {
	t.Helper()
	op := appdb.User{ID: uuid.NewString(), ClusterID: clusterID, Username: "op"}
	if err := mem.CreateUser(context.Background(), op); err != nil {
		t.Fatal(err)
	}
	if err := mem.BindRole(context.Background(), clusterID, op.ID, rbac.Operator); err != nil {
		t.Fatal(err)
	}
	plain := "ndl_op_lab_token"
	if err := mem.CreateToken(context.Background(), appdb.APIToken{
		ID: uuid.NewString(), ClusterID: clusterID, UserID: op.ID, Name: "op",
		TokenHash: secutil.HashSHA256(plain), Prefix: "ndl_op",
	}); err != nil {
		t.Fatal(err)
	}
	return plain
}

func TestLabQemuProtoOperatorForbidden(t *testing.T) {
	s, mem, token := testServer(t)
	cluster, _ := mem.GetCluster(context.Background())
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	_ = claimAdmin(t, ts, token)
	plain := operatorToken(t, mem, cluster.ID)
	for _, path := range []string{
		"/api/v1/lab/qemu-proto",
		"/api/v1/lab/qemu-proto/stop",
		"/api/v1/lab/qemu-proto/kill",
	} {
		method := "POST"
		if path == "/api/v1/lab/qemu-proto" {
			// both GET and POST
			req, _ := http.NewRequest("GET", ts.URL+path, nil)
			req.Header.Set("Authorization", "Bearer "+plain)
			res, err := ts.Client().Do(req)
			if err != nil {
				t.Fatal(err)
			}
			if res.StatusCode != http.StatusForbidden {
				b, _ := io.ReadAll(res.Body)
				t.Fatalf("operator GET %s %d %s", path, res.StatusCode, b)
			}
			_ = res.Body.Close()
		}
		req, _ := http.NewRequest(method, ts.URL+path, strings.NewReader("{}"))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+plain)
		res, err := ts.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		if res.StatusCode != http.StatusForbidden {
			b, _ := io.ReadAll(res.Body)
			t.Fatalf("operator %s %s %d %s", method, path, res.StatusCode, b)
		}
		_ = res.Body.Close()
	}
}

func TestLabQemuProtoStoresVMKind(t *testing.T) {
	s, mem, token := testServer(t)
	cluster, _ := mem.GetCluster(context.Background())
	nodeID := uuid.NewString()
	_ = mem.UpsertNode(context.Background(), appdb.Node{ID: nodeID, ClusterID: cluster.ID, Name: "local"})
	poolID := seedQemuLab(t, mem, cluster.ID, nodeID)
	fq := &fakeQEMU{}
	s.QEMU = fq
	s.Storage = fakeStorage{vol: storage.CreateVolumeResult{Handle: storage.VolumeHandle{
		BackendType: storage.BackendDirectory, BackendRef: "volumes/vm-disk/proto.qcow2",
		Kind: storage.KindBlock, Class: storage.ClassVMDisk, Format: storage.FormatQCOW2,
	}}}
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)
	body := `{"pool_id":"` + poolID + `"}`
	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/lab/qemu-proto", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != http.StatusCreated {
		b, _ := io.ReadAll(res.Body)
		t.Fatalf("start %d %s", res.StatusCode, b)
	}
	var out map[string]any
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if out["kind"] != qemu.KindVM || out["name"] != qemuProtoName {
		t.Fatalf("response kind/name %v %v", out["kind"], out["name"])
	}
	if out["accel"] == "kvm" && fq.spec.Accel != "kvm" {
		t.Fatal("claimed kvm without using it")
	}
	if fq.spec.Accel == "kvm" {
		t.Fatal("control plane must not set kvm")
	}
	row, err := mem.GetWorkloadByName(context.Background(), cluster.ID, qemuProtoName)
	if err != nil || row == nil {
		t.Fatal("workload missing")
	}
	if row.Kind != qemu.KindVM {
		t.Fatalf("stored kind %q", row.Kind)
	}
	if !strings.HasPrefix(fq.spec.DiskPath, "/var/lib/ndl/storage/local/volumes/vm-disk/") {
		t.Fatalf("disk path %q", fq.spec.DiskPath)
	}
	stReq, _ := http.NewRequest("GET", ts.URL+"/api/v1/lab/qemu-proto", nil)
	stReq.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	stRes, err := ts.Client().Do(stReq)
	if err != nil {
		t.Fatal(err)
	}
	if stRes.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(stRes.Body)
		t.Fatalf("status %d %s", stRes.StatusCode, b)
	}
	var status map[string]any
	if err := json.NewDecoder(stRes.Body).Decode(&status); err != nil {
		t.Fatal(err)
	}
	_ = stRes.Body.Close()
	if status["observe_status"] != qemu.StatusStopped {
		t.Fatalf("honest unit %v", status)
	}
	if status["kind"] != qemu.KindVM {
		t.Fatalf("status kind %v", status["kind"])
	}
	stop, _ := http.NewRequest("POST", ts.URL+"/api/v1/lab/qemu-proto/stop", strings.NewReader("{}"))
	stop.Header.Set("Content-Type", "application/json")
	stop.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	stopRes, err := ts.Client().Do(stop)
	if err != nil {
		t.Fatal(err)
	}
	if stopRes.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(stopRes.Body)
		t.Fatalf("stop %d %s", stopRes.StatusCode, b)
	}
	_ = stopRes.Body.Close()
	if fq.stops != 1 {
		t.Fatalf("stops %d", fq.stops)
	}
}

func TestLabQemuProtoFailsClosedForTinyDisk(t *testing.T) {
	s, mem, token := testServer(t)
	cluster, _ := mem.GetCluster(context.Background())
	nodeID := uuid.NewString()
	_ = mem.UpsertNode(context.Background(), appdb.Node{ID: nodeID, ClusterID: cluster.ID, Name: "local"})
	poolID := seedQemuLab(t, mem, cluster.ID, nodeID)
	s.QEMU = &fakeQEMU{}
	s.Storage = fakeStorage{vol: storage.CreateVolumeResult{Handle: storage.VolumeHandle{
		BackendType: storage.BackendDirectory, BackendRef: "volumes/vm-disk/proto.qcow2",
		Kind: storage.KindBlock, Class: storage.ClassVMDisk, Format: storage.FormatQCOW2,
	}}}
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)
	body := `{"pool_id":"` + poolID + `","size_bytes":1}`
	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/lab/qemu-proto", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("tiny qemu-proto disk %d %s", res.StatusCode, raw)
	}
	if !strings.Contains(string(raw), storage.ErrInvalidSize.Error()) {
		t.Fatalf("tiny qemu-proto disk body %s", raw)
	}
	items, _ := mem.ListWorkloads(context.Background(), cluster.ID)
	if len(items) != 0 {
		t.Fatalf("GET must not list a qemu-proto VM whose disk apply cannot create: %+v", items)
	}
	vols, _ := mem.ListVolumes(context.Background(), cluster.ID, poolID)
	if len(vols) != 0 {
		t.Fatalf("GET must not list a volume apply cannot create: %+v", vols)
	}
}

func TestLabQemuProtoFailsClosedForUnavailableVolume(t *testing.T) {
	s, mem, token := testServer(t)
	cluster, _ := mem.GetCluster(context.Background())
	nodeID := uuid.NewString()
	_ = mem.UpsertNode(context.Background(), appdb.Node{ID: nodeID, ClusterID: cluster.ID, Name: "local"})
	poolID := seedQemuLab(t, mem, cluster.ID, nodeID)
	s.QEMU = &fakeQEMU{}
	s.Storage = fakeStorage{vol: storage.CreateVolumeResult{Handle: storage.VolumeHandle{
		BackendType: storage.BackendDirectory, BackendRef: "volumes/vm-disk/proto.qcow2",
		Kind: storage.KindBlock, Class: storage.ClassVMDisk, Format: storage.FormatQCOW2,
	}}}
	volID := uuid.NewString()
	if err := mem.CreateVolume(context.Background(), appdb.Volume{
		ID: volID, ClusterID: cluster.ID, NodeID: nodeID, PoolID: poolID,
		Class: storage.ClassVMDisk, Kind: storage.KindBlock, Format: storage.FormatQCOW2,
		SizeBytes: 1 << 30, Status: storage.StatusUnavailable, BackendType: storage.BackendDirectory,
		BackendRef: "volumes/vm-disk/proto.qcow2",
	}); err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)
	body := `{"pool_id":"` + poolID + `","volume_id":"` + volID + `"}`
	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/lab/qemu-proto", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ := ts.Client().Do(req)
	raw, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusConflict {
		t.Fatalf("unavailable qemu-proto volume %d %s", res.StatusCode, raw)
	}
	if !strings.Contains(string(raw), "storage is unavailable") {
		t.Fatalf("unavailable qemu-proto volume body %s", raw)
	}
	items, _ := mem.ListWorkloads(context.Background(), cluster.ID)
	if len(items) != 0 {
		t.Fatalf("GET must not list a qemu-proto VM whose disk start cannot attach: %+v", items)
	}
}

func TestLabQemuProtoHasNoHostExec(t *testing.T) {
	b, err := os.ReadFile("phase7.go")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "Host.Exec") {
		t.Fatal("phase7 must not use Host.Exec")
	}
}
