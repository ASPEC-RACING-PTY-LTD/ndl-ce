package httpapi

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/no-dal/ndl-ce/internal/appdb"
	"github.com/no-dal/ndl-ce/internal/backup"
	"github.com/no-dal/ndl-ce/internal/lxc"
	"github.com/no-dal/ndl-ce/internal/qemu"
	"github.com/no-dal/ndl-ce/internal/storage"
)

func TestRestoreNewCTV2DestPullsControlObject(t *testing.T) {
	s, mem, token := testServer(t)
	cluster, _ := mem.GetCluster(t.Context())
	control := seedNode(t, mem, cluster.ID, debianInv(), false)
	worker := appdb.Node{ID: uuid.NewString(), ClusterID: cluster.ID, Name: "box-b", Role: "worker"}
	if err := mem.UpsertNode(t.Context(), worker); err != nil {
		t.Fatal(err)
	}
	poolID, netID := seedCompute(t, mem, cluster.ID, control.ID)
	localWL := &fakeWorkloads{}
	destWL := &fakeWorkloads{}
	localBK := &fakeBackup{}
	destBK := &fakeBackup{}
	s.Workloads = localWL
	s.Backup = localBK
	s.Storage = fakeStorage{vol: storage.CreateVolumeResult{Handle: storage.VolumeHandle{
		BackendType: storage.BackendDirectory, BackendRef: "volumes/container-root/x",
		Kind: storage.KindFilesystem, Class: storage.ClassContainerRoot, Format: storage.FormatDirectory,
	}}}
	s.destOverride = &destAgentOverride{
		listen:    "192.168.2.183:9444",
		workloads: destWL,
		backup:    destBK,
	}

	wlID := uuid.NewString()
	if err := mem.CreateWorkload(t.Context(), appdb.Workload{
		ID: wlID, ClusterID: cluster.ID, NodeID: control.ID, OwnerNodeID: control.ID, DesiredNodeID: control.ID,
		Name: "cert-dest-pull", Kind: lxc.KindSystemContainer, Status: lxc.StatusStopped,
		ImagePin: "alpine/3.21/amd64/default", CPUs: 1, MemoryBytes: lxc.DefaultMemoryBytes,
	}); err != nil {
		t.Fatal(err)
	}
	if err := mem.CreateWorkloadNIC(t.Context(), appdb.WorkloadNIC{
		ID: uuid.NewString(), ClusterID: cluster.ID, WorkloadID: wlID, NetworkID: netID,
	}); err != nil {
		t.Fatal(err)
	}
	tgtID := uuid.NewString()
	if err := mem.CreateBackupTarget(t.Context(), appdb.BackupTarget{
		ID: tgtID, ClusterID: cluster.ID, Name: "cert-minio", Kind: "minio",
		Locator: "s3://cert-bucket/prefix", Endpoint: "http://127.0.0.1:19000",
		Bucket: "cert-bucket", Status: appdb.BackupAvailable,
	}, "secret-pass", ""); err != nil {
		t.Fatal(err)
	}
	runID := uuid.NewString()
	if err := mem.CreateBackupRun(t.Context(), appdb.BackupRun{
		ID: runID, ClusterID: cluster.ID, TargetID: tgtID, WorkloadID: wlID, Status: appdb.BackupSucceeded,
	}); err != nil {
		t.Fatal(err)
	}
	artID := uuid.NewString()
	if err := mem.CreateBackupArtifact(t.Context(), appdb.BackupArtifact{
		ID: artID, ClusterID: cluster.ID, RunID: runID, WorkloadID: wlID,
		Format: backup.Format, CaptureMode: appdb.BackupCaptureFull,
		Namespace: "cert-dest-pull", BackupID: uuid.NewString(),
		Locator: "ndl-cab://backups/cert-dest-pull/" + artID, LocalComplete: true,
	}); err != nil {
		t.Fatal(err)
	}
	_ = poolID

	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)
	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/backups/artifacts/"+artID+"/restore", strings.NewReader(`{"mode":"new","target_node_id":"`+worker.ID+`"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ := ts.Client().Do(req)
	raw, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusAccepted {
		t.Fatalf("dest restore %d %s", res.StatusCode, raw)
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	newID, _ := out["restored_workload_id"].(string)
	if newID == "" {
		t.Fatalf("dest restore must return restored_workload_id: %s", raw)
	}
	got, _ := mem.GetWorkload(t.Context(), cluster.ID, newID)
	if got == nil || got.NodeID != worker.ID || got.Status == "unavailable" {
		t.Fatalf("dest-owned restore %+v", got)
	}
	if localWL.creates != 0 {
		t.Fatalf("must not CreateCT on the control unix agent: %+v", localWL)
	}
	if destWL.creates != 1 {
		t.Fatalf("dest must CreateCT once, got %d", destWL.creates)
	}
	if destWL.lastSpec.IP.IPv4Mode != lxc.IPModeDisabled {
		t.Fatalf("dest restore must not wait for dest DHCP: %+v", destWL.lastSpec.IP)
	}
	localV2 := 0
	destV2 := 0
	localArchive := 0
	destMkdir := 0
	for _, c := range localBK.copies {
		if c[0] == qemu.BackupV2Restore {
			localV2++
		}
		if strings.HasPrefix(c[0], qemu.BackupArchive) {
			localArchive++
		}
	}
	for _, c := range destBK.copies {
		if c[0] == qemu.BackupV2Restore {
			destV2++
		}
		if c[0] == qemu.BackupMkdir {
			destMkdir++
		}
	}
	if localV2 != 1 {
		t.Fatalf("control must restore V2 locally: %+v", localBK.copies)
	}
	if destV2 != 0 {
		t.Fatalf("dest must not v2-restore against the control target: %+v", destBK.copies)
	}
	if localArchive < 1 || destMkdir < 1 {
		t.Fatalf("control archive + dest mkdir required local=%+v dest=%+v", localBK.copies, destBK.copies)
	}
	if len(s.destOverride.pulled) != 1 || !strings.HasPrefix(s.destOverride.pulled[0].SourcePath, "http://") {
		t.Fatalf("dest must pull a dest-reachable object: %+v", s.destOverride.pulled)
	}
}

func TestRestoreNewCTV2DestDisconnectedStaysUnavailable(t *testing.T) {
	s, mem, token := testServer(t)
	cluster, _ := mem.GetCluster(t.Context())
	control := seedNode(t, mem, cluster.ID, debianInv(), false)
	worker := appdb.Node{ID: uuid.NewString(), ClusterID: cluster.ID, Name: "box-b", Role: "worker"}
	if err := mem.UpsertNode(t.Context(), worker); err != nil {
		t.Fatal(err)
	}
	_, netID := seedCompute(t, mem, cluster.ID, control.ID)
	localWL := &fakeWorkloads{}
	s.Workloads = localWL
	s.Backup = &fakeBackup{}
	s.Storage = fakeStorage{vol: storage.CreateVolumeResult{Handle: storage.VolumeHandle{
		BackendType: storage.BackendDirectory, BackendRef: "volumes/container-root/x",
		Kind: storage.KindFilesystem, Class: storage.ClassContainerRoot, Format: storage.FormatDirectory,
	}}}

	wlID := uuid.NewString()
	if err := mem.CreateWorkload(t.Context(), appdb.Workload{
		ID: wlID, ClusterID: cluster.ID, NodeID: control.ID,
		Name: "cert-dest-down", Kind: lxc.KindSystemContainer, Status: lxc.StatusStopped,
	}); err != nil {
		t.Fatal(err)
	}
	if err := mem.CreateWorkloadNIC(t.Context(), appdb.WorkloadNIC{
		ID: uuid.NewString(), ClusterID: cluster.ID, WorkloadID: wlID, NetworkID: netID,
	}); err != nil {
		t.Fatal(err)
	}
	tgtID := uuid.NewString()
	if err := mem.CreateBackupTarget(t.Context(), appdb.BackupTarget{
		ID: tgtID, ClusterID: cluster.ID, Name: "local", Kind: appdb.BackupLocal,
		Locator: t.TempDir(), Status: appdb.BackupAvailable,
	}, "", ""); err != nil {
		t.Fatal(err)
	}
	runID := uuid.NewString()
	if err := mem.CreateBackupRun(t.Context(), appdb.BackupRun{
		ID: runID, ClusterID: cluster.ID, TargetID: tgtID, WorkloadID: wlID, Status: appdb.BackupSucceeded,
	}); err != nil {
		t.Fatal(err)
	}
	artID := uuid.NewString()
	if err := mem.CreateBackupArtifact(t.Context(), appdb.BackupArtifact{
		ID: artID, ClusterID: cluster.ID, RunID: runID, WorkloadID: wlID,
		Format: backup.Format, CaptureMode: appdb.BackupCaptureFull,
		Namespace: "cert-dest-down", BackupID: uuid.NewString(),
	}); err != nil {
		t.Fatal(err)
	}

	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)
	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/backups/artifacts/"+artID+"/restore", strings.NewReader(`{"mode":"new","target_node_id":"`+worker.ID+`"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ := ts.Client().Do(req)
	raw, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusAccepted {
		t.Fatalf("disconnected dest restore %d %s", res.StatusCode, raw)
	}
	var out map[string]any
	_ = json.Unmarshal(raw, &out)
	newID, _ := out["restored_workload_id"].(string)
	got, _ := mem.GetWorkload(t.Context(), cluster.ID, newID)
	if got == nil || got.NodeID != worker.ID || got.Status != "unavailable" || !strings.Contains(got.Reason, "dest agent is not connected") {
		t.Fatalf("disconnected dest must not pretend the guest exists %+v", got)
	}
	if localWL.creates != 0 {
		t.Fatalf("must not CreateCT on the control unix agent: %+v", localWL)
	}
}
