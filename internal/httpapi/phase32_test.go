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
	"github.com/no-dal/ndl-ce/internal/agentrpc"
	"github.com/no-dal/ndl-ce/internal/appdb"
	"github.com/no-dal/ndl-ce/internal/migrate"
	"github.com/no-dal/ndl-ce/internal/storage"
)

func TestPhase32LiveMigrateMovesOwnership(t *testing.T) {
	s, mem, token := testServer(t)
	s.Migrate = migrate.NewFake()
	clusterRow, _ := mem.GetCluster(t.Context())
	control := seedNode(t, mem, clusterRow.ID, debianInv(), false)
	worker := appdb.Node{ID: uuid.NewString(), ClusterID: clusterRow.ID, Name: "box-b", Role: "worker", Hostname: "box-b"}
	if err := mem.UpsertNode(t.Context(), worker); err != nil {
		t.Fatal(err)
	}
	wlID := uuid.NewString()
	s.Migrate.(*migrate.Fake).SetSourceRunning(wlID, true)
	if err := mem.CreateWorkload(t.Context(), appdb.Workload{
		ID: wlID, ClusterID: clusterRow.ID, NodeID: control.ID,
		OwnerNodeID: control.ID, DesiredNodeID: control.ID,
		Name: "move-me", Kind: "vm", Status: "running",
	}); err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)
	body := `{"dest_node_id":"` + worker.ID + `","mode":"live"}`
	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/workloads/"+wlID+"/migrate", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ := ts.Client().Do(req)
	raw, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("migrate %d %s", res.StatusCode, raw)
	}
	got, _ := mem.GetWorkload(t.Context(), clusterRow.ID, wlID)
	if got.NodeID != worker.ID || got.OwnerNodeID != worker.ID || got.OwnershipEpoch != 1 {
		t.Fatalf("ownership %+v", got)
	}
	if got.Status != "running" {
		t.Fatalf("dest status %s", got.Status)
	}
	var job map[string]any
	if err := json.Unmarshal(raw, &job); err != nil {
		t.Fatal(err)
	}
	stored, err := mem.GetMigrateJob(t.Context(), clusterRow.ID, job["id"].(string))
	if err != nil || stored == nil || stored.WorkloadID != wlID || stored.DestNodeID != worker.ID {
		t.Fatalf("migrate job %+v %v", stored, err)
	}
	listed, _ := mem.ListWorkloads(t.Context(), clusterRow.ID)
	if len(listed) != 1 {
		t.Fatalf("must not create a second workload copy: %d", len(listed))
	}
}

func TestPhase32MigrateFailsClosedForUnavailableVolume(t *testing.T) {
	s, mem, token := testServer(t)
	s.Migrate = migrate.NewFake()
	clusterRow, _ := mem.GetCluster(t.Context())
	control := seedNode(t, mem, clusterRow.ID, debianInv(), false)
	worker := appdb.Node{ID: uuid.NewString(), ClusterID: clusterRow.ID, Name: "box-b", Role: "worker", Hostname: "box-b"}
	if err := mem.UpsertNode(t.Context(), worker); err != nil {
		t.Fatal(err)
	}
	pool := appdb.StoragePool{
		ID: uuid.NewString(), ClusterID: clusterRow.ID, NodeID: control.ID, Name: "mig-offline",
		BackendType: storage.BackendDirectory, Status: storage.StatusAvailable,
		RootPath: storage.DefaultPoolPath,
	}
	if err := mem.CreateStoragePool(t.Context(), pool); err != nil {
		t.Fatal(err)
	}
	vol := appdb.Volume{
		ID: uuid.NewString(), ClusterID: clusterRow.ID, NodeID: control.ID, PoolID: pool.ID,
		Class: storage.ClassVMDisk, Status: storage.StatusUnavailable,
		BackendRef: "volumes/vm-disk/boot.qcow2",
	}
	if err := mem.CreateVolume(t.Context(), vol); err != nil {
		t.Fatal(err)
	}
	wlID := uuid.NewString()
	s.Migrate.(*migrate.Fake).SetSourceRunning(wlID, true)
	if err := mem.CreateWorkload(t.Context(), appdb.Workload{
		ID: wlID, ClusterID: clusterRow.ID, NodeID: control.ID,
		OwnerNodeID: control.ID, DesiredNodeID: control.ID,
		Name: "move-me", Kind: "vm", Status: "running",
	}); err != nil {
		t.Fatal(err)
	}
	if err := mem.CreateWorkloadDisk(t.Context(), appdb.WorkloadDisk{
		ID: uuid.NewString(), ClusterID: clusterRow.ID, WorkloadID: wlID, VolumeID: vol.ID,
	}); err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)
	body := `{"dest_node_id":"` + worker.ID + `","mode":"live"}`
	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/workloads/"+wlID+"/migrate", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ := ts.Client().Do(req)
	raw, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("unavailable volume migrate %d %s", res.StatusCode, raw)
	}
	if !strings.Contains(string(raw), "storage is unavailable") {
		t.Fatalf("unavailable volume migrate body %s", raw)
	}
	got, _ := mem.GetWorkload(t.Context(), clusterRow.ID, wlID)
	if got == nil || got.NodeID != control.ID || got.OwnershipEpoch != 0 {
		t.Fatalf("GET must not claim dest ownership copy cannot read: %+v", got)
	}
	jobs, _ := mem.ListMigrateJobs(t.Context(), clusterRow.ID, 50)
	if len(jobs) != 0 {
		t.Fatalf("migrate must not persist a job copy cannot read: %+v", jobs)
	}
}

func TestPhase32FailedLiveLeavesSourceRunning(t *testing.T) {
	s, mem, token := testServer(t)
	fake := migrate.NewFake()
	fake.FailLive = true
	s.Migrate = fake
	clusterRow, _ := mem.GetCluster(t.Context())
	control := seedNode(t, mem, clusterRow.ID, debianInv(), false)
	worker := appdb.Node{ID: uuid.NewString(), ClusterID: clusterRow.ID, Name: "box-b", Role: "worker"}
	if err := mem.UpsertNode(t.Context(), worker); err != nil {
		t.Fatal(err)
	}
	wlID := uuid.NewString()
	fake.SetSourceRunning(wlID, true)
	if err := mem.CreateWorkload(t.Context(), appdb.Workload{
		ID: wlID, ClusterID: clusterRow.ID, NodeID: control.ID,
		OwnerNodeID: control.ID, DesiredNodeID: control.ID,
		Name: "stay", Kind: "vm", Status: "running",
	}); err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)
	body := `{"dest_node_id":"` + worker.ID + `","mode":"live"}`
	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/workloads/"+wlID+"/migrate", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ := ts.Client().Do(req)
	raw, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusConflict {
		t.Fatalf("failed live %d %s", res.StatusCode, raw)
	}
	if !strings.Contains(string(raw), "source remains running") {
		t.Fatalf("error %s", raw)
	}
	got, _ := mem.GetWorkload(t.Context(), clusterRow.ID, wlID)
	if got.NodeID != control.ID || got.OwnershipEpoch != 0 {
		t.Fatalf("source ownership must not move %+v", got)
	}
	if got.Status != "running" {
		t.Fatalf("source must stay running: %s %s", got.Status, got.Reason)
	}
	if fake.DestRunning(wlID) || fake.DestIncoming(wlID) {
		t.Fatal("dest must be aborted")
	}
}

func TestPhase32CTLiveRefusedOfflineOK(t *testing.T) {
	s, mem, token := testServer(t)
	s.Migrate = migrate.NewFake()
	clusterRow, _ := mem.GetCluster(t.Context())
	control := seedNode(t, mem, clusterRow.ID, debianInv(), false)
	worker := appdb.Node{ID: uuid.NewString(), ClusterID: clusterRow.ID, Name: "box-b", Role: "worker"}
	if err := mem.UpsertNode(t.Context(), worker); err != nil {
		t.Fatal(err)
	}
	wlID := uuid.NewString()
	s.Migrate.(*migrate.Fake).SetSourceRunning(wlID, true)
	if err := mem.CreateWorkload(t.Context(), appdb.Workload{
		ID: wlID, ClusterID: clusterRow.ID, NodeID: control.ID,
		OwnerNodeID: control.ID, DesiredNodeID: control.ID,
		Name: "ct-1", Kind: "system-container", Status: "running",
	}); err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)
	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/workloads/"+wlID+"/migrate", strings.NewReader(
		`{"dest_node_id":"`+worker.ID+`","mode":"live"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ := ts.Client().Do(req)
	raw, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("ct live %d %s", res.StatusCode, raw)
	}
	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/workloads/"+wlID+"/migrate", strings.NewReader(
		`{"dest_node_id":"`+worker.ID+`","mode":"offline"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(req)
	raw, _ = io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("ct offline %d %s", res.StatusCode, raw)
	}
	got, _ := mem.GetWorkload(t.Context(), clusterRow.ID, wlID)
	if got.NodeID != worker.ID || got.OwnershipEpoch != 1 {
		t.Fatalf("ct ownership %+v", got)
	}
}

func TestPhase32MissingDestAgentLeavesSource(t *testing.T) {
	s, mem, token := testServer(t)
	clusterRow, _ := mem.GetCluster(t.Context())
	control := seedNode(t, mem, clusterRow.ID, debianInv(), false)
	worker := appdb.Node{ID: uuid.NewString(), ClusterID: clusterRow.ID, Name: "box-b", Role: "worker"}
	if err := mem.UpsertNode(t.Context(), worker); err != nil {
		t.Fatal(err)
	}
	wlID := uuid.NewString()
	if err := mem.CreateWorkload(t.Context(), appdb.Workload{
		ID: wlID, ClusterID: clusterRow.ID, NodeID: control.ID,
		OwnerNodeID: control.ID, DesiredNodeID: control.ID,
		Name: "solo", Kind: "vm", Status: "running",
	}); err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)
	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/workloads/"+wlID+"/migrate", strings.NewReader(
		`{"dest_node_id":"`+worker.ID+`","mode":"live"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ := ts.Client().Do(req)
	raw, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusFailedDependency {
		t.Fatalf("missing dest agent %d %s", res.StatusCode, raw)
	}
	got, _ := mem.GetWorkload(t.Context(), clusterRow.ID, wlID)
	if got.NodeID != control.ID || got.Status != "running" || got.OwnershipEpoch != 0 {
		t.Fatalf("source must be untouched %+v", got)
	}
}

func TestPhase32SameNodeAndCPUHostRefused(t *testing.T) {
	s, mem, token := testServer(t)
	s.Migrate = migrate.NewFake()
	clusterRow, _ := mem.GetCluster(t.Context())
	control := seedNode(t, mem, clusterRow.ID, debianInv(), false)
	worker := appdb.Node{ID: uuid.NewString(), ClusterID: clusterRow.ID, Name: "box-b", Role: "worker"}
	if err := mem.UpsertNode(t.Context(), worker); err != nil {
		t.Fatal(err)
	}
	wlID := uuid.NewString()
	applied, _ := json.Marshal(map[string]any{"argv": []string{"/usr/bin/qemu-system-x86_64", "-cpu", "host"}})
	if err := mem.CreateWorkload(t.Context(), appdb.Workload{
		ID: wlID, ClusterID: clusterRow.ID, NodeID: control.ID,
		OwnerNodeID: control.ID, DesiredNodeID: control.ID,
		Name: "host-cpu", Kind: "vm", Status: "running", AppliedJSON: applied,
	}); err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)
	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/workloads/"+wlID+"/migrate", strings.NewReader(
		`{"dest_node_id":"`+control.ID+`","mode":"offline"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ := ts.Client().Do(req)
	if res.StatusCode != http.StatusUnprocessableEntity {
		raw, _ := io.ReadAll(res.Body)
		_ = res.Body.Close()
		t.Fatalf("same node %d %s", res.StatusCode, raw)
	}
	_ = res.Body.Close()
	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/workloads/"+wlID+"/migrate", strings.NewReader(
		`{"dest_node_id":"`+worker.ID+`","mode":"live"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(req)
	raw, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusConflict && res.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("cpu host live %d %s", res.StatusCode, raw)
	}
	if !strings.Contains(string(raw), "cpu host") && !strings.Contains(string(raw), "source remains") {
		t.Fatalf("cpu host %s", raw)
	}
	got, _ := mem.GetWorkload(t.Context(), clusterRow.ID, wlID)
	if got.NodeID != control.ID {
		t.Fatalf("cpu host live must not move %+v", got)
	}
}

func TestPhase32AdaptMigrateNilIsUnavailableNotLocalStart(t *testing.T) {
	if _, ok := AdaptMigrate(nil).(migrateUnavailable); !ok {
		t.Fatal("nil client must be unavailable")
	}
	if _, ok := AdaptMigrate(struct{}{}).(migrateUnavailable); !ok {
		t.Fatal("unknown client must be unavailable")
	}
	if _, ok := AdaptMigrate(agentrpc.Client{}).(migrateUnavailable); !ok {
		t.Fatal("empty agent socket must be unavailable")
	}
	fake := migrate.NewFake()
	if AdaptMigrate(fake) != fake {
		t.Fatal("AdaptMigrate must type-assert a Runtime client")
	}
	unavail := AdaptMigrate(nil)
	if err := unavail.StartDest(t.Context(), "wl"); err == nil {
		t.Fatal("unavailable must not start dest")
	}

	s, mem, token := testServer(t)
	s.Migrate = AdaptMigrate(nil)
	clusterRow, _ := mem.GetCluster(t.Context())
	control := seedNode(t, mem, clusterRow.ID, debianInv(), false)
	worker := appdb.Node{ID: uuid.NewString(), ClusterID: clusterRow.ID, Name: "box-b", Role: "worker"}
	if err := mem.UpsertNode(t.Context(), worker); err != nil {
		t.Fatal(err)
	}
	wlID := uuid.NewString()
	if err := mem.CreateWorkload(t.Context(), appdb.Workload{
		ID: wlID, ClusterID: clusterRow.ID, NodeID: control.ID,
		OwnerNodeID: control.ID, DesiredNodeID: control.ID,
		Name: "solo", Kind: "vm", Status: "running",
	}); err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)
	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/workloads/"+wlID+"/migrate", strings.NewReader(
		`{"dest_node_id":"`+worker.ID+`","mode":"live"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ := ts.Client().Do(req)
	raw, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusFailedDependency {
		t.Fatalf("adapt nil dest agent %d %s", res.StatusCode, raw)
	}
	if !strings.Contains(string(raw), "dest agent is not connected") {
		t.Fatalf("honesty %s", raw)
	}
	got, _ := mem.GetWorkload(t.Context(), clusterRow.ID, wlID)
	if got.NodeID != control.ID || got.Status != "running" || got.OwnershipEpoch != 0 {
		t.Fatalf("source must be untouched %+v", got)
	}
}

func TestPhase32OCIMigrateIs422NotCopiedLikeVM(t *testing.T) {
	s, mem, token := testServer(t)
	s.Migrate = migrate.NewFake()
	clusterRow, _ := mem.GetCluster(t.Context())
	control := seedNode(t, mem, clusterRow.ID, debianInv(), false)
	worker := appdb.Node{ID: uuid.NewString(), ClusterID: clusterRow.ID, Name: "box-b", Role: "worker"}
	if err := mem.UpsertNode(t.Context(), worker); err != nil {
		t.Fatal(err)
	}
	wlID := uuid.NewString()
	if err := mem.CreateWorkload(t.Context(), appdb.Workload{
		ID: wlID, ClusterID: clusterRow.ID, NodeID: control.ID,
		OwnerNodeID: control.ID, DesiredNodeID: control.ID,
		Name: "app", Kind: "oci", Status: "running",
	}); err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)
	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/workloads/"+wlID+"/migrate", strings.NewReader(
		`{"dest_node_id":"`+worker.ID+`","mode":"offline"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ := ts.Client().Do(req)
	raw, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("oci migrate %d %s", res.StatusCode, raw)
	}
	if !strings.Contains(string(raw), "OCI migrate recreates") {
		t.Fatalf("honesty %s", raw)
	}
	got, _ := mem.GetWorkload(t.Context(), clusterRow.ID, wlID)
	if got.NodeID != control.ID || got.OwnershipEpoch != 0 {
		t.Fatalf("oci must not copy like a VM %+v", got)
	}
}

func TestPhase32DestDiskLocatorDiffersAndZFSIsNotShared(t *testing.T) {
	s, mem, _ := testServer(t)
	clusterRow, _ := mem.GetCluster(t.Context())
	control := seedNode(t, mem, clusterRow.ID, debianInv(), false)
	worker := appdb.Node{ID: uuid.NewString(), ClusterID: clusterRow.ID, Name: "box-b", Role: "worker"}
	if err := mem.UpsertNode(t.Context(), worker); err != nil {
		t.Fatal(err)
	}
	srcPool := appdb.StoragePool{
		ID: uuid.NewString(), ClusterID: clusterRow.ID, NodeID: control.ID, Name: "tank",
		BackendType: storage.BackendZFS, Status: storage.StatusAvailable,
		RootPath: storage.ZFSMountRoot + "/1",
	}
	if err := mem.CreateStoragePool(t.Context(), srcPool); err != nil {
		t.Fatal(err)
	}
	destPool := appdb.StoragePool{
		ID: uuid.NewString(), ClusterID: clusterRow.ID, NodeID: worker.ID, Name: "tank-b",
		BackendType: storage.BackendZFS, Status: storage.StatusAvailable,
		RootPath: storage.ZFSMountRoot + "/2",
	}
	if err := mem.CreateStoragePool(t.Context(), destPool); err != nil {
		t.Fatal(err)
	}
	vol := appdb.Volume{
		ID: uuid.NewString(), ClusterID: clusterRow.ID, NodeID: control.ID, PoolID: srcPool.ID,
		Class: storage.ClassVMDisk, Kind: storage.KindBlock, Status: storage.StatusAvailable,
		BackendType: storage.BackendZFS, BackendRef: "/dev/zvol/tank/" + uuid.NewString(),
	}
	if err := mem.CreateVolume(t.Context(), vol); err != nil {
		t.Fatal(err)
	}
	wl := appdb.Workload{
		ID: uuid.NewString(), ClusterID: clusterRow.ID, NodeID: control.ID,
		OwnerNodeID: control.ID, DesiredNodeID: control.ID, Name: "zfs-vm", Kind: "vm",
	}
	if err := mem.CreateWorkload(t.Context(), wl); err != nil {
		t.Fatal(err)
	}
	if err := mem.CreateWorkloadDisk(t.Context(), appdb.WorkloadDisk{
		ID: uuid.NewString(), ClusterID: clusterRow.ID, WorkloadID: wl.ID, VolumeID: vol.ID,
	}); err != nil {
		t.Fatal(err)
	}
	shared, copies, err := s.migrateDisks(t.Context(), wl, &worker)
	if err != nil {
		t.Fatal(err)
	}
	if shared {
		t.Fatal("local ZFS must not skip copy as shared")
	}
	if len(copies) != 1 || copies[0].DestPath == "" || copies[0].DestPath == copies[0].SourcePath {
		t.Fatalf("dest locator must differ %+v", copies)
	}

	nfsPool := appdb.StoragePool{
		ID: uuid.NewString(), ClusterID: clusterRow.ID, NodeID: control.ID, Name: "nfs",
		BackendType: storage.BackendNFS, Status: storage.StatusAvailable,
		RootPath: storage.NFSMountRoot + "/p",
	}
	if err := mem.CreateStoragePool(t.Context(), nfsPool); err != nil {
		t.Fatal(err)
	}
	if err := mem.UpsertDatastore(t.Context(), appdb.Datastore{PoolID: nfsPool.ID, Kind: storage.BackendNFS, Locator: "nas.example:/export"}); err != nil {
		t.Fatal(err)
	}
	nfsVol := appdb.Volume{
		ID: uuid.NewString(), ClusterID: clusterRow.ID, NodeID: control.ID, PoolID: nfsPool.ID,
		Class: storage.ClassVMDisk, Kind: storage.KindBlock, Status: storage.StatusAvailable,
		BackendType: storage.BackendNFS, BackendRef: "library/iso/a.iso",
	}
	if err := mem.CreateVolume(t.Context(), nfsVol); err != nil {
		t.Fatal(err)
	}
	nfsWL := appdb.Workload{
		ID: uuid.NewString(), ClusterID: clusterRow.ID, NodeID: control.ID, Name: "nfs-vm", Kind: "vm",
	}
	if err := mem.CreateWorkload(t.Context(), nfsWL); err != nil {
		t.Fatal(err)
	}
	if err := mem.CreateWorkloadDisk(t.Context(), appdb.WorkloadDisk{
		ID: uuid.NewString(), ClusterID: clusterRow.ID, WorkloadID: nfsWL.ID, VolumeID: nfsVol.ID,
	}); err != nil {
		t.Fatal(err)
	}
	nfsShared, nfsCopies, err := s.migrateDisks(t.Context(), nfsWL, &worker)
	if err != nil {
		t.Fatal(err)
	}
	if !nfsShared || len(nfsCopies) != 1 {
		t.Fatalf("nfs datastore must be shared %+v", nfsCopies)
	}

	bare := appdb.StoragePool{
		ID: uuid.NewString(), ClusterID: clusterRow.ID, NodeID: control.ID, Name: "dir",
		BackendType: storage.BackendDirectory, Status: storage.StatusAvailable,
		RootPath: storage.DefaultPoolPath,
	}
	if err := mem.CreateStoragePool(t.Context(), bare); err != nil {
		t.Fatal(err)
	}
	bareVol := appdb.Volume{
		ID: uuid.NewString(), ClusterID: clusterRow.ID, NodeID: control.ID, PoolID: bare.ID,
		Class: storage.ClassVMDisk, Status: storage.StatusAvailable,
		BackendRef: "volumes/vm-disk/boot.qcow2",
	}
	if err := mem.CreateVolume(t.Context(), bareVol); err != nil {
		t.Fatal(err)
	}
	bareWL := appdb.Workload{ID: uuid.NewString(), ClusterID: clusterRow.ID, NodeID: control.ID, Name: "dir-vm", Kind: "vm"}
	if err := mem.CreateWorkload(t.Context(), bareWL); err != nil {
		t.Fatal(err)
	}
	if err := mem.CreateWorkloadDisk(t.Context(), appdb.WorkloadDisk{
		ID: uuid.NewString(), ClusterID: clusterRow.ID, WorkloadID: bareWL.ID, VolumeID: bareVol.ID,
	}); err != nil {
		t.Fatal(err)
	}
	lonely := appdb.Node{ID: uuid.NewString(), ClusterID: clusterRow.ID, Name: "empty-dest", Role: "worker"}
	_, _, err = s.migrateDisks(t.Context(), bareWL, &lonely)
	if err == nil || !strings.Contains(err.Error(), destLocatorMissing) {
		t.Fatalf("missing dest pool must fail closed: %v", err)
	}

	missingWL := appdb.Workload{ID: uuid.NewString(), ClusterID: clusterRow.ID, NodeID: control.ID, Name: "ghost-vm", Kind: "vm"}
	if err := mem.CreateWorkload(t.Context(), missingWL); err != nil {
		t.Fatal(err)
	}
	if err := mem.CreateWorkloadDisk(t.Context(), appdb.WorkloadDisk{
		ID: uuid.NewString(), ClusterID: clusterRow.ID, WorkloadID: missingWL.ID, VolumeID: uuid.NewString(),
	}); err != nil {
		t.Fatal(err)
	}
	_, _, err = s.migrateDisks(t.Context(), missingWL, &worker)
	if err == nil || !strings.Contains(err.Error(), "workload volume is missing") {
		t.Fatalf("missing volume must fail closed: %v", err)
	}

	downVol := appdb.Volume{
		ID: uuid.NewString(), ClusterID: clusterRow.ID, NodeID: control.ID, PoolID: srcPool.ID,
		Class: storage.ClassVMDisk, Status: storage.StatusUnavailable,
		BackendRef: "volumes/vm-disk/down.qcow2",
	}
	if err := mem.CreateVolume(t.Context(), downVol); err != nil {
		t.Fatal(err)
	}
	downWL := appdb.Workload{ID: uuid.NewString(), ClusterID: clusterRow.ID, NodeID: control.ID, Name: "down-vm", Kind: "vm"}
	if err := mem.CreateWorkload(t.Context(), downWL); err != nil {
		t.Fatal(err)
	}
	if err := mem.CreateWorkloadDisk(t.Context(), appdb.WorkloadDisk{
		ID: uuid.NewString(), ClusterID: clusterRow.ID, WorkloadID: downWL.ID, VolumeID: downVol.ID,
	}); err != nil {
		t.Fatal(err)
	}
	_, copies, err = s.migrateDisks(t.Context(), downWL, &worker)
	if err == nil || !strings.Contains(err.Error(), "storage is unavailable") || copies != nil {
		t.Fatalf("unavailable volume must fail closed: %+v %v", copies, err)
	}
}

func TestDestVolumeLocatorRejectsClassEscape(t *testing.T) {
	dest := &appdb.Node{ID: uuid.NewString()}
	vol := &appdb.Volume{ID: uuid.NewString(), Class: "../../etc"}
	pools := []appdb.StoragePool{{
		NodeID: dest.ID, RootPath: storage.DefaultPoolPath, BackendType: storage.BackendDirectory,
	}}
	got, err := destVolumeLocator(dest, vol, "/src", pools)
	if err == nil || !strings.Contains(err.Error(), destLocatorMissing) || got != "" {
		t.Fatalf("class escape must fail closed: %q %v", got, err)
	}
}

func TestMigrateDisksRejectsRelativeSourceEscape(t *testing.T) {
	s, mem, _ := testServer(t)
	clusterRow, _ := mem.GetCluster(t.Context())
	control := seedNode(t, mem, clusterRow.ID, debianInv(), false)
	worker := appdb.Node{ID: uuid.NewString(), ClusterID: clusterRow.ID, Name: "box-b", Role: "worker"}
	if err := mem.UpsertNode(t.Context(), worker); err != nil {
		t.Fatal(err)
	}
	destPool := appdb.StoragePool{
		ID: uuid.NewString(), ClusterID: clusterRow.ID, NodeID: worker.ID, Name: "dest",
		BackendType: storage.BackendDirectory, Status: storage.StatusAvailable,
		RootPath: storage.DefaultPoolPath,
	}
	if err := mem.CreateStoragePool(t.Context(), destPool); err != nil {
		t.Fatal(err)
	}
	srcPool := appdb.StoragePool{
		ID: uuid.NewString(), ClusterID: clusterRow.ID, NodeID: control.ID, Name: "src",
		BackendType: storage.BackendDirectory, Status: storage.StatusAvailable,
		RootPath: storage.DefaultPoolPath,
	}
	if err := mem.CreateStoragePool(t.Context(), srcPool); err != nil {
		t.Fatal(err)
	}
	vol := appdb.Volume{
		ID: uuid.NewString(), ClusterID: clusterRow.ID, NodeID: control.ID, PoolID: srcPool.ID,
		Class: storage.ClassVMDisk, Status: storage.StatusAvailable, BackendRef: "../../etc/passwd",
	}
	if err := mem.CreateVolume(t.Context(), vol); err != nil {
		t.Fatal(err)
	}
	wl := appdb.Workload{ID: uuid.NewString(), ClusterID: clusterRow.ID, NodeID: control.ID, Name: "escape-vm", Kind: "vm"}
	if err := mem.CreateWorkload(t.Context(), wl); err != nil {
		t.Fatal(err)
	}
	if err := mem.CreateWorkloadDisk(t.Context(), appdb.WorkloadDisk{
		ID: uuid.NewString(), ClusterID: clusterRow.ID, WorkloadID: wl.ID, VolumeID: vol.ID,
	}); err != nil {
		t.Fatal(err)
	}
	_, copies, err := s.migrateDisks(t.Context(), wl, &worker)
	if err == nil || !strings.Contains(err.Error(), "volume locator is invalid") || copies != nil {
		t.Fatalf("relative source escape must fail closed: %+v %v", copies, err)
	}
}

type failUpdateWorkloadObservedStore struct {
	appdb.Store
}

func (f failUpdateWorkloadObservedStore) UpdateWorkloadObserved(context.Context, appdb.Workload) error {
	return errors.New("persist failed")
}

func TestPhase32MigrateWorkloadJSONMatchesGETWhenObservedPersistFails(t *testing.T) {
	s, mem, token := testServer(t)
	s.Migrate = migrate.NewFake()
	clusterRow, _ := mem.GetCluster(t.Context())
	control := seedNode(t, mem, clusterRow.ID, debianInv(), false)
	worker := appdb.Node{ID: uuid.NewString(), ClusterID: clusterRow.ID, Name: "box-b", Role: "worker", Hostname: "box-b"}
	if err := mem.UpsertNode(t.Context(), worker); err != nil {
		t.Fatal(err)
	}
	wlID := uuid.NewString()
	s.Migrate.(*migrate.Fake).SetSourceRunning(wlID, true)
	if err := mem.CreateWorkload(t.Context(), appdb.Workload{
		ID: wlID, ClusterID: clusterRow.ID, NodeID: control.ID,
		OwnerNodeID: control.ID, DesiredNodeID: control.ID,
		Name: "move-me", Kind: "vm", Status: "running", Reason: "on-source",
	}); err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)
	s.Store = failUpdateWorkloadObservedStore{Store: mem}

	body := `{"dest_node_id":"` + worker.ID + `","mode":"live"}`
	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/workloads/"+wlID+"/migrate", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ := ts.Client().Do(req)
	raw, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("migrate %d %s", res.StatusCode, raw)
	}
	var job map[string]any
	if err := json.Unmarshal(raw, &job); err != nil {
		t.Fatal(err)
	}
	nested, _ := job["workload"].(map[string]any)
	if nested == nil {
		t.Fatalf("migrate body missing workload %s", raw)
	}
	got, err := mem.GetWorkload(t.Context(), clusterRow.ID, wlID)
	if err != nil || got == nil {
		t.Fatalf("get workload %v", err)
	}
	if nested["reason"] != got.Reason {
		t.Fatalf("200 workload.reason %v must match GET %q", nested["reason"], got.Reason)
	}
	if nested["status"] != got.Status {
		t.Fatalf("200 workload.status %v must match GET %q", nested["status"], got.Status)
	}
	if got.Reason == "live migrate completed" {
		t.Fatal("failed observed persist must not invent dest reason in the store")
	}
}

type failUpdateMigrateJobStore struct {
	appdb.Store
}

func (f failUpdateMigrateJobStore) UpdateMigrateJob(context.Context, appdb.MigrateJob) error {
	return errors.New("persist failed")
}

func TestPhase32MigrateFailsClosedWhenJobPersistFails(t *testing.T) {
	s, mem, token := testServer(t)
	s.Migrate = migrate.NewFake()
	clusterRow, _ := mem.GetCluster(t.Context())
	control := seedNode(t, mem, clusterRow.ID, debianInv(), false)
	worker := appdb.Node{ID: uuid.NewString(), ClusterID: clusterRow.ID, Name: "box-b", Role: "worker", Hostname: "box-b"}
	if err := mem.UpsertNode(t.Context(), worker); err != nil {
		t.Fatal(err)
	}
	wlID := uuid.NewString()
	s.Migrate.(*migrate.Fake).SetSourceRunning(wlID, true)
	if err := mem.CreateWorkload(t.Context(), appdb.Workload{
		ID: wlID, ClusterID: clusterRow.ID, NodeID: control.ID,
		OwnerNodeID: control.ID, DesiredNodeID: control.ID,
		Name: "move-me", Kind: "vm", Status: "running",
	}); err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)
	s.Store = failUpdateMigrateJobStore{Store: mem}

	body := `{"dest_node_id":"` + worker.ID + `","mode":"live"}`
	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/workloads/"+wlID+"/migrate", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ := ts.Client().Do(req)
	raw, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusInternalServerError {
		t.Fatalf("migrate persist %d %s", res.StatusCode, raw)
	}
	if !strings.Contains(string(raw), "could not record migrate job") {
		t.Fatalf("migrate persist body %s", raw)
	}
}
