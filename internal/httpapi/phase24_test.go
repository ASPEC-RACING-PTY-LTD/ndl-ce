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
	"github.com/no-dal/ndl-ce/internal/storage"
)

type fakeVerify struct {
	sha     string
	imgOK   bool
	reason  string
	extract []byte
}

func (f *fakeVerify) VerifyBackup(_ context.Context, _, expected string) (qemu.VerifyResult, error) {
	obs := f.sha
	if obs == "" {
		obs = expected
	}
	if expected != "" && obs != expected {
		return qemu.VerifyResult{ObservedSHA256: obs, Status: qemu.VerifyUnverified, Reason: "checksum mismatch"}, nil
	}
	if !f.imgOK {
		return qemu.VerifyResult{ObservedSHA256: obs, Status: qemu.VerifyUnverified, Reason: firstNonEmpty(f.reason, "qemu-img check was not executed")}, nil
	}
	return qemu.VerifyResult{ObservedSHA256: obs, QEMUImgOK: true, Status: qemu.VerifyVerified}, nil
}

func (f *fakeVerify) ExtractBackup(_ context.Context, _, guestPath, dest string) (qemu.ExtractResult, error) {
	if f.extract == nil {
		return qemu.ExtractResult{GuestPath: guestPath, Status: qemu.VerifyUnavailable, Reason: "libguestfs extract is not configured"}, nil
	}
	if err := os.WriteFile(dest, f.extract, 0o600); err != nil {
		return qemu.ExtractResult{}, err
	}
	return qemu.ExtractResult{GuestPath: guestPath, DestPath: dest, Size: int64(len(f.extract)), SHA256: "abc", Status: qemu.VerifyVerified}, nil
}

func seedBackupArtifact(t *testing.T, ts *httptest.Server, cookie, poolID, netID string) (wlID, artID, srcStatus string) {
	t.Helper()
	body := `{"name":"web","kind":"vm","pool_id":"` + poolID + `","network_id":"` + netID + `","cpus":1,"memory_bytes":268435456}`
	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/workloads", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("workload %d %s", res.StatusCode, raw)
	}
	var wl map[string]any
	_ = json.Unmarshal(raw, &wl)
	wlID, _ = wl["id"].(string)
	srcStatus, _ = wl["status"].(string)

	dir := t.TempDir()
	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/backups/targets", strings.NewReader(`{"name":"local-disk","kind":"local","locator":"`+dir+`"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(req)
	raw, _ = io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("target %d %s", res.StatusCode, raw)
	}
	var tgt map[string]any
	_ = json.Unmarshal(raw, &tgt)
	runBody, _ := json.Marshal(map[string]any{"workload_id": wlID, "target_id": tgt["id"]})
	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/backups/run", strings.NewReader(string(runBody)))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(req)
	raw, _ = io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusAccepted {
		t.Fatalf("run %d %s", res.StatusCode, raw)
	}
	req, _ = http.NewRequest("GET", ts.URL+"/api/v1/backups/artifacts", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(req)
	raw, _ = io.ReadAll(res.Body)
	_ = res.Body.Close()
	var listed struct {
		Items []map[string]any `json:"items"`
	}
	_ = json.Unmarshal(raw, &listed)
	if len(listed.Items) == 0 {
		t.Fatalf("artifacts %s", raw)
	}
	if listed.Items[0]["verify_status"] != appdb.BackupUnverified {
		t.Fatalf("catalog without verify must be unverified: %s", raw)
	}
	artID, _ = listed.Items[0]["id"].(string)
	return wlID, artID, srcStatus
}

func TestPhase24ChecksumMismatchUnverified(t *testing.T) {
	s, mem, token := testServer(t)
	cluster, _ := mem.GetCluster(context.Background())
	nodeID := uuid.NewString()
	_ = mem.UpsertNode(context.Background(), appdb.Node{ID: nodeID, ClusterID: cluster.ID, Name: "local"})
	poolID, netID := seedCompute(t, mem, cluster.ID, nodeID)
	s.Storage = fakeStorage{vol: storage.CreateVolumeResult{Handle: storage.VolumeHandle{
		BackendType: storage.BackendDirectory, BackendRef: "volumes/vm-disk/boot.qcow2",
		Kind: storage.KindBlock, Class: storage.ClassVMDisk, Format: storage.FormatQCOW2,
	}}}
	s.VM = &fakeVM{}
	s.Backup = &fakeBackup{}
	s.Verify = &fakeVerify{sha: "deadbeef"}
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close)
	cookie := claimAdmin(t, ts, token)
	_, artID, _ := seedBackupArtifact(t, ts, cookie, poolID, netID)

	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/backups/artifacts/"+artID+"/verify", strings.NewReader(`{"mode":"open"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusAccepted {
		t.Fatalf("verify %d %s", res.StatusCode, raw)
	}
	var art map[string]any
	_ = json.Unmarshal(raw, &art)
	if art["verify_status"] != appdb.BackupUnverified || art["verify_error"] != "checksum mismatch" {
		t.Fatalf("checksum mismatch must be unverified: %s", raw)
	}
}

func TestPhase24ThrowawayDoesNotTouchSource(t *testing.T) {
	s, mem, token := testServer(t)
	cluster, _ := mem.GetCluster(context.Background())
	nodeID := uuid.NewString()
	_ = mem.UpsertNode(context.Background(), appdb.Node{ID: nodeID, ClusterID: cluster.ID, Name: "local"})
	poolID, netID := seedCompute(t, mem, cluster.ID, nodeID)
	s.Storage = fakeStorage{vol: storage.CreateVolumeResult{Handle: storage.VolumeHandle{
		BackendType: storage.BackendDirectory, BackendRef: "volumes/vm-disk/boot.qcow2",
		Kind: storage.KindBlock, Class: storage.ClassVMDisk, Format: storage.FormatQCOW2,
	}}}
	s.VM = &fakeVM{}
	s.Backup = &fakeBackup{}
	s.Verify = &fakeVerify{imgOK: true}
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close)
	cookie := claimAdmin(t, ts, token)
	wlID, artID, srcStatus := seedBackupArtifact(t, ts, cookie, poolID, netID)

	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/backups/artifacts/"+artID+"/verify", strings.NewReader(`{"mode":"throwaway"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusAccepted {
		t.Fatalf("throwaway %d %s", res.StatusCode, raw)
	}
	var art map[string]any
	_ = json.Unmarshal(raw, &art)
	if art["verify_status"] != appdb.BackupVerified {
		t.Fatalf("throwaway %s", raw)
	}
	tid, _ := art["throwaway_workload_id"].(string)
	if tid == "" || tid == wlID {
		t.Fatalf("throwaway must mint a new UUID: %s", raw)
	}
	src, _ := mem.GetWorkload(context.Background(), cluster.ID, wlID)
	if src == nil || src.Status != srcStatus {
		t.Fatalf("source workload changed: %+v", src)
	}
	tw, _ := mem.GetWorkload(context.Background(), cluster.ID, tid)
	if tw == nil || !strings.HasPrefix(tw.Name, "verify-") {
		t.Fatalf("throwaway name %+v", tw)
	}
	nics, _ := mem.ListWorkloadNICs(context.Background(), cluster.ID, tid)
	if len(nics) == 0 {
		t.Fatal("throwaway still needs an isolated NIC")
	}
	srcNics, _ := mem.ListWorkloadNICs(context.Background(), cluster.ID, wlID)
	if len(srcNics) > 0 && nics[0].ID == srcNics[0].ID {
		t.Fatal("throwaway must not reuse the source NIC row")
	}
}

func TestPhase24RestoreFileAndViewerDenied(t *testing.T) {
	s, mem, token := testServer(t)
	cluster, _ := mem.GetCluster(context.Background())
	nodeID := uuid.NewString()
	_ = mem.UpsertNode(context.Background(), appdb.Node{ID: nodeID, ClusterID: cluster.ID, Name: "local"})
	poolID, netID := seedCompute(t, mem, cluster.ID, nodeID)
	s.Storage = fakeStorage{vol: storage.CreateVolumeResult{Handle: storage.VolumeHandle{
		BackendType: storage.BackendDirectory, BackendRef: "volumes/vm-disk/boot.qcow2",
		Kind: storage.KindBlock, Class: storage.ClassVMDisk, Format: storage.FormatQCOW2,
	}}}
	s.VM = &fakeVM{}
	s.Backup = &fakeBackup{}
	s.Verify = &fakeVerify{extract: []byte("nodal-host\n")}
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close)
	cookie := claimAdmin(t, ts, token)
	_, artID, _ := seedBackupArtifact(t, ts, cookie, poolID, netID)

	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/backups/artifacts/"+artID+"/restore-file", strings.NewReader(`{"path":"/etc/hostname"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("restore-file %d %s", res.StatusCode, raw)
	}
	if !strings.Contains(string(raw), "bm9kYWwtaG9zdAo=") {
		t.Fatalf("content %s", raw)
	}

	view := appdb.User{ID: uuid.NewString(), ClusterID: cluster.ID, Username: "view"}
	_ = mem.CreateUser(context.Background(), view)
	_ = mem.BindRole(context.Background(), cluster.ID, view.ID, rbac.Viewer)
	plain := "ndl_view_verify"
	_ = mem.CreateToken(context.Background(), appdb.APIToken{
		ID: uuid.NewString(), ClusterID: cluster.ID, UserID: view.ID, Name: "v",
		TokenHash: hashToken(plain), Prefix: "ndl_vv",
	})
	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/backups/artifacts/"+artID+"/verify", strings.NewReader(`{"mode":"open"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+plain)
	res, err = ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("viewer %d", res.StatusCode)
	}
}
