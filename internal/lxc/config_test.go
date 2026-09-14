package lxc

import (
	"os"
	"runtime"
	"strings"
	"testing"
)

func TestHostLXCOverridesUsesGeneratedNestingWhenApparmor(t *testing.T) {
	got := hostLXCOverrides(Spec{})
	if _, err := os.Stat("/sys/kernel/security/apparmor"); err != nil {
		if !strings.Contains(got, "unconfined") {
			t.Fatalf("want unconfined without securityfs, got %q", got)
		}
		return
	}
	assertGeneratedNesting(t, got)
	if strings.Contains(got, "unconfined") {
		t.Fatal("must not unconfine every container when AppArmor is usable")
	}
	if strings.Contains(got, "lxc-container-ndl-nesting") {
		t.Fatal("must not use the static nesting profile")
	}
	if strings.Contains(got, "lxc.apparmor.profile = unconfined") {
		t.Fatal(got)
	}
	for _, line := range strings.Split(got, "\n") {
		if strings.TrimSpace(line) == "mount," {
			t.Fatal("No-DAL must not add a blanket mount rule")
		}
	}
}

func TestHostLXCOverridesUnconfinedWithoutSecurityfs(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip()
	}
	if _, err := os.Stat("/sys/kernel/security/apparmor"); err == nil {
		t.Skip("securityfs is present on this host")
	}
	if !strings.Contains(hostLXCOverrides(Spec{}), "unconfined") {
		t.Fatal(hostLXCOverrides(Spec{}))
	}
}

func TestNestingOptOutOmitsNestedEngineKeys(t *testing.T) {
	off := false
	got := hostLXCOverrides(Spec{Nesting: &off})
	if _, err := os.Stat("/sys/kernel/security/apparmor"); err != nil {
		t.Skip("securityfs missing")
	}
	if strings.Contains(got, "allow_nesting") || strings.Contains(got, "seccomp.allow_nesting") {
		t.Fatal(got)
	}
	if strings.Contains(got, BinNestingApparmor) || strings.Contains(got, "/dev/fuse") {
		t.Fatal(got)
	}
	if !strings.Contains(got, "lxc.apparmor.raw = deny mount -> /proc/") {
		t.Fatal(got)
	}
}

func TestHostnameOfSanitizes(t *testing.T) {
	if hostnameOf("Aspec Racing", "id") != "aspec-racing" {
		t.Fatal(hostnameOf("Aspec Racing", "id"))
	}
}

func TestUnprivilegedRenderKeepsIDMap(t *testing.T) {
	got := RenderConfig(Spec{
		WorkloadID: "11111111-1111-1111-1111-111111111111",
		Name:       "ct",
		RootfsPath: "/var/lib/ndl/storage/demo/rootfs",
		Privileged: false,
		UIDMap:     DefaultUIDMap,
		GIDMap:     DefaultGIDMap,
	})
	if !strings.Contains(got, "lxc.idmap = "+DefaultUIDMap) {
		t.Fatal(got)
	}
	if !strings.Contains(got, "lxc.mount.auto = proc:mixed sys:rw cgroup:mixed") {
		t.Fatal(got)
	}
	if strings.Contains(got, "unconfined") {
		t.Fatal(got)
	}
	inc := hostLXCIncludes(Spec{Privileged: false})
	if _, err := os.Stat("/usr/share/lxc/config/userns.conf"); err == nil && !strings.Contains(inc, "userns.conf") {
		t.Fatal(inc)
	}
}

func assertGeneratedNesting(t *testing.T, cfg string) {
	t.Helper()
	if !strings.Contains(cfg, "lxc.apparmor.profile = "+ApparmorGeneratedProfile) {
		t.Fatalf("want generated AppArmor profile, got %q", cfg)
	}
	if !strings.Contains(cfg, "lxc.apparmor.allow_nesting = 1") {
		t.Fatalf("want allow_nesting=1, got %q", cfg)
	}
	if !strings.Contains(cfg, "lxc.seccomp.allow_nesting = 1") {
		t.Fatalf("want seccomp.allow_nesting=1, got %q", cfg)
	}
	if !strings.Contains(cfg, "lxc.mount.entry = /dev/fuse dev/fuse none bind,optional,create=file 0 0") {
		t.Fatalf("want fuse device bind, got %q", cfg)
	}
	if !strings.Contains(cfg, "lxc.hook.start-host = "+BinNestingApparmor) {
		t.Fatalf("want nesting AppArmor hook, got %q", cfg)
	}
	if strings.Contains(cfg, "lxc-container-ndl-nesting") {
		t.Fatal("obsolete static nesting profile must not remain")
	}
	if strings.Contains(cfg, "lxc.apparmor.profile = unconfined") {
		t.Fatal("nesting must not unconfine")
	}
}
