package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/no-dal/ndl-ce/internal/lxc"
)

func TestPrepareCTRewritesObsoleteApparmor(t *testing.T) {
	dir := t.TempDir()
	e := &lxc.Engine{DataDir: dir, SkipHostCmds: true, FakeUnpack: true}
	id := uuid.NewString()
	root := filepath.Join(dir, "rootfs", id)
	_, err := e.Create(context.Background(), lxc.Spec{
		WorkloadID: id, Name: "prep", ImagePin: "imported",
		VolumeID: uuid.NewString(), RootfsPath: root, SkipImage: true, NoStart: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	legacy := "lxc.apparmor.profile = lxc-container-ndl-nesting\n"
	if err := os.WriteFile(filepath.Join(dir, "runtime", "lxc", id, "config"), []byte(legacy), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := prepareCT(e, []string{id}); err != nil {
		t.Fatal(err)
	}
	cfg, err := os.ReadFile(filepath.Join(dir, "runtime", "lxc", id, "config"))
	if err != nil {
		t.Fatal(err)
	}
	body := string(cfg)
	if strings.Contains(body, "lxc-container-ndl-nesting") {
		t.Fatal(body)
	}
	if _, err := os.Stat("/sys/kernel/security/apparmor"); err == nil {
		if !strings.Contains(body, "lxc.apparmor.profile = generated") {
			t.Fatal(body)
		}
		if !strings.Contains(body, "lxc.apparmor.allow_nesting = 1") {
			t.Fatal(body)
		}
		if !strings.Contains(body, "lxc.seccomp.allow_nesting = 1") {
			t.Fatal(body)
		}
		if !strings.Contains(body, "lxc.hook.start-host = "+lxc.BinNestingApparmor) {
			t.Fatal(body)
		}
		if strings.Contains(body, "unconfined") {
			t.Fatal(body)
		}
	}
	if _, err := os.Stat(filepath.Join(root, "usr", "bin", "nano")); err == nil {
		t.Fatal("SkipHostCmds prepare must not copy host nano")
	}
}

func TestPrepareCTAppliesGuestNano(t *testing.T) {
	if _, err := os.Lstat("/usr/bin/nano"); err != nil {
		t.Skip("host nano is required")
	}
	dir := t.TempDir()
	e := &lxc.Engine{DataDir: dir, SkipHostCmds: true, FakeUnpack: true}
	id := uuid.NewString()
	root := filepath.Join(dir, "rootfs", id)
	_, err := e.Create(context.Background(), lxc.Spec{
		WorkloadID: id, Name: "prep-nano", ImagePin: "imported",
		VolumeID: uuid.NewString(), RootfsPath: root, SkipImage: true, NoStart: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	e.SkipHostCmds = false
	e.FakeUnpack = false
	if err := prepareCT(e, []string{id}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "usr", "bin", "nano")); err != nil {
		t.Fatal("prepare must install nano after remount")
	}
}

func TestPrepareCTUsage(t *testing.T) {
	if err := prepareCT(&lxc.Engine{DataDir: t.TempDir()}, nil); err == nil {
		t.Fatal("want usage error")
	}
}
