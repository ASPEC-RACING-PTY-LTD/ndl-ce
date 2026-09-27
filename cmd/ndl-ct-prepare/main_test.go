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

func TestPrepareCTRewritesDevicePolicyForGPUContainer(t *testing.T) {
	dir := t.TempDir()
	e := &lxc.Engine{DataDir: dir, SkipHostCmds: true, FakeUnpack: true}
	id := uuid.NewString()
	_, err := e.Create(context.Background(), lxc.Spec{
		WorkloadID: id, Name: "gpu", ImagePin: "imported",
		VolumeID: uuid.NewString(), RootfsPath: filepath.Join(dir, "rootfs", id), SkipImage: true, NoStart: true,
		GPUDevices: []string{"/dev/nvidia0", "/dev/nvidiactl"},
	})
	if err != nil {
		t.Fatal(err)
	}
	cfgPath := filepath.Join(dir, "runtime", "lxc", id, "config")
	stale := "lxc.cgroup2.devices.allow = c 195:0 rwm\nlxc.cgroup2.devices.allow = c 195:255 rwm\n"
	if err := os.WriteFile(cfgPath, []byte(stale), 0o640); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := prepareCT(e, []string{id}); err != nil {
			t.Fatal(err)
		}
		cfg, err := os.ReadFile(cfgPath)
		if err != nil {
			t.Fatal(err)
		}
		body := string(cfg)
		for _, want := range []string{
			"lxc.cgroup2.devices.deny = a\n",
			"lxc.cgroup2.devices.allow = c 1:3 rwm\n",
			"lxc.cgroup2.devices.allow = c 1:5 rwm\n",
			"lxc.cgroup2.devices.allow = c 136:* rwm\n",
			"lxc.cgroup2.devices.allow = c 195:0 rwm\n",
			"lxc.cgroup2.devices.allow = c 195:255 rwm\n",
		} {
			if !strings.Contains(body, want) {
				t.Fatalf("prepare %d missing %q:\n%s", i, want, body)
			}
		}
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
