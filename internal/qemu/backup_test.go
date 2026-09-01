package qemu

import (
	"context"
	"testing"
)

func TestCopyOfflineSkipHostCmdsIsStandaloneConvert(t *testing.T) {
	e := &Engine{DataDir: t.TempDir(), SkipHostCmds: true}
	res, err := e.CopyOffline(context.Background(), BackupCopy, "/var/lib/ndl/storage/local/a.qcow2", "/tmp/ndl-backup/a.qcow2")
	if err != nil {
		t.Fatal(err)
	}
	if res.Format != "qcow2" || res.SHA256 == "" {
		t.Fatalf("%+v", res)
	}
	st, err := e.CopyOffline(context.Background(), BackupStat, "", "/tmp/ndl-backup")
	if err != nil || st.Size != 1 {
		t.Fatalf("stat %+v %v", st, err)
	}
}
