package qemu

import (
	"context"
	"strings"
	"testing"
)

func TestCopyOfflineSkipHostCmdsErrors(t *testing.T) {
	e := &Engine{DataDir: t.TempDir(), SkipHostCmds: true}
	res, err := e.CopyOffline(context.Background(), BackupCopy, "/var/lib/ndl/storage/local/a.qcow2", "/tmp/ndl-backup/a.qcow2")
	if err == nil {
		t.Fatal("CopyOffline must error under SkipHostCmds")
	}
	if res.SHA256 == "fixture" || res.Size == 1 {
		t.Fatalf("must not invent checksum or size: %+v", res)
	}
	st, err := e.CopyOffline(context.Background(), BackupStat, "", "/tmp/ndl-backup")
	if err == nil {
		t.Fatal("BackupStat must error under SkipHostCmds")
	}
	if st.Size == 1 {
		t.Fatalf("must not invent size: %+v", st)
	}
}

func TestConvertOfflineSkipHostCmdsErrors(t *testing.T) {
	e := &Engine{DataDir: t.TempDir(), SkipHostCmds: true}
	err := e.ConvertOffline(context.Background(), ConvertRequest{
		SourcePath:   "/var/lib/ndl/storage/local/a.qcow2",
		DestPath:     "/var/lib/ndl/storage/local/b.qcow2",
		SourceFormat: "qcow2", DestFormat: "qcow2",
	})
	if err == nil {
		t.Fatal("ConvertOffline must error under SkipHostCmds")
	}
}

func TestBackupStatJailsHostPaths(t *testing.T) {
	e := &Engine{DataDir: t.TempDir()}
	if _, err := e.CopyOffline(context.Background(), BackupStat, "", "/etc/passwd"); err == nil {
		t.Fatal("/etc/passwd must not be probed")
	}
	dir := t.TempDir()
	st, err := e.CopyOffline(context.Background(), BackupStat, "", dir)
	if err != nil {
		t.Fatal(err)
	}
	if st.Size != 1 {
		t.Fatalf("allowed temp dir %+v", st)
	}
}

func TestCopyOfflineBackupCopyAllowsArtifactDest(t *testing.T) {
	e := &Engine{DataDir: t.TempDir(), SkipHostCmds: true}
	src := "/var/lib/ndl/storage/local/a.qcow2"
	_, err := e.CopyOffline(context.Background(), BackupCopy, src, "/var/lib/ndl/backups/a.qcow2")
	if err == nil || strings.Contains(err.Error(), "storage root") {
		t.Fatalf("artifact dest must not require storage root: %v", err)
	}
	if _, err := e.CopyOffline(context.Background(), BackupCopy, src, "/etc/passwd"); err == nil {
		t.Fatal("backup copy must not write /etc")
	}
	_, err = e.CopyOffline(context.Background(), BackupReplace, src, "/var/lib/ndl/backups/a.qcow2")
	if err == nil {
		t.Fatal("replace dest must stay a disk locator")
	}
	if !strings.Contains(err.Error(), "storage root") && !strings.Contains(err.Error(), "disk_path") && !strings.Contains(err.Error(), "VolumeHandle") {
		t.Fatalf("replace dest: %v", err)
	}
	_, err = e.CopyOffline(context.Background(), BackupReplace, src, "/var/lib/ndl/storage/local/a.qcow2")
	if err == nil || !strings.Contains(err.Error(), "host commands skipped") {
		t.Fatalf("replace disk dest: %v", err)
	}
}

func TestConvertOfflineStillJailsArtifactDest(t *testing.T) {
	e := &Engine{DataDir: t.TempDir(), SkipHostCmds: true}
	err := e.ConvertOffline(context.Background(), ConvertRequest{
		SourcePath: "/var/lib/ndl/storage/local/a.qcow2", DestPath: "/var/lib/ndl/backups/a.qcow2",
		SourceFormat: "qcow2", DestFormat: "qcow2",
	})
	if err == nil || !(strings.Contains(err.Error(), "storage root") || strings.Contains(err.Error(), "disk_path") || strings.Contains(err.Error(), "VolumeHandle")) {
		t.Fatalf("VM convert dest must stay under storage: %v", err)
	}
}
