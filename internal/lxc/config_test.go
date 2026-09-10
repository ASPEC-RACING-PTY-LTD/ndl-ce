package lxc

import (
	"os"
	"runtime"
	"strings"
	"testing"
)

func TestHostLXCOverridesUsesGeneratedNestingWhenApparmor(t *testing.T) {
	got := hostLXCOverrides()
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
	if !strings.Contains(hostLXCOverrides(), "unconfined") {
		t.Fatal(hostLXCOverrides())
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
	if strings.Contains(cfg, "lxc-container-ndl-nesting") {
		t.Fatal("obsolete static nesting profile must not remain")
	}
}
