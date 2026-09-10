package lxc

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGuestBaselineFiles(t *testing.T) {
	root := t.TempDir()
	spec := Spec{Name: "baseline-ct", UIDMap: DefaultUIDMap, GIDMap: DefaultGIDMap}
	if err := provisionGuest(root, spec); err != nil {
		t.Fatal(err)
	}
	mustContain := []string{
		"etc/timezone",
		"etc/localtime",
		"etc/environment",
		"etc/profile.d/00-ndl-term.sh",
		"etc/profile.d/00-ndl-details.sh",
		"etc/systemd/system/container-getty@.service.d/ndl-autologin.conf",
		"etc/systemd/system/console-getty.service.d/ndl-autologin.conf",
		"etc/systemd/system/systemd-networkd-wait-online.service",
		"etc/systemd/system/systemd-homed.service",
		"etc/systemd/system/systemd-homed-firstboot.service",
	}
	for _, rel := range mustContain {
		if _, err := os.Lstat(filepath.Join(root, rel)); err != nil {
			t.Fatalf("%s: %v", rel, err)
		}
	}
	env, err := os.ReadFile(filepath.Join(root, "etc", "environment"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(env), "LC_ALL=C.UTF-8") {
		t.Fatal(string(env))
	}
	wait, err := os.Readlink(filepath.Join(root, "etc", "systemd", "system", "systemd-networkd-wait-online.service"))
	if err != nil || wait != "/dev/null" {
		t.Fatalf("wait-online mask %s %v", wait, err)
	}
	getty, err := os.ReadFile(filepath.Join(root, "etc", "systemd", "system", "container-getty@.service.d", "ndl-autologin.conf"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(getty), "--autologin root") || !strings.Contains(string(getty), "TERM=linux") {
		t.Fatal(string(getty))
	}
	if _, err := os.Lstat(filepath.Join(root, "etc", "ssh", "sshd_config.d", "ndl-root.conf")); err == nil {
		t.Fatal("root SSH must not be enabled by default")
	}
}

func TestSanitizeRootfsFixesEtcTraverse(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "etc"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(root, 0o750); err != nil {
		t.Fatal(err)
	}
	spec := Spec{Privileged: true}
	if err := sanitizeRootfs(root, spec); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(root)
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm()&0o111 != 0o111 || st.Mode().Perm()&0o004 != 0o004 {
		t.Fatalf("root mode %s", st.Mode())
	}
	etc, err := os.Stat(filepath.Join(root, "etc"))
	if err != nil {
		t.Fatal(err)
	}
	if etc.Mode().Perm()&0o111 != 0o111 {
		t.Fatalf("etc mode %s", etc.Mode())
	}
}

func TestPythonCompatIsOptIn(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "usr", "lib", "python3.13")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(dir, "EXTERNALLY-MANAGED")
	if err := os.WriteFile(marker, []byte("pep668\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := provisionGuest(root, Spec{Name: "py"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatal("default provision must keep EXTERNALLY-MANAGED")
	}
	if err := provisionGuest(root, Spec{Name: "py", PythonSystemPIP: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("python_system_pip must remove EXTERNALLY-MANAGED")
	}
}

func TestDebianBasePackagesDistinguished(t *testing.T) {
	got := debianBasePackages()
	for _, p := range helperScriptDebianPackages {
		found := false
		for _, g := range got {
			if g == p {
				found = true
			}
		}
		if !found {
			t.Fatalf("missing helper package %s", p)
		}
	}
	for _, p := range []string{"git", "docker.io", "docker-ce"} {
		for _, g := range got {
			if g == p {
				t.Fatalf("must not install %s by default", p)
			}
		}
	}
}

func TestMaybeMarkFirstBootstrap(t *testing.T) {
	root := t.TempDir()
	if err := maybeMarkFirstBootstrap(root); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, guestFirstBootstrapRel)); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, guestNDLDir), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, guestBaselineRel), []byte("schema=1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_ = os.Remove(filepath.Join(root, guestFirstBootstrapRel))
	if err := maybeMarkFirstBootstrap(root); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, guestFirstBootstrapRel)); err == nil {
		t.Fatal("existing baseline must not be marked for first bootstrap")
	}
}

func TestRenderConfigTUNAndMknodOptional(t *testing.T) {
	base := Spec{
		WorkloadID: "11111111-1111-1111-1111-111111111111",
		Name:       "ct",
		RootfsPath: "/var/lib/ndl/storage/demo/rootfs",
		BridgeName: "br0",
	}
	got := RenderConfig(base)
	if strings.Contains(got, "/dev/net/tun") || strings.Contains(got, "10:200") {
		t.Fatal("TUN must be off by default")
	}
	if strings.Contains(got, "mknod") {
		t.Fatal("mknod must be off by default")
	}
	base.TUN = true
	got = RenderConfig(base)
	if !strings.Contains(got, "lxc.cgroup2.devices.allow = c 10:200 rwm") {
		t.Fatal(got)
	}
	if !strings.Contains(got, "lxc.mount.entry = /dev/net/tun") {
		t.Fatal(got)
	}
}
