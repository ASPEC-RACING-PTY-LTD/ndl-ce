package ctbackup

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestArchiveExtractRoundTrip(t *testing.T) {
	if _, err := os.Stat(BinTar); err != nil {
		t.Skip("tar is not installed")
	}
	root := t.TempDir()
	src := filepath.Join(root, "rootfs")
	if err := os.MkdirAll(filepath.Join(src, "etc"), 0o755); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(src, "etc", "ndl-marker")
	if err := os.WriteFile(marker, []byte("hello-ct"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("ndl-marker", filepath.Join(src, "etc", "ndl-link")); err != nil {
		t.Fatal(err)
	}
	sparse := filepath.Join(src, "sparse.dat")
	sf, err := os.Create(sparse)
	if err != nil {
		t.Fatal(err)
	}
	if err := sf.Truncate(1 << 20); err != nil {
		t.Fatal(err)
	}
	if _, err := sf.WriteAt([]byte("HEAD"), 0); err != nil {
		t.Fatal(err)
	}
	if err := sf.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(src, "proc"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "proc", "should-not-copy"), []byte("nope"), 0o644); err != nil {
		t.Fatal(err)
	}

	dest := filepath.Join(root, "out", "a.tar.zst")
	meta := []byte(`{"kind":"system-container","name":"ndl-backup-ct-test"}`)
	res, err := Archive(context.Background(), src, dest, "", meta)
	if err != nil {
		t.Fatal(err)
	}
	if res.Size < 1 || res.SHA256 == "" {
		t.Fatalf("checksummed archive %+v", res)
	}
	if res.Format != FormatZstd && res.Format != FormatGzip {
		t.Fatalf("format %s", res.Format)
	}

	gotMeta, err := ReadMeta(context.Background(), res.Dest)
	if err != nil {
		t.Fatal(err)
	}
	if string(gotMeta) != string(meta) {
		t.Fatalf("meta %s", gotMeta)
	}

	out := filepath.Join(root, "restore")
	if err := os.MkdirAll(out, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(out, "stale"), []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Extract(context.Background(), res.Dest, out); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(filepath.Join(out, "etc", "ndl-marker"))
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "hello-ct" {
		t.Fatalf("marker %s", body)
	}
	link, err := os.Readlink(filepath.Join(out, "etc", "ndl-link"))
	if err != nil || link != "ndl-marker" {
		t.Fatalf("symlink %s %v", link, err)
	}
	st, err := os.Stat(filepath.Join(out, "sparse.dat"))
	if err != nil {
		t.Fatal(err)
	}
	if st.Size() != 1<<20 {
		t.Fatalf("sparse size %d", st.Size())
	}
	if _, err := os.Stat(filepath.Join(out, "proc", "should-not-copy")); err == nil {
		t.Fatal("virtual proc must not be archived")
	}
	if _, err := os.Stat(filepath.Join(out, "stale")); err == nil {
		t.Fatal("replace must clear stale files")
	}
	if _, err := os.Stat(filepath.Join(out, MetaName)); err == nil {
		t.Fatal("metadata must not land in the guest rootfs")
	}
	mode := st.Mode().Perm()
	_ = mode
}

func TestArchivePreservesModeAndOptionalXattr(t *testing.T) {
	if _, err := os.Stat(BinTar); err != nil {
		t.Skip("tar is not installed")
	}
	src := filepath.Join(t.TempDir(), "rootfs")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(src, "acl-file")
	if err := os.WriteFile(path, []byte("x"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o640); err != nil {
		t.Fatal(err)
	}
	xattrOK := unixSetUserXattr(path, "user.ndl", []byte("cap-meta")) == nil
	dest := filepath.Join(t.TempDir(), "a.tar.gz")
	res, err := Archive(context.Background(), src, dest, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "root")
	if err := Extract(context.Background(), res.Dest, out); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(filepath.Join(out, "acl-file"))
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != 0o640 {
		t.Fatalf("mode %o", st.Mode().Perm())
	}
	if xattrOK {
		got, err := unixGetUserXattr(filepath.Join(out, "acl-file"), "user.ndl")
		if err != nil || string(got) != "cap-meta" {
			t.Fatalf("xattr %s %v", got, err)
		}
	}
}

func TestFreezeMissingFileIsSkipped(t *testing.T) {
	prev := cgroupSlice
	t.Cleanup(func() { cgroupSlice = prev })
	cgroupSlice = filepath.Join(t.TempDir(), "system.slice")
	unit := "nodal-ct@11111111-1111-4111-8111-111111111111.service"
	unfreeze, err := freezeUnitIfPresent(unit, false)
	if err != nil {
		t.Fatal(err)
	}
	unfreeze()
}

func TestFreezeWritesAndClears(t *testing.T) {
	prev := cgroupSlice
	t.Cleanup(func() { cgroupSlice = prev })
	cgroupSlice = filepath.Join(t.TempDir(), "system.slice")
	unit := "nodal-ct@11111111-1111-4111-8111-111111111111.service"
	dir := filepath.Join(cgroupSlice, unit)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "cgroup.freeze")
	if err := os.WriteFile(path, []byte("0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	unfreeze, err := freezeUnitIfPresent(unit, true)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := os.ReadFile(path)
	if strings.TrimSpace(string(body)) != "1" {
		t.Fatalf("frozen %s", body)
	}
	unfreeze()
	body, _ = os.ReadFile(path)
	if strings.TrimSpace(string(body)) != "0" {
		t.Fatalf("unfrozen %s", body)
	}
}

func TestValidateFreezeUnitRejectsTraversal(t *testing.T) {
	if err := ValidateFreezeUnit("../etc.service"); err == nil {
		t.Fatal("expected reject")
	}
	if err := ValidateFreezeUnit("nodal-ct@not-a-uuid.service"); err == nil {
		t.Fatal("expected reject")
	}
}

func TestArchiveRefusesEtc(t *testing.T) {
	_, err := Archive(context.Background(), "/etc", filepath.Join(t.TempDir(), "a.tar.gz"), "", nil)
	if err == nil {
		t.Fatal("must not archive /etc")
	}
}

func TestParseFreezeUnit(t *testing.T) {
	unit, err := ParseFreezeUnit("archive:nodal-ct@11111111-1111-4111-8111-111111111111.service")
	if err != nil || !strings.HasPrefix(unit, "nodal-ct@") {
		t.Fatalf("%s %v", unit, err)
	}
	if _, err := ParseFreezeUnit("copy"); err == nil {
		t.Fatal("copy is not an archive action")
	}
}

func TestArchiveUnfreezesWhenTarFails(t *testing.T) {
	prev := cgroupSlice
	prevTar := runTarCmd
	t.Cleanup(func() {
		cgroupSlice = prev
		runTarCmd = prevTar
	})
	cgroupSlice = filepath.Join(t.TempDir(), "system.slice")
	unit := "nodal-ct@11111111-1111-4111-8111-111111111111.service"
	dir := filepath.Join(cgroupSlice, unit)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "cgroup.freeze")
	if err := os.WriteFile(path, []byte("0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runTarCmd = func(context.Context, ...string) error {
		body, _ := os.ReadFile(path)
		if strings.TrimSpace(string(body)) != "1" {
			t.Fatalf("must freeze before tar: %s", body)
		}
		return os.ErrPermission
	}
	src := filepath.Join(t.TempDir(), "rootfs")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	_, err := Archive(context.Background(), src, filepath.Join(t.TempDir(), "a.tar.gz"), unit, nil)
	if err == nil {
		t.Fatal("tar failure must surface")
	}
	body, _ := os.ReadFile(path)
	if strings.TrimSpace(string(body)) != "0" {
		t.Fatalf("must unfreeze after failure: %s", body)
	}
}

func unixSetUserXattr(path, name string, val []byte) error {
	return unixSetxattr(path, name, val)
}

func unixGetUserXattr(path, name string) ([]byte, error) {
	return unixGetxattr(path, name)
}
