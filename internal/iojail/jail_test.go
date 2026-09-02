package iojail

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCleanRelRejectsDotDot(t *testing.T) {
	if _, err := cleanRel("../etc/passwd"); err == nil {
		t.Fatal("expected escape")
	}
	if _, err := cleanRel("foo/../../etc"); err == nil {
		t.Fatal("expected escape")
	}
	got, err := cleanRel("/var/log")
	if err != nil || got != "var/log" {
		t.Fatalf("%s %v", got, err)
	}
}

func TestOpenBeneathStaysInRoot(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "ok.txt"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	f, abs, err := OpenBeneath(root, "ok.txt", os.O_RDONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	if filepath.Base(abs) != "ok.txt" {
		t.Fatal(abs)
	}
	if _, _, err := OpenBeneath(root, "../ok.txt", os.O_RDONLY, 0); err == nil {
		t.Fatal("dot-dot must fail")
	}
}

func TestOpenBeneathRejectsSymlinkEscape(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	secret := filepath.Join(outside, "secret")
	if err := os.WriteFile(secret, []byte("no"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(secret, filepath.Join(root, "link")); err != nil {
		t.Skip(err)
	}
	if _, _, err := OpenBeneath(root, "link", os.O_RDONLY, 0); err == nil {
		t.Fatal("symlink escape must fail")
	}
}

func TestMkdirBeneathIdempotent(t *testing.T) {
	root := t.TempDir()
	if err := MkdirBeneath(root, "once", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := MkdirBeneath(root, "once", 0o755); err != nil {
		t.Fatal(err)
	}
}

func TestRemoveBeneathDoesNotFollowSymlink(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	secret := filepath.Join(outside, "secret")
	if err := os.WriteFile(secret, []byte("no"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(secret, filepath.Join(root, "sub", "link")); err != nil {
		t.Skip(err)
	}
	if err := RemoveBeneath(root, "sub"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(secret); err != nil {
		t.Fatalf("outside file must survive: %v", err)
	}
	if err := RemoveBeneath(root, "."); err == nil {
		t.Fatal("jail root must be refused")
	}
}

func TestHostDeny(t *testing.T) {
	if err := deniedHost("/", "/var/lib/ndl/host.key"); err == nil {
		t.Fatal("host key must be denied")
	}
	if err := deniedHost("/", "/tmp/safe"); err != nil {
		t.Fatal(err)
	}
}
