package backuphost

import (
	"bytes"
	"context"
	"encoding/json"
	"math/rand"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/no-dal/ndl-ce/internal/backup"
	"github.com/no-dal/ndl-ce/internal/diskguard"
)

func randData(seed int64, n int) []byte {
	b := make([]byte, n)
	rand.New(rand.NewSource(seed)).Read(b)
	return b
}

func captureTo(t *testing.T, h *Host, src string, spec TargetSpec) Result {
	t.Helper()
	res, err := h.Handle(context.Background(), ActionCapture, src, mustJSON(Request{
		Action: ActionCapture, WorkloadID: "wl-s", WorkloadName: "storage",
		CaptureMode: backup.CaptureModeFull, Target: spec,
	}))
	if err != nil {
		t.Fatal(err)
	}
	var out Result
	if err := json.Unmarshal([]byte(res.Extra), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func status(t *testing.T, h *Host) Result {
	t.Helper()
	res, err := h.Handle(context.Background(), ActionStatus, "", mustJSON(Request{Action: ActionStatus}))
	if err != nil {
		t.Fatal(err)
	}
	var out Result
	_ = json.Unmarshal([]byte(res.Extra), &out)
	return out
}

func waitProtected(t *testing.T, h *Host, backupID string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		for _, p := range status(t, h).Points {
			if p.BackupID == backupID && p.Remote == backup.RemoteProtected {
				return
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("restore point %s never reached protected", backupID)
}

func countFiles(t *testing.T, dir, ext string) int {
	t.Helper()
	n := 0
	_ = filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() && filepath.Ext(p) == ext {
			n++
		}
		return nil
	})
	return n
}

// The October 2026 emergency cleanup deleted the repository directory and,
// with it, the only copy of the key every remote backup is sealed with.
func TestRepositoryKeySurvivesRepositoryDeletion(t *testing.T) {
	root := filepath.Join(t.TempDir(), "backup-repo")
	keyPath := filepath.Join(t.TempDir(), "keys", "backup-master.key")
	remote := t.TempDir()
	spec := TargetSpec{ID: "local-r", Kind: "local", Locator: remote}
	src := t.TempDir()
	mustWrite(t, filepath.Join(src, "data.bin"), randData(1, 200<<10))

	h, err := Open(Options{Root: root, KeyPath: keyPath, Settings: Settings{MaxLocalBytes: 1 << 30, MinHostFreeBytes: 1, UploadWorkers: 1}})
	if err != nil {
		t.Fatal(err)
	}
	out := captureTo(t, h, src, spec)
	waitProtected(t, h, out.BackupID)
	h.Close()
	if fi, err := os.Stat(keyPath); err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("key must be preserved outside the repository with mode 0600: %v %v", fi, err)
	}

	if err := os.RemoveAll(root); err != nil {
		t.Fatal(err)
	}
	h2, err := Open(Options{Root: root, KeyPath: keyPath, Settings: Settings{MinHostFreeBytes: 1}})
	if err != nil {
		t.Fatal(err)
	}
	defer h2.Close()
	repo, man, err := backup.FetchRemote(context.Background(), backup.LocalTarget{Root: remote}, h2.repo.Keys(), out.Namespace, out.BackupID, t.TempDir())
	if err != nil {
		t.Fatalf("remote backup must stay restorable after the repository is deleted: %v", err)
	}
	dst := t.TempDir()
	if err := repo.Restore(context.Background(), man, backup.RestoreOptions{Dest: dst}); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(filepath.Join(dst, "data.bin"))
	if !bytes.Equal(got, randData(1, 200<<10)) {
		t.Fatal("restored data differs")
	}
}

func TestKeyMismatchIsReportedNotOverwritten(t *testing.T) {
	root := t.TempDir()
	keyPath := filepath.Join(t.TempDir(), "backup-master.key")
	preserved := bytes.Repeat([]byte{7}, 32)
	if err := os.WriteFile(keyPath, preserved, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "master.key"), bytes.Repeat([]byte{9}, 32), 0o600); err != nil {
		t.Fatal(err)
	}
	h, err := Open(Options{Root: root, KeyPath: keyPath, Settings: Settings{MinHostFreeBytes: 1}})
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()
	if st := status(t, h); st.Workspace.KeyNote == "" {
		t.Fatal("a differing preserved key must be reported")
	}
	if got, _ := os.ReadFile(keyPath); !bytes.Equal(got, preserved) {
		t.Fatal("the preserved key must never be overwritten")
	}
}

func TestExpireRemovesRemoteCopyAndMaintenanceSweepsPacks(t *testing.T) {
	remote := t.TempDir()
	spec := TargetSpec{ID: "local-x", Kind: "local", Locator: remote}
	h, err := Open(Options{Root: t.TempDir(), Settings: Settings{MaxLocalBytes: 1 << 30, MinHostFreeBytes: 1, UploadWorkers: 2}})
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()
	src := t.TempDir()
	mustWrite(t, filepath.Join(src, "a.bin"), randData(2, 300<<10))
	first := captureTo(t, h, src, spec)
	waitProtected(t, h, first.BackupID)
	mustWrite(t, filepath.Join(src, "a.bin"), randData(3, 300<<10))
	second := captureTo(t, h, src, spec)
	waitProtected(t, h, second.BackupID)
	packsBefore := countFiles(t, remote, ".pack")

	res, err := h.Handle(context.Background(), ActionExpire, "", mustJSON(Request{
		Action: ActionExpire, Namespace: first.Namespace, BackupID: first.BackupID,
		Target: spec, DeferGC: true,
	}))
	if err != nil {
		t.Fatal(err)
	}
	var out Result
	_ = json.Unmarshal([]byte(res.Extra), &out)
	if out.RemoteExpire == nil || !out.RemoteExpire.ManifestDeleted || out.GC != nil {
		t.Fatalf("expire result %+v", out)
	}
	if _, err := os.Stat(filepath.Join(remote, "backups", first.Namespace, "manifests", first.BackupID+".snap")); !os.IsNotExist(err) {
		t.Fatal("the expired manifest must be removed from the target")
	}
	if st := status(t, h); st.Workspace.PendingRemoteSweep == 0 {
		t.Fatal("expired packs must wait for a sweep")
	}

	res, err = h.Handle(context.Background(), ActionGC, "", mustJSON(Request{Action: ActionGC}))
	if err != nil {
		t.Fatal(err)
	}
	_ = json.Unmarshal([]byte(res.Extra), &out)
	if out.GC == nil || out.GC.Local.PacksDeleted == 0 || out.GC.Remote["local-x"].PacksDeleted == 0 {
		t.Fatalf("maintenance must reclaim local and remote packs: %+v", out.GC)
	}
	if countFiles(t, remote, ".pack") >= packsBefore {
		t.Fatal("remote packs did not shrink")
	}
	if st := status(t, h); st.Workspace.LastGC == nil || st.Workspace.PendingRemoteSweep != 0 {
		t.Fatalf("status must report the last maintenance run: %+v", st.Workspace)
	}
	// The remaining point still restores from the target.
	repo, man, err := backup.FetchRemote(context.Background(), backup.LocalTarget{Root: remote}, h.repo.Keys(), second.Namespace, second.BackupID, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.Restore(context.Background(), man, backup.RestoreOptions{Dest: t.TempDir()}); err != nil {
		t.Fatal(err)
	}
}

func TestFailedCaptureTriggersCleanup(t *testing.T) {
	h, err := Open(Options{Root: t.TempDir(), Settings: Settings{MaxLocalBytes: 1 << 20, MinHostFreeBytes: 1}})
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()
	src := t.TempDir()
	for i := 0; i < 6; i++ {
		mustWrite(t, filepath.Join(src, "f"+string(rune('a'+i))+".bin"), randData(int64(10+i), 16<<20))
	}
	_, err = h.Handle(context.Background(), ActionCapture, src, mustJSON(Request{
		Action: ActionCapture, WorkloadID: "wl-f", WorkloadName: "fail", CaptureMode: backup.CaptureModeFull,
	}))
	if err == nil {
		t.Fatal("the workspace limit must stop the capture")
	}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		st := status(t, h)
		if st.Workspace.LastGC != nil && st.Workspace.Usage != nil && st.Workspace.Usage.PackBytes == 0 {
			if st.Workspace.LastGC.Reason != "failed capture" {
				t.Fatalf("reason %q", st.Workspace.LastGC.Reason)
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("packs from the failed capture were not reclaimed: %+v", status(t, h).Workspace.Usage)
}

func TestProtectHostRaisesReserveToDiskProtectionThreshold(t *testing.T) {
	root := t.TempDir()
	h, err := Open(Options{Root: root, ProtectHost: true, Settings: Settings{MinHostFreeBytes: 1}})
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()
	total, _, err := fsStat(root)
	if err != nil {
		t.Skip("statfs unavailable")
	}
	_, critical, _ := diskguard.DefaultPolicy().Thresholds(total)
	if got := h.effectiveReserve(1); got != critical {
		t.Fatalf("effective reserve %d, want the critical threshold %d", got, critical)
	}
	h.protectHost = false
	if got := h.effectiveReserve(1); got != 1 {
		t.Fatalf("without host protection the configured reserve applies, got %d", got)
	}
}

func TestOpenRemovesStaleRemoteRestoreDirs(t *testing.T) {
	root := t.TempDir()
	stale := filepath.Join(root, "remote-restore-123")
	mustWrite(t, filepath.Join(stale, "packs", "x.pack"), make([]byte, 4096))
	h, err := Open(Options{Root: root, Settings: Settings{MinHostFreeBytes: 1}})
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatal("interrupted restore staging must be removed")
	}
	if ws := status(t, h).Workspace; ws.StaleRestoreDirsRemoved != 1 || ws.StaleRestoreBytesRemoved != 4096 {
		t.Fatalf("status %+v", ws)
	}
}
