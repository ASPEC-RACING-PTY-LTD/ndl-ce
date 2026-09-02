package hostos

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/no-dal/ndl-ce/internal/hostos/debian"
	"github.com/no-dal/ndl-ce/internal/hostos/ubuntu"
	"github.com/no-dal/ndl-ce/internal/ndnet"
)

type fakeAdapter struct {
	id       string
	tier     string
	ok       bool
	persist  string
	wrote    string
	writeErr error
}

func (f fakeAdapter) ID() string               { return f.id }
func (fakeAdapter) VersionID() string          { return "test" }
func (fakeAdapter) Family() string             { return "linux" }
func (f fakeAdapter) SupportTier() string      { return f.tier }
func (f fakeAdapter) Qualified() bool          { return f.ok }
func (fakeAdapter) PackageTool() string        { return "none" }
func (f fakeAdapter) NetworkPersist() string   { return f.persist }
func (fakeAdapter) FirewallKind() string       { return "nftables" }
func (fakeAdapter) KernelModules() string      { return "shared" }
func (fakeAdapter) GPUDrivers() string         { return "optional" }
func (fakeAdapter) BootloaderRollback() string { return "checkpoint-tar" }
func (fakeAdapter) Gaps() []string             { return nil }
func (fakeAdapter) NetworkFiles(ndnet.Plan) []ndnet.File {
	return []ndnet.File{{RelPath: "50-ndl-test.netdev", Body: "[NetDev]\n"}}
}
func (f *fakeAdapter) WriteNetwork(_ Platform, destDir string, _ []ndnet.File) error {
	f.wrote = destDir
	return f.writeErr
}

func TestLookupDebianTier1(t *testing.T) {
	p := Platform{ID: "debian", VersionID: "13", Architecture: "amd64"}
	a := Lookup(p)
	if a.ID() != debian.ID || !a.Qualified() || a.SupportTier() != Tier1 {
		t.Fatalf("%+v qualified=%v tier=%s", a, a.Qualified(), a.SupportTier())
	}
	if a.NetworkPersist() != debian.NetworkPersist {
		t.Fatal(a.NetworkPersist())
	}
}

func TestLookupUbuntuUnqualified(t *testing.T) {
	p := Platform{ID: "ubuntu", VersionID: "24.04", Architecture: "amd64"}
	a := Lookup(p)
	if a.Qualified() || a.SupportTier() != Unsupported {
		t.Fatal("ubuntu must not be claimed as Tier 1")
	}
	if len(a.Gaps()) == 0 {
		t.Fatal("qualification gaps required")
	}
}

func TestUbuntuAdapterRefusesDebianNetworkd(t *testing.T) {
	a := UbuntuAdapter{Version: "24.04", Arch: "amd64"}
	err := a.WriteNetwork(Platform{ID: "debian", VersionID: "13"}, debian.NetworkDir, nil)
	if err == nil || !strings.Contains(err.Error(), "must not rewrite") {
		t.Fatalf("err=%v", err)
	}
	if err := ubuntu.RefuseDebianNetworkd("debian", debian.NetworkDir); err == nil {
		t.Fatal("expected refuse")
	}
}

func TestUnsupportedAdapterRefusesEnrollShape(t *testing.T) {
	a := Lookup(Platform{ID: "fedora", VersionID: "42", Architecture: "amd64"})
	if a.Qualified() {
		t.Fatal("fedora must not enroll")
	}
	if err := a.WriteNetwork(Platform{ID: "fedora"}, "/tmp", nil); err == nil {
		t.Fatal("expected error")
	}
}

func TestFakeAdapterDoesNotCallAptByName(t *testing.T) {
	f := &fakeAdapter{id: "fake", tier: Tier1, ok: true, persist: "none"}
	var a Adapter = f
	if strings.Contains(a.PackageTool(), "apt") {
		t.Fatal("fake adapter must not hardcode apt")
	}
	dest := t.TempDir()
	if err := f.WriteNetwork(Platform{ID: "fake"}, dest, nil); err != nil {
		t.Fatal(err)
	}
	if f.wrote != dest {
		t.Fatalf("wrote=%s", f.wrote)
	}
}

func TestDebianAdapterRefusesForeignHost(t *testing.T) {
	err := DebianAdapter{}.WriteNetwork(Platform{ID: "ubuntu", VersionID: "24.04"}, debian.NetworkDir, []ndnet.File{
		{RelPath: "50-ndl-test.netdev", Body: "[NetDev]\n"},
	})
	if err == nil {
		t.Fatal("debian adapter must not write on ubuntu")
	}
}

func TestDetectUbuntuStillUnsupported(t *testing.T) {
	_, err := DetectFrom(strings.NewReader(fixture(t, "os-release.ubuntu-24.04")), "x86_64")
	if err == nil {
		t.Fatal("Ubuntu must not enroll until qualification passes")
	}
	var hostErr Error
	if !errors.As(err, &hostErr) {
		t.Fatal(err)
	}
}

func TestISOWrapsNodalMetapackage(t *testing.T) {
	root, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	// testdata lives in this package; ISO files are at repo packaging/iso.
	b, err := os.ReadFile(filepath.Join(root, "..", "..", "packaging", "iso", "mkosi.conf"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(b)
	if !strings.Contains(text, "Distribution=debian") || !strings.Contains(text, "Release=trixie") {
		t.Fatal("ISO must be Debian 13")
	}
	if !strings.Contains(text, "nodal") {
		t.Fatal("ISO must wrap the nodal metapackage")
	}
	if strings.Contains(text, "Distribution=ubuntu") {
		t.Fatal("Debian ISO must not claim Ubuntu")
	}
}
