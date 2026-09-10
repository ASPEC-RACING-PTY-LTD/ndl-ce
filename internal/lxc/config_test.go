package lxc

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestHostLXCOverridesUsesNestingProfileWhenApparmor(t *testing.T) {
	got := hostLXCOverrides()
	if _, err := os.Stat("/sys/kernel/security/apparmor"); err != nil {
		if !strings.Contains(got, "unconfined") {
			t.Fatalf("want unconfined without securityfs, got %q", got)
		}
		return
	}
	if !strings.Contains(got, ApparmorNestingProfile) {
		t.Fatalf("want dedicated nesting profile, got %q", got)
	}
	if strings.Contains(got, "unconfined") {
		t.Fatal("must not unconfine every container when AppArmor is usable")
	}
}

func TestNestingProfileAllowsDockerBindMounts(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", "packaging", "apparmor", "lxc", "lxc-ndl-nesting"))
	if err != nil {
		t.Fatal(err)
	}
	body := string(b)
	if !strings.Contains(body, "profile "+ApparmorNestingProfile) {
		t.Fatal(body)
	}
	if !strings.Contains(body, "mount options=(rw,rbind)") {
		t.Fatal("profile must allow rbind for containerd/BuildKit")
	}
	if !strings.Contains(body, "mount fstype=overlay") {
		t.Fatal("profile must allow overlay")
	}
	if !strings.Contains(body, "-> /tmp/**") || !strings.Contains(body, "-> /var/lib/docker/**") {
		t.Fatal("profile must name Docker mount targets")
	}
	parser, err := exec.LookPath("apparmor_parser")
	if err != nil {
		return
	}
	if _, err := os.Stat("/etc/apparmor.d/abstractions/lxc/container-base"); err != nil {
		return
	}
	wrapped := filepath.Join(t.TempDir(), "lxc-ndl-nesting")
	if err := os.WriteFile(wrapped, append([]byte("#include <tunables/global>\n"), b...), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(parser, "-Q", "-I", "/etc/apparmor.d", wrapped)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("apparmor_parser: %v\n%s", err, out)
	}
}

func TestHostnameOfSanitizes(t *testing.T) {
	if hostnameOf("Aspec Racing", "id") != "aspec-racing" {
		t.Fatal(hostnameOf("Aspec Racing", "id"))
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
