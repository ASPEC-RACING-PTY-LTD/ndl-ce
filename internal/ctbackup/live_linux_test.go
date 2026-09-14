package ctbackup

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestLiveDirectoryRootfsArchive(t *testing.T) {
	src := os.Getenv("NDL_LIVE_CT_ROOTFS")
	if src == "" {
		t.Skip("set NDL_LIVE_CT_ROOTFS to archive a disposable Directory container root")
	}
	if _, err := os.Stat(BinTar); err != nil {
		t.Skip("tar is not installed")
	}
	marker := filepath.Join(src, "etc", "ndl-backup-live-marker")
	if err := os.MkdirAll(filepath.Dir(marker), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(marker, []byte("ndl-backup-ct-test"), 0o644); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(t.TempDir(), "live.tar.zst")
	unit := os.Getenv("NDL_LIVE_CT_UNIT")
	res, err := Archive(context.Background(), src, dest, unit, []byte(`{"kind":"system-container","name":"ndl-backup-ct-test"}`))
	if err != nil {
		t.Fatal(err)
	}
	if res.Size < 1 || res.SHA256 == "" {
		t.Fatalf("archive %+v", res)
	}
	out := filepath.Join(t.TempDir(), "restore")
	if err := Extract(context.Background(), res.Dest, out); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(filepath.Join(out, "etc", "ndl-backup-live-marker"))
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "ndl-backup-ct-test" {
		t.Fatalf("marker %s", body)
	}
	meta, err := ReadMeta(context.Background(), res.Dest)
	if err != nil {
		t.Fatal(err)
	}
	if string(meta) == "" {
		t.Fatal("metadata missing")
	}
}
