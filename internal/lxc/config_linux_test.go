//go:build linux

package lxc

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func TestCgroupRuleFromStatUsesHostNode(t *testing.T) {
	if got := cgroupRuleFromStat("/dev/null"); got != "c 1:3 rwm" {
		t.Fatalf("got %q", got)
	}
	if got := cgroupRuleFromStat("/dev/does-not-exist"); got != "" {
		t.Fatalf("missing node must not get a rule: %q", got)
	}
	if got := cgroupRuleFromStat("/dev"); got != "" {
		t.Fatalf("directory must not get a rule: %q", got)
	}
}

func TestCgroupRuleFromStatResolvesSymlinkInsideDev(t *testing.T) {
	dir := t.TempDir()
	link := filepath.Join(dir, "pci-0000:02:00.0-render")
	if err := os.Symlink("/dev/zero", link); err != nil {
		t.Fatal(err)
	}
	if got := cgroupRuleFromStat(link); got != "c 1:5 rwm" {
		t.Fatalf("by-path style symlink must resolve to its node: %q", got)
	}
	file := filepath.Join(dir, "regular")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	toFile := filepath.Join(dir, "to-file")
	if err := os.Symlink(file, toFile); err != nil {
		t.Fatal(err)
	}
	if got := cgroupRuleFromStat(toFile); got != "" {
		t.Fatalf("symlink to a regular file must not get a rule: %q", got)
	}
}

func TestCgroupRuleFromStatRejectsNodeOutsideDev(t *testing.T) {
	dir := t.TempDir()
	node := filepath.Join(dir, "fake-null")
	if err := syscall.Mknod(node, syscall.S_IFCHR|0o600, 1<<8|3); err != nil {
		t.Skipf("mknod needs CAP_MKNOD: %v", err)
	}
	if got := cgroupRuleFromStat(node); got != "" {
		t.Fatalf("node outside /dev must not get a rule: %q", got)
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink(node, link); err != nil {
		t.Fatal(err)
	}
	if got := cgroupRuleFromStat(link); got != "" {
		t.Fatalf("symlink escaping /dev must not get a rule: %q", got)
	}
}

func TestRenderConfigUsesStatForHostNodes(t *testing.T) {
	cfg := RenderConfig(Spec{
		WorkloadID: "33333333-3333-4333-8333-333333333333", Name: "ct", RootfsPath: "/vol/root",
		Privileged: true, GPUDevices: []string{"/dev/zero"},
	})
	if !strings.Contains(cfg, "lxc.cgroup2.devices.allow = c 1:5 rwm\n") {
		t.Fatal(cfg)
	}
}
