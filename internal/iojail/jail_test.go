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

func TestCleanRelRejectsControlCharacters(t *testing.T) {
	if _, err := cleanRel("foo\nbar"); err == nil {
		t.Fatal("newline must fail")
	}
	if _, err := cleanRel("foo\x1bbar"); err == nil {
		t.Fatal("escape must fail")
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
	if err := deniedHost("/", "/var/lib/postgresql/16/main"); err == nil {
		t.Fatal("postgres data must be denied")
	}
	if err := deniedHost("/", "/var/lib/ndl/secrets/wireguard/peer.key"); err == nil {
		t.Fatal("ndl secrets must be denied")
	}
	if err := deniedHost("/", "/var/lib/ndl/certs/current.key"); err == nil {
		t.Fatal("ndl certs must be denied")
	}
	if err := deniedHost("/", "/tmp/safe"); err != nil {
		t.Fatal(err)
	}
}

func TestRenameBeneathDeniesHostSource(t *testing.T) {
	if err := RenameBeneath("/", "var/lib/ndl/host.key", "tmp/ndl-out"); err == nil {
		t.Fatal("rename of host.key must be denied")
	}
	if err := RenameBeneath("/", "etc/ndl/x", "tmp/ndl-out"); err == nil {
		t.Fatal("rename out of /etc/ndl must be denied")
	}
}

func TestRenameBeneathDoesNotWipeDestinationDirectory(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "keep", "child"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "keep", "child", "x"), []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("a"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := RenameBeneath(root, "a.txt", "keep"); err == nil {
		t.Fatal("rename onto a directory must fail")
	}
	got, err := os.ReadFile(filepath.Join(root, "keep", "child", "x"))
	if err != nil || string(got) != "keep" {
		t.Fatalf("destination directory must survive: %s %v", got, err)
	}
}

func TestRemoveBeneathRefusesParentOfDeniedPrefix(t *testing.T) {
	root := t.TempDir()
	secretDir := filepath.Join(root, "var", "lib", "ndl", "secrets")
	if err := os.MkdirAll(secretDir, 0o700); err != nil {
		t.Fatal(err)
	}
	secret := filepath.Join(secretDir, "x")
	if err := os.WriteFile(secret, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	safe := filepath.Join(root, "var", "lib", "ndl", "ok.txt")
	if err := os.WriteFile(safe, []byte("ok"), 0o644); err != nil {
		t.Fatal(err)
	}
	prev := HostDenyPrefixes
	HostDenyPrefixes = []string{secretDir}
	t.Cleanup(func() { HostDenyPrefixes = prev })

	if err := RemoveBeneath(root, "var/lib/ndl"); err == nil {
		t.Fatal("parent delete of a denied prefix must fail")
	}
	if _, err := os.Stat(secret); err != nil {
		t.Fatalf("denied child must survive parent delete: %v", err)
	}
	if err := RemoveBeneath(root, "var/lib/ndl/secrets/x"); err == nil {
		t.Fatal("direct denied delete must fail")
	}
	if _, err := os.Stat(secret); err != nil {
		t.Fatalf("denied file must survive direct delete: %v", err)
	}
	if err := RemoveBeneath(root, "var/lib/ndl/ok.txt"); err != nil {
		t.Fatalf("sibling of a denied prefix must still delete: %v", err)
	}
}

func TestRenameBeneathRefusesParentOfDeniedPrefix(t *testing.T) {
	root := t.TempDir()
	secretDir := filepath.Join(root, "var", "lib", "ndl", "secrets")
	if err := os.MkdirAll(secretDir, 0o700); err != nil {
		t.Fatal(err)
	}
	secret := filepath.Join(secretDir, "x")
	if err := os.WriteFile(secret, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "tmp"), 0o755); err != nil {
		t.Fatal(err)
	}
	prev := HostDenyPrefixes
	HostDenyPrefixes = []string{secretDir}
	t.Cleanup(func() { HostDenyPrefixes = prev })

	if err := RenameBeneath(root, "var/lib/ndl", "tmp/exfil"); err == nil {
		t.Fatal("parent rename of a denied prefix must fail")
	}
	if _, err := os.Stat(secret); err != nil {
		t.Fatalf("denied child must not move: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "tmp", "exfil")); !os.IsNotExist(err) {
		t.Fatal("exfil dest must not be created")
	}
}

func TestOpenBeneathDeniesInJailSymlinkToHostPrefix(t *testing.T) {
	root := t.TempDir()
	secretDir := filepath.Join(root, "denied")
	if err := os.Mkdir(secretDir, 0o700); err != nil {
		t.Fatal(err)
	}
	secret := filepath.Join(secretDir, "host.key")
	if err := os.WriteFile(secret, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(secret, filepath.Join(root, "link")); err != nil {
		t.Skip(err)
	}
	prev := HostDenyPrefixes
	HostDenyPrefixes = []string{secretDir}
	t.Cleanup(func() { HostDenyPrefixes = prev })
	if _, _, err := OpenBeneath(root, "link", os.O_RDONLY, 0); err == nil {
		t.Fatal("symlink into a denied prefix must fail")
	}
}
