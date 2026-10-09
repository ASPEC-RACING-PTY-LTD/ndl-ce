package backuphost

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/no-dal/ndl-ce/internal/backup"
)

func TestDiskCaptureBacksUpOneImageAndCleansStaging(t *testing.T) {
	h, err := Open(Options{Root: t.TempDir(), Settings: Settings{MaxLocalBytes: 1 << 30, MinHostFreeBytes: 1}})
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()
	pool := t.TempDir()
	disk := filepath.Join(pool, "vm.qcow2")
	data := randData(7, 3<<20)
	mustWrite(t, disk, data)
	mustWrite(t, filepath.Join(pool, "other.qcow2"), []byte("not part of the backup"))
	res, err := h.Handle(context.Background(), ActionCapture, disk, mustJSON(Request{
		Action: ActionCapture, WorkloadID: "vm-1", WorkloadName: "rsdw", CaptureMode: CaptureModeDisk, DiskPath: disk,
	}))
	if err != nil {
		t.Fatal(err)
	}
	var out Result
	_ = json.Unmarshal([]byte(res.Extra), &out)
	if out.BackupID == "" || out.FilesScanned != 1 || out.CaptureMode != CaptureModeDisk {
		t.Fatalf("result %+v", out)
	}
	entries, _ := os.ReadDir(pool)
	if len(entries) != 2 {
		t.Fatalf("staging must be removed, pool holds %d entries", len(entries))
	}
	dst := t.TempDir()
	if _, err := h.Handle(context.Background(), ActionRestore, dst, mustJSON(Request{Namespace: out.Namespace, BackupID: out.BackupID, Dest: dst})); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(filepath.Join(dst, "disk.qcow2"))
	if !bytes.Equal(got, data) {
		t.Fatal("restored disk differs")
	}
	if _, err := os.Stat(disk); err != nil {
		t.Fatal("the source disk must be untouched")
	}
}

func TestRemoteOnlyExpiryKeepsLocalCopy(t *testing.T) {
	remote := t.TempDir()
	spec := TargetSpec{ID: "r", Kind: "local", Locator: remote}
	h, err := Open(Options{Root: t.TempDir(), Settings: Settings{MaxLocalBytes: 1 << 30, MinHostFreeBytes: 1}})
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()
	src := t.TempDir()
	mustWrite(t, filepath.Join(src, "a.bin"), randData(8, 200<<10))
	out := captureTo(t, h, src, spec)
	waitProtected(t, h, out.BackupID)
	if _, err := h.Handle(context.Background(), ActionExpire, "", mustJSON(Request{
		Action: ActionExpire, Namespace: out.Namespace, BackupID: out.BackupID, Target: spec, RemoteOnly: true,
	})); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(remote, "backups", out.Namespace, "manifests", out.BackupID+".snap")); !os.IsNotExist(err) {
		t.Fatal("the remote manifest must be removed")
	}
	st, err := h.repo.LoadState(out.Namespace, out.BackupID)
	if err != nil || st.Remote != backup.RemoteNone {
		t.Fatalf("the local copy must stay and be marked local-only: %+v %v", st, err)
	}
	if _, err := h.repo.LoadManifest(out.Namespace, out.BackupID); err != nil {
		t.Fatal("local manifest must stay")
	}
}

func TestVerifyFindsMissingData(t *testing.T) {
	root := t.TempDir()
	h, err := Open(Options{Root: root, Settings: Settings{MaxLocalBytes: 1 << 30, MinHostFreeBytes: 1}})
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()
	src := t.TempDir()
	mustWrite(t, filepath.Join(src, "a.bin"), randData(9, 300<<10))
	captureTo(t, h, src, TargetSpec{})
	res, err := h.verify(context.Background())
	if err != nil || len(res) != 1 || !res[0].OK || res[0].Checked == 0 {
		t.Fatalf("healthy repository: %+v %v", res, err)
	}
	packs, _ := filepath.Glob(filepath.Join(root, "packs", "*.pack"))
	for _, p := range packs {
		raw, _ := os.ReadFile(p)
		for i := range raw {
			raw[i] ^= 0xff
		}
		_ = os.WriteFile(p, raw, 0o640)
	}
	res, err = h.verify(context.Background())
	if err != nil || len(res) != 1 || res[0].OK || res[0].Error == "" {
		t.Fatalf("damaged data must fail verification: %+v %v", res, err)
	}
}

func TestKeyExportMarksExported(t *testing.T) {
	root := t.TempDir()
	keyPath := filepath.Join(t.TempDir(), "backup-master.key")
	h, err := Open(Options{Root: root, KeyPath: keyPath, Settings: Settings{MinHostFreeBytes: 1}})
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()
	if status(t, h).Workspace.KeyExported {
		t.Fatal("a new key is not exported yet")
	}
	res, err := h.Handle(context.Background(), ActionKey, "", mustJSON(Request{Action: ActionKey}))
	if err != nil {
		t.Fatal(err)
	}
	var out Result
	_ = json.Unmarshal([]byte(res.Extra), &out)
	master, _ := os.ReadFile(filepath.Join(root, "master.key"))
	if out.Key != hex.EncodeToString(master) {
		t.Fatal("exported key must be the repository key")
	}
	if !status(t, h).Workspace.KeyExported {
		t.Fatal("export must be recorded")
	}
}

func TestWipeRemoteDeletesOnlyBackupObjectsAndKeepsLocal(t *testing.T) {
	remote := t.TempDir()
	spec := TargetSpec{ID: "w", Kind: "local", Locator: remote}
	h, err := Open(Options{Root: t.TempDir(), Settings: Settings{MaxLocalBytes: 1 << 30, MinHostFreeBytes: 1}})
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()
	src := t.TempDir()
	mustWrite(t, filepath.Join(src, "a.bin"), randData(11, 200<<10))
	out := captureTo(t, h, src, spec)
	waitProtected(t, h, out.BackupID)
	mustWrite(t, filepath.Join(remote, "backups", "rsdw", "2026-10-03", "disk.qcow2"), []byte("legacy"))
	mustWrite(t, filepath.Join(remote, "other", "keep.txt"), []byte("not ours"))
	res, err := h.Handle(context.Background(), ActionWipeRemote, "", mustJSON(Request{Action: ActionWipeRemote, Target: spec}))
	if err != nil {
		t.Fatal(err)
	}
	var st Result
	_ = json.Unmarshal([]byte(res.Extra), &st)
	if st.Wipe == nil || st.Wipe.ObjectsDeleted == 0 {
		t.Fatalf("wipe %+v", st.Wipe)
	}
	if n := countFiles(t, filepath.Join(remote, "backups"), ".pack") + countFiles(t, filepath.Join(remote, "backups"), ".qcow2"); n != 0 {
		t.Fatalf("%d backup objects left", n)
	}
	if _, err := os.Stat(filepath.Join(remote, "other", "keep.txt")); err != nil {
		t.Fatal("objects outside backups/ must not be touched")
	}
	ps, err := h.repo.LoadState(out.Namespace, out.BackupID)
	if err != nil || ps.Remote != backup.RemoteNone {
		t.Fatalf("local copy must stay, local-only: %+v %v", ps, err)
	}
}
