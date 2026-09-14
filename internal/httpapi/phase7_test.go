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
	disks, err := mem.ListWorkloadDisks(context.Background(), cluster.ID, row.ID)
	if err != nil || len(disks) != 1 || disks[0].VolumeID == "" {
		t.Fatalf("disk join %+v %v", disks, err)
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

func TestLabQemuProtoJoinsExistingZVolUnderHostPath(t *testing.T) {
	s, mem, token := testServer(t)
	cluster, _ := mem.GetCluster(context.Background())
	nodeID := uuid.NewString()
	_ = mem.UpsertNode(context.Background(), appdb.Node{ID: nodeID, ClusterID: cluster.ID, Name: "local"})
	poolID := uuid.NewString()
	if err := mem.CreateStoragePool(context.Background(), appdb.StoragePool{
		ID: poolID, ClusterID: cluster.ID, NodeID: nodeID, Name: "zfs-lab",
		BackendType: storage.BackendZFS, Status: storage.StatusAvailable,
		RootPath: storage.ZFSMountRoot,
	}); err != nil {
		t.Fatal(err)
	}
	fq := &fakeQEMU{}
	s.QEMU = fq
	s.Storage = fakeStorage{vol: storage.CreateVolumeResult{Handle: storage.VolumeHandle{
		BackendType: storage.BackendDirectory, BackendRef: "volumes/vm-disk/proto.qcow2",
		Kind: storage.KindBlock, Class: storage.ClassVMDisk, Format: storage.FormatQCOW2,
	}}}
	volID := uuid.NewString()
	zvol := "/dev/zvol/tank/" + volID
	if err := mem.CreateVolume(context.Background(), appdb.Volume{
		ID: volID, ClusterID: cluster.ID, NodeID: nodeID, PoolID: poolID,
		Class: storage.ClassVMDisk, Kind: storage.KindBlock, Format: storage.FormatZvol,
		SizeBytes: 1 << 30, Status: storage.StatusAvailable, BackendType: storage.BackendZFS,
		BackendRef: zvol,
	}); err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)
	body := `{"volume_id":"` + volID + `"}`
	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/lab/qemu-proto", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ := ts.Client().Do(req)
	raw, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("zvol qemu-proto %d %s", res.StatusCode, raw)
	}
	if fq.spec.DiskPath != zvol {
		t.Fatalf("StartQemuProto must use the zvol device, not JoinUnder the pool root: %s", fq.spec.DiskPath)
	}
	if strings.Contains(fq.spec.DiskPath, storage.ZFSMountRoot+"/dev/") {
		t.Fatalf("StartQemuProto must not join the zvol under the ZFS mount root: %s", fq.spec.DiskPath)
	}
	if fq.spec.DiskFormat != "raw" {
		t.Fatalf("StartQemuProto must start a zvol as raw, not catalog format %q", fq.spec.DiskFormat)
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	if out["disk_format"] != "raw" {
		t.Fatalf("GET disk_format must be raw for a zvol start: %s", raw)
	}
}

func TestLabQemuProtoJoinsExistingRBDUnderHostPath(t *testing.T) {
	s, mem, token := testServer(t)
	cluster, _ := mem.GetCluster(context.Background())
	nodeID := uuid.NewString()
	_ = mem.UpsertNode(context.Background(), appdb.Node{ID: nodeID, ClusterID: cluster.ID, Name: "local"})
	poolID := seedDistributedPool(t, mem, cluster.ID, nodeID)
	fq := &fakeQEMU{}
	s.QEMU = fq
	s.Distributed = &fakeDistributed{up: true}
	s.Storage = fakeStorage{vol: storage.CreateVolumeResult{Handle: storage.VolumeHandle{
		BackendType: storage.BackendDirectory, BackendRef: "volumes/vm-disk/proto.qcow2",
		Kind: storage.KindBlock, Class: storage.ClassVMDisk, Format: storage.FormatQCOW2,
	}}}
	volID := uuid.NewString()
	rbd, err := storage.RBDDevicePath("rbd", volID)
	if err != nil {
		t.Fatal(err)
	}
	if err := mem.CreateVolume(context.Background(), appdb.Volume{
		ID: volID, ClusterID: cluster.ID, NodeID: nodeID, PoolID: poolID,
		Class: storage.ClassVMDisk, Kind: storage.KindBlock, Format: storage.FormatRBD,
		SizeBytes: 1 << 30, Status: storage.StatusAvailable, BackendType: storage.BackendDistributed,
		BackendRef: rbd,
	}); err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)
	body := `{"volume_id":"` + volID + `"}`
	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/lab/qemu-proto", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ := ts.Client().Do(req)
	raw, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("rbd qemu-proto %d %s", res.StatusCode, raw)
	}
	if fq.spec.DiskPath != rbd {
		t.Fatalf("StartQemuProto must use the RBD device, not JoinUnder the pool root: %s", fq.spec.DiskPath)
	}
	if fq.spec.DiskFormat != "raw" {
		t.Fatalf("StartQemuProto must start a mapped RBD as raw, not catalog format %q", fq.spec.DiskFormat)
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	if out["disk_format"] != "raw" {
		t.Fatalf("GET disk_format must be raw for an RBD start: %s", raw)
	}
}

func TestLabQemuProtoJoinsExistingISCSIUnderHostPath(t *testing.T) {
	s, mem, token := testServer(t)
	cluster, _ := mem.GetCluster(context.Background())
	nodeID := uuid.NewString()
	_ = mem.UpsertNode(context.Background(), appdb.Node{ID: nodeID, ClusterID: cluster.ID, Name: "local"})
	dev, err := storage.ISCSIDevicePath("10.0.0.8:3260", "iqn.2020-01.com.example:target1")
	if err != nil {
		t.Fatal(err)
	}
	poolID := uuid.NewString()
	if err := mem.CreateStoragePool(context.Background(), appdb.StoragePool{
		ID: poolID, ClusterID: cluster.ID, NodeID: nodeID, Name: "lun-lab",
		BackendType: storage.BackendISCSI, Status: storage.StatusAvailable, RootPath: dev,
	}); err != nil {
		t.Fatal(err)
	}
	fq := &fakeQEMU{}
	s.QEMU = fq
	s.Storage = fakeStorage{vol: storage.CreateVolumeResult{Handle: storage.VolumeHandle{
		BackendType: storage.BackendDirectory, BackendRef: "volumes/vm-disk/proto.qcow2",
		Kind: storage.KindBlock, Class: storage.ClassVMDisk, Format: storage.FormatQCOW2,
	}}}
	volID := uuid.NewString()
	if err := mem.CreateVolume(context.Background(), appdb.Volume{
		ID: volID, ClusterID: cluster.ID, NodeID: nodeID, PoolID: poolID,
		Class: storage.ClassVMDisk, Kind: storage.KindBlock, Format: storage.FormatRaw,
		SizeBytes: 1 << 30, Status: storage.StatusAvailable, BackendType: storage.BackendISCSI,
		BackendRef: dev,
	}); err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)
	body := `{"volume_id":"` + volID + `"}`
	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/lab/qemu-proto", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ := ts.Client().Do(req)
	raw, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("iscsi qemu-proto %d %s", res.StatusCode, raw)
	}
	if fq.spec.DiskPath != dev {
		t.Fatalf("StartQemuProto must use the iSCSI by-path device, not JoinUnder the pool root: %s", fq.spec.DiskPath)
	}
	if fq.spec.DiskFormat != "raw" {
		t.Fatalf("StartQemuProto must start an iSCSI LUN as raw, not catalog format %q", fq.spec.DiskFormat)
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	if out["disk_format"] != "raw" {
		t.Fatalf("GET disk_format must be raw for an iSCSI start: %s", raw)
	}
}

func TestLabQemuProtoFailsClosedForContainerRoot(t *testing.T) {
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
	volID := uuid.NewString()
	if err := mem.CreateVolume(context.Background(), appdb.Volume{
		ID: volID, ClusterID: cluster.ID, NodeID: nodeID, PoolID: poolID,
		Class: storage.ClassContainerRoot, Kind: storage.KindFilesystem, Format: storage.FormatDirectory,
		Status: storage.StatusAvailable, BackendType: storage.BackendDirectory,
		BackendRef: "volumes/container-root/" + volID,
	}); err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)
	body := `{"volume_id":"` + volID + `"}`
	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/lab/qemu-proto", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ := ts.Client().Do(req)
	raw, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusConflict {
		t.Fatalf("container-root qemu-proto %d %s", res.StatusCode, raw)
	}
	if !strings.Contains(string(raw), "volume is not a vm-disk") {
		t.Fatalf("container-root qemu-proto body %s", raw)
	}
	if fq.spec.WorkloadID != "" {
		t.Fatal("StartQemuProto must not run for a container-root disk")
	}
	items, _ := mem.ListWorkloads(context.Background(), cluster.ID)
	if len(items) != 0 {
		t.Fatalf("GET must not list a qemu-proto VM whose disk start cannot attach a container-root: %+v", items)
	}
}

func TestLabQemuProtoFailsClosedForNewDistributedVolume(t *testing.T) {
	s, mem, token := testServer(t)
	cluster, _ := mem.GetCluster(context.Background())
	nodeID := uuid.NewString()
	_ = mem.UpsertNode(context.Background(), appdb.Node{ID: nodeID, ClusterID: cluster.ID, Name: "local"})
	poolID := seedDistributedPool(t, mem, cluster.ID, nodeID)
	s.Distributed = &fakeDistributed{up: true}
	s.QEMU = &fakeQEMU{}
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
	res, _ := ts.Client().Do(req)
	raw, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("new distributed qemu-proto %d %s", res.StatusCode, raw)
	}
	if !strings.Contains(string(raw), "distributed RBD pools do not store directory qcow2 copies") {
		t.Fatalf("new distributed qemu-proto body %s", raw)
	}
	items, _ := mem.ListWorkloads(context.Background(), cluster.ID)
	if len(items) != 0 {
		t.Fatalf("GET must not list a qemu-proto VM whose disk is a directory copy on an RBD pool: %+v", items)
	}
}

func TestLabQemuProtoFailsClosedWhenDiskPersistFails(t *testing.T) {
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
	s.Store = failCreateWorkloadDiskStore{Store: mem}

	body := `{"pool_id":"` + poolID + `"}`
	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/lab/qemu-proto", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ := ts.Client().Do(req)
	raw, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusInternalServerError {
		t.Fatalf("disk persist %d %s", res.StatusCode, raw)
	}
	if !strings.Contains(string(raw), "could not record VM disk") {
		t.Fatalf("disk persist body %s", raw)
	}
	if fq.stops != 1 {
		t.Fatalf("host leftover qemu-proto after disk persist fail: stops %d", fq.stops)
	}
	items, _ := mem.ListWorkloads(context.Background(), cluster.ID)
	if len(items) != 0 {
		t.Fatalf("GET must not list a qemu-proto VM whose disk join cannot be recorded: %+v", items)
	}
}

func TestLabQemuProtoFailsClosedWhenStopSpecPersistFails(t *testing.T) {
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
	body := `{"pool_id":"` + poolID + `"}`
	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/lab/qemu-proto", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ := ts.Client().Do(req)
	raw, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("start %d %s", res.StatusCode, raw)
	}
	s.Store = failUpdateWorkloadSpecStore{Store: mem}

	stop, _ := http.NewRequest("POST", ts.URL+"/api/v1/lab/qemu-proto/stop", strings.NewReader("{}"))
	stop.Header.Set("Content-Type", "application/json")
	stop.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	stopRes, _ := ts.Client().Do(stop)
	stopRaw, _ := io.ReadAll(stopRes.Body)
	_ = stopRes.Body.Close()
	if stopRes.StatusCode != http.StatusInternalServerError {
		t.Fatalf("stop spec persist %d %s", stopRes.StatusCode, stopRaw)
	}
	if !strings.Contains(string(stopRaw), "could not record VM spec") {
		t.Fatalf("stop spec persist body %s", stopRaw)
	}
	row, err := mem.GetWorkloadByName(context.Background(), cluster.ID, qemuProtoName)
	if err != nil || row == nil {
		t.Fatal("GET must still list the qemu-proto VM after a stop persist miss")
	}
	if row.DesiredPower != "running" {
		t.Fatalf("200 must not claim desired_power stopped when spec persist failed: %+v", row)
	}
}

func TestLabQemuProtoStopStatusMatchesGETWhenObservedPersistFails(t *testing.T) {
	s, mem, token := testServer(t)
	cluster, _ := mem.GetCluster(context.Background())
	nodeID := uuid.NewString()
	_ = mem.UpsertNode(context.Background(), appdb.Node{ID: nodeID, ClusterID: cluster.ID, Name: "local"})
	poolID := seedQemuLab(t, mem, cluster.ID, nodeID)
	s.QEMU = &fakeQEMU{start: qemu.Result{UnitActive: true}}
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
	res, _ := ts.Client().Do(req)
	raw, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("start %d %s", res.StatusCode, raw)
	}
	s.Store = failUpdateWorkloadObservedStore{Store: mem}

	stop, _ := http.NewRequest("POST", ts.URL+"/api/v1/lab/qemu-proto/stop", strings.NewReader("{}"))
	stop.Header.Set("Content-Type", "application/json")
	stop.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	stopRes, _ := ts.Client().Do(stop)
	stopRaw, _ := io.ReadAll(stopRes.Body)
	_ = stopRes.Body.Close()
	if stopRes.StatusCode != http.StatusOK {
		t.Fatalf("stop %d %s", stopRes.StatusCode, stopRaw)
	}
	var out map[string]any
	if err := json.Unmarshal(stopRaw, &out); err != nil {
		t.Fatal(err)
	}
	got, err := mem.GetWorkloadByName(context.Background(), cluster.ID, qemuProtoName)
	if err != nil || got == nil {
		t.Fatalf("get workload %v", err)
	}
	if out["status"] != got.Status {
		t.Fatalf("200 status %v must match GET %q", out["status"], got.Status)
	}
	if out["desired_power"] != got.DesiredPower {
		t.Fatalf("200 desired_power %v must match GET %q", out["desired_power"], got.DesiredPower)
	}
	if out["unit_active"] != got.UnitActive {
		t.Fatalf("200 unit_active %v must match GET %v", out["unit_active"], got.UnitActive)
	}
	if !got.UnitActive {
		t.Fatal("GET unit_active must stay the stored start row when observed persist misses")
	}
	if out["observe_status"] != qemu.StatusStopped {
		t.Fatalf("observe_status must stay live %v", out["observe_status"])
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
