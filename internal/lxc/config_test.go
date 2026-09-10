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
	body := readNestingProfile(t)
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
	compileNestingProfile(t, body)
}

func TestNestingProfileAllowsBuildKitReadOnlyRbind(t *testing.T) {
	body := readNestingProfile(t)
	if strings.Contains(body, "unconfined") {
		t.Fatal("nesting profile must not unconfine the container")
	}
	for _, line := range strings.Split(body, "\n") {
		trim := strings.TrimSpace(line)
		if trim == "mount," || trim == "mount, " {
			t.Fatal("nesting profile must not allow every mount")
		}
	}
	if !strings.Contains(body, "mount options=(ro,rbind)") {
		t.Fatal("BuildKit snapshot binds use Options:[rbind ro]; exact rw,rbind is not enough")
	}
	idx := strings.Index(body, "mount options in (")
	if idx < 0 {
		t.Fatal("profile must use options in (...) so rbind+ro subsets match")
	}
	end := strings.Index(body[idx:], ")")
	if end < 0 {
		t.Fatal(body)
	}
	set := body[idx : idx+end]
	if !strings.Contains(set, "ro") || !strings.Contains(set, "rbind") {
		t.Fatalf("options in set must include ro and rbind: %s", set)
	}
	if !strings.Contains(body, "/var/lib/containerd/** -> /var/lib/docker/**") {
		t.Fatal("profile must allow containerd overlay snapshots onto Docker BuildKit mounts")
	}
	if !strings.Contains(body, "-> /var/lib/docker/**") || !strings.Contains(body, "-> /var/lib/containerd/**") {
		t.Fatal("profile must name Docker and containerd mount targets")
	}
	compileNestingProfile(t, body)
}

func readNestingProfile(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "packaging", "apparmor", "lxc", "lxc-ndl-nesting"))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func compileNestingProfile(t *testing.T, body string) {
	t.Helper()
	parser, err := exec.LookPath("apparmor_parser")
	if err != nil {
		return
	}
	if _, err := os.Stat("/etc/apparmor.d/abstractions/lxc/container-base"); err != nil {
		return
	}
	wrapped := filepath.Join(t.TempDir(), "lxc-ndl-nesting")
	if err := os.WriteFile(wrapped, append([]byte("#include <tunables/global>\n"), []byte(body)...), 0o644); err != nil {
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
