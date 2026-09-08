package migration

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func plantPopulatedRootfs(t *testing.T, root string) {
	t.Helper()
	for _, d := range []string{"sbin", "usr/bin", "bin", "etc"} {
		if err := os.MkdirAll(filepath.Join(root, filepath.FromSlash(d)), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "sbin", "init"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "etc", "hosts"), []byte("127.0.0.1 localhost\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func plantSkeletonRootfs(t *testing.T, root string) {
	t.Helper()
	for _, d := range []string{"dev", "etc", "proc", "sys"} {
		if err := os.MkdirAll(filepath.Join(root, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, ".ndl-rootfs-ok"), []byte("ok\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestVerifyCopiedRootfsRejectsSkeleton(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	plantSkeletonRootfs(t, root)
	if err := VerifyCopiedRootfs(root); err == nil {
		t.Fatal("skeleton must not verify")
	}
	if _, err := ObservedContainerTransfer(root, false); err == nil {
		t.Fatal("skeleton must not observe transfer_complete")
	}
}

func TestVerifyCopiedRootfsRejectsEmpty(t *testing.T) {
	t.Parallel()
	if err := VerifyCopiedRootfs(t.TempDir()); err == nil {
		t.Fatal("empty dest must not verify")
	}
	if err := VerifyCopiedRootfs(""); err == nil {
		t.Fatal("empty path must not verify")
	}
}

func TestVerifyCopiedRootfsRequiresExecutableInit(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	for _, d := range []string{"sbin", "usr", "bin"} {
		if err := os.MkdirAll(filepath.Join(root, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "sbin", "init"), []byte("not exec\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := VerifyCopiedRootfs(root); err == nil {
		t.Fatal("non-executable init must not verify")
	}
	if err := os.Chmod(filepath.Join(root, "sbin", "init"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := VerifyCopiedRootfs(root); err != nil {
		t.Fatal(err)
	}
	obs, err := ObservedContainerTransfer(root, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(obs) != 2 || obs[0] != VerifyTransfer || obs[1] != VerifyConfig {
		t.Fatalf("observed %v", obs)
	}
}

func TestCopyLocalRootfsEmptySourceFails(t *testing.T) {
	t.Parallel()
	src := t.TempDir()
	dest := filepath.Join(t.TempDir(), "rootfs")
	if err := CopyLocalRootfs(src, dest); err == nil {
		t.Fatal("empty source must not copy")
	}
}

func TestCopyLocalRootfsSkeletonSourceFails(t *testing.T) {
	t.Parallel()
	src := t.TempDir()
	plantSkeletonRootfs(t, src)
	dest := filepath.Join(t.TempDir(), "rootfs")
	if err := CopyLocalRootfs(src, dest); err == nil {
		t.Fatal("skeleton source must not copy")
	}
}

func TestCopyLocalRootfsPreservesRelativeSymlink(t *testing.T) {
	t.Parallel()
	src := t.TempDir()
	plantPopulatedRootfs(t, src)
	if err := os.MkdirAll(filepath.Join(src, "lib", "systemd"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "lib", "systemd", "systemd"), []byte("init\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(src, "sbin", "init")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../lib/systemd/systemd", filepath.Join(src, "sbin", "init")); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(t.TempDir(), "rootfs")
	if err := CopyLocalRootfs(src, dest); err != nil {
		t.Fatal(err)
	}
	got, err := os.Readlink(filepath.Join(dest, "sbin", "init"))
	if err != nil || got != "../lib/systemd/systemd" {
		t.Fatalf("symlink %v %s", err, got)
	}
	if err := VerifyCopiedRootfs(dest); err != nil {
		t.Fatal(err)
	}
}

func TestObservedContainerTransferRejectsSkeletonLevels(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	plantSkeletonRootfs(t, root)
	obs, err := ObservedContainerTransfer(root, true)
	if err == nil || obs != nil {
		t.Fatalf("must not mark verified: %v %v", obs, err)
	}
	if !strings.Contains(err.Error(), "init") && !strings.Contains(err.Error(), "skeleton") {
		t.Fatalf("error %v", err)
	}
}
