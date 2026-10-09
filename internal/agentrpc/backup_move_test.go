package agentrpc

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/no-dal/ndl-ce/internal/backup"
	"github.com/no-dal/ndl-ce/internal/backuphost"
)

func TestRelocateCopiesARepositoryWithBackupsAndFreesTheOldOne(t *testing.T) {
	dir := t.TempDir()
	h := &Handler{}
	h.Ident.Dir = dir
	host, err := h.backupHost()
	if err != nil {
		t.Fatal(err)
	}
	src := t.TempDir()
	if err := os.WriteFile(filepath.Join(src, "data.bin"), make([]byte, 300<<10), 0o644); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(backuphost.Request{Action: backuphost.ActionCapture, WorkloadID: "wl", WorkloadName: "app", CaptureMode: backup.CaptureModeFull})
	if _, err := host.Handle(context.Background(), backuphost.ActionCapture, src, string(raw)); err != nil {
		t.Fatal(err)
	}
	old := host.Root()
	if host.RestorePoints() != 1 {
		t.Fatalf("restore points %d", host.RestorePoints())
	}

	dest := filepath.Join(t.TempDir(), "pool", "backup-repo")
	if err := os.MkdirAll(filepath.Dir(dest), 0o750); err != nil {
		t.Fatal(err)
	}
	req, _ := json.Marshal(backuphost.Request{Action: backuphost.ActionRelocate, Path: dest})
	if _, err := h.relocateBackupRepo(context.Background(), string(req)); err != nil {
		t.Fatalf("a repository with backups must move: %v", err)
	}
	moved, err := h.backupHost()
	if err != nil {
		t.Fatal(err)
	}
	if moved.Root() != dest || moved.RestorePoints() != 1 {
		t.Fatalf("the moved repository must hold the backup: %s %d", moved.Root(), moved.RestorePoints())
	}
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Fatal("the old repository must be removed once the copy is in use")
	}
	if got, _ := os.ReadFile(h.backupRepoPathFile()); filepath.Clean(string(got[:len(got)-1])) != dest {
		t.Fatalf("path file %q", got)
	}
}
