package hostos

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/no-dal/ndl-ce/internal/hostos/debian"
)

func fixture(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestDetectDebian13Amd64(t *testing.T) {
	p, err := DetectFrom(strings.NewReader(fixture(t, "os-release.debian-13")), "x86_64")
	if err != nil {
		t.Fatal(err)
	}
	if p.SupportTier != Tier1 {
		t.Fatalf("tier=%q", p.SupportTier)
	}
	if p.Architecture != "amd64" {
		t.Fatalf("arch=%q", p.Architecture)
	}
	got := strings.Join(p.Capabilities, ",")
	want := strings.Join(debian.Capabilities(), ",")
	if got != want {
		t.Fatalf("capabilities=%q want %q", got, want)
	}
}

func TestDetectDebian13WrongArch(t *testing.T) {
	_, err := DetectFrom(strings.NewReader(fixture(t, "os-release.debian-13")), "aarch64")
	var hostErr Error
	if !errors.As(err, &hostErr) {
		t.Fatalf("err=%v", err)
	}
	if !strings.Contains(err.Error(), "does not currently support") {
		t.Fatalf("message=%q", err.Error())
	}
}

func TestDetectFedoraUnsupported(t *testing.T) {
	_, err := DetectFrom(strings.NewReader(fixture(t, "os-release.fedora-42")), "x86_64")
	if err == nil {
		t.Fatal("expected unsupported")
	}
	if !strings.Contains(err.Error(), "debian 13") {
		t.Fatalf("should list supported hosts: %v", err)
	}
}

func TestDetectUbuntuNotSupportedInPhase0(t *testing.T) {
	_, err := DetectFrom(strings.NewReader(fixture(t, "os-release.ubuntu-24.04")), "x86_64")
	if err == nil {
		t.Fatal("Ubuntu must not be a supported host in Phase 0")
	}
}

func TestSupportedListIsDebian13Amd64Only(t *testing.T) {
	list := Supported()
	if len(list) != 1 {
		t.Fatalf("supported=%v", list)
	}
	h := list[0]
	if h.ID != "debian" || h.VersionID != "13" || h.Architecture != "amd64" || h.Tier != Tier1 {
		t.Fatalf("supported=%+v", h)
	}
}
