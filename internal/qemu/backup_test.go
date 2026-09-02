package qemu

import (
	"context"
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
