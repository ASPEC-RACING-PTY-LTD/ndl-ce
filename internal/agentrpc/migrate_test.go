package agentrpc

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	agentv1 "github.com/no-dal/ndl-ce/gen/nodal/agent/v1"
	"github.com/no-dal/ndl-ce/internal/qemu"
)

func TestDestPullActionExtractsArchivesAndDirectories(t *testing.T) {
	if got := destPullAction("http://10.0.0.1:9/pack", "/var/lib/ndl/storage/local/volumes/container-root/dest"); got != qemu.BackupExtractRoot {
		t.Fatalf("directory dest pull %s", got)
	}
	if got := destPullAction("https://objects.example/a.tar.zst", "/var/lib/ndl/backups/a.tar.zst"); got != qemu.BackupExtractRoot {
		t.Fatalf("archive pull %s", got)
	}
	if got := destPullAction("s3://ndl/disk.qcow2", "/var/lib/ndl/storage/local/volumes/vm-disk/a.qcow2"); got != qemu.BackupCopy {
		t.Fatalf("qcow2 pull %s", got)
	}
}

func TestNamePulledArchiveSniffsGzipAndZstd(t *testing.T) {
	dir := t.TempDir()
	gz := filepath.Join(dir, "g")
	if err := os.WriteFile(gz, []byte{0x1f, 0x8b, 0x08, 0x00}, 0o600); err != nil {
		t.Fatal(err)
	}
	named, err := namePulledArchive(gz)
	if err != nil || !strings.HasSuffix(named, ".tar.gz") {
		t.Fatalf("gzip pull %s %v", named, err)
	}
	zst := filepath.Join(dir, "z")
	if err := os.WriteFile(zst, []byte{0x28, 0xb5, 0x2f, 0xfd}, 0o600); err != nil {
		t.Fatal(err)
	}
	named, err = namePulledArchive(zst)
	if err != nil || !strings.HasSuffix(named, ".tar.zst") {
		t.Fatalf("zstd pull %s %v", named, err)
	}
}

func TestExecComputeMigratePullAndCopySkipWorkloadUUID(t *testing.T) {
	h := &Handler{}
	_, err := h.execComputeMigrate(context.Background(), &agentv1.ComputeMigrate{
		Action: "pull_volume", Uri: "file:///tmp/pack",
	})
	if err == nil {
		t.Fatal("expected pull source validation")
	}
	if strings.Contains(err.Error(), "workload_id must be a UUID") {
		t.Fatalf("dest pull must not require a guest UUID: %v", err)
	}
	if !strings.Contains(err.Error(), "http") && !strings.Contains(err.Error(), "s3") {
		t.Fatalf("expected pull source error, got %v", err)
	}

	_, err = h.execComputeMigrate(context.Background(), &agentv1.ComputeMigrate{Action: "prepare_incoming"})
	if err == nil || !strings.Contains(err.Error(), "workload_id must be a UUID") {
		t.Fatalf("guest actions still require a UUID: %v", err)
	}
}
