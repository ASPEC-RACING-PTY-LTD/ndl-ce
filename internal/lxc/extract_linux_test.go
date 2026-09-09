//go:build linux

package lxc

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
)

func TestUnpackTarUnprivilegedMappedOwnershipFromRestrictedCache(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("requires host root to own a 0750 cache and map 0 -> 100000")
	}
	if _, err := os.Stat(BinUsernsExec); err != nil {
		t.Skip("lxc-usernsexec is not installed")
	}
	if _, err := os.Stat(BinTar); err != nil {
		t.Skip("tar is not installed")
	}

	dir := t.TempDir()
	cache := filepath.Join(dir, "var", "lib", "ndl", "cache", "lxc-images", "debian", "trixie", "amd64", "default")
	if err := os.MkdirAll(cache, 0o750); err != nil {
		t.Fatal(err)
	}
	archive := filepath.Join(cache, "a4b36e63a2d16d508e7191e8ed20bed908e71e928bb92b30ddd46861c7c6edf9.tar.gz")
	writeMappedRootfsTarGz(t, archive)
	restrictAsRoot(t, filepath.Join(dir, "var"), archive)

	rootfs := filepath.Join(dir, "var", "lib", "ndl", "storage", "rootfs")
	if err := os.MkdirAll(rootfs, 0o750); err != nil {
		t.Fatal(err)
	}

	e := &Engine{}
	if err := e.unpackTar(context.Background(), Spec{UIDMap: DefaultUIDMap, GIDMap: DefaultGIDMap}, archive, rootfs); err != nil {
		t.Fatal(err)
	}

	assertOwner(t, filepath.Join(rootfs, "etc", "hostname"), 100000, 100000)
	assertOwner(t, filepath.Join(rootfs, "var", "lib", "apt", "lists", "auxfiles"), 100100, 165534)
	st, err := os.Stat(archive)
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != 0o640 {
		t.Fatalf("cache must stay 0640, got %o", st.Mode().Perm())
	}
	if sys, ok := st.Sys().(*syscall.Stat_t); !ok || sys.Uid != 0 || sys.Gid != 0 {
		t.Fatalf("cache must stay root-owned: %+v", st.Sys())
	}
}

func writeMappedRootfsTarGz(t *testing.T, dest string) {
	t.Helper()
	f, err := os.OpenFile(dest, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o640)
	if err != nil {
		t.Fatal(err)
	}
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	addTarDir(t, tw, "./etc", 0o755, 0, 0)
	addTarFile(t, tw, "./etc/hostname", []byte("debian\n"), 0o644, 0, 0)
	addTarDir(t, tw, "./var", 0o755, 0, 0)
	addTarDir(t, tw, "./var/lib", 0o755, 0, 0)
	addTarDir(t, tw, "./var/lib/apt", 0o755, 0, 0)
	addTarDir(t, tw, "./var/lib/apt/lists", 0o755, 0, 0)
	addTarDir(t, tw, "./var/lib/apt/lists/auxfiles", 0o755, 100, 65534)
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}

func addTarDir(t *testing.T, tw *tar.Writer, name string, mode int64, uid, gid int) {
	t.Helper()
	if err := tw.WriteHeader(&tar.Header{
		Name:     name,
		Typeflag: tar.TypeDir,
		Mode:     mode,
		Uid:      uid,
		Gid:      gid,
	}); err != nil {
		t.Fatal(err)
	}
}

func addTarFile(t *testing.T, tw *tar.Writer, name string, body []byte, mode int64, uid, gid int) {
	t.Helper()
	if err := tw.WriteHeader(&tar.Header{
		Name:     name,
		Typeflag: tar.TypeReg,
		Mode:     mode,
		Size:     int64(len(body)),
		Uid:      uid,
		Gid:      gid,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(body); err != nil {
		t.Fatal(err)
	}
}

func restrictAsRoot(t *testing.T, tree, archive string) {
	t.Helper()
	if err := filepath.Walk(tree, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if err := os.Chown(p, 0, 0); err != nil {
			return err
		}
		if info.IsDir() {
			return os.Chmod(p, 0o750)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := os.Chown(archive, 0, 0); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(archive, 0o640); err != nil {
		t.Fatal(err)
	}
}

func assertOwner(t *testing.T, path string, uid, gid int) {
	t.Helper()
	st, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	sys, ok := st.Sys().(*syscall.Stat_t)
	if !ok {
		t.Fatal("stat")
	}
	if int(sys.Uid) != uid || int(sys.Gid) != gid {
		t.Fatalf("%s uid/gid %d:%d want %d:%d", path, sys.Uid, sys.Gid, uid, gid)
	}
}

func TestUnpackTarMappedLiveDoesNotNeedCachePathname(t *testing.T) {
	if testing.Short() {
		t.Skip("live userns extract")
	}
	if _, err := exec.LookPath(BinUsernsExec); err != nil {
		t.Skip("lxc-usernsexec not installed")
	}
	if _, err := exec.LookPath("xz"); err != nil {
		t.Skip("xz not installed")
	}
	dir := t.TempDir()
	cacheDir := filepath.Join(dir, "cache")
	rootfs := filepath.Join(dir, "rootfs")
	if err := os.MkdirAll(cacheDir, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(rootfs, 0o750); err != nil {
		t.Fatal(err)
	}
	archive := filepath.Join(cacheDir, "root.tar.xz")
	if err := writeTinyRootTarXZ(archive); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(archive, 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(cacheDir, 0o750); err != nil {
		t.Fatal(err)
	}
	e := &Engine{}
	if err := e.unpackTar(context.Background(), Spec{UIDMap: DefaultUIDMap, GIDMap: DefaultGIDMap}, archive, rootfs); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(archive)
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != 0o640 {
		t.Fatalf("cache mode must stay 0640, got %o", st.Mode().Perm())
	}
	host, err := os.ReadFile(filepath.Join(rootfs, "etc", "hostname"))
	if err != nil {
		t.Fatal(err)
	}
	if string(host) != "guest\n" {
		t.Fatalf("extracted %q", host)
	}
	info, err := os.Stat(filepath.Join(rootfs, "etc", "hostname"))
	if err != nil {
		t.Fatal(err)
	}
	if statUID(info) != 100000 {
		t.Fatalf("mapped uid %d", statUID(info))
	}
}

func writeTinyRootTarXZ(dest string) error {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	hdr := &tar.Header{Name: "etc/hostname", Mode: 0o644, Size: 6, Uid: 0, Gid: 0}
	if err := tw.WriteHeader(hdr); err != nil {
		return err
	}
	if _, err := tw.Write([]byte("guest\n")); err != nil {
		return err
	}
	if err := tw.Close(); err != nil {
		return err
	}
	cmd := exec.Command("xz", "-c")
	cmd.Stdin = bytes.NewReader(buf.Bytes())
	out, err := os.OpenFile(dest, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o640)
	if err != nil {
		return err
	}
	cmd.Stdout = out
	cmd.Stderr = io.Discard
	if err := cmd.Run(); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}

func statUID(info os.FileInfo) int {
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return -1
	}
	return int(st.Uid)
}

func TestUnpackTarUnprivilegedErrorsRemainFatal(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("requires host root")
	}
	if _, err := exec.LookPath(BinUsernsExec); err != nil {
		t.Skip("lxc-usernsexec is not installed")
	}
	dir := t.TempDir()
	cache := filepath.Join(dir, "cache")
	if err := os.MkdirAll(cache, 0o750); err != nil {
		t.Fatal(err)
	}
	archive := filepath.Join(cache, "bad.tar.xz")
	if err := os.WriteFile(archive, []byte("not-an-archive"), 0o640); err != nil {
		t.Fatal(err)
	}
	rootfs := filepath.Join(dir, "rootfs")
	if err := os.MkdirAll(rootfs, 0o750); err != nil {
		t.Fatal(err)
	}
	e := &Engine{}
	err := e.unpackTar(context.Background(), Spec{UIDMap: DefaultUIDMap, GIDMap: DefaultGIDMap}, archive, rootfs)
	if err == nil {
		t.Fatal("corrupt archive must fail")
	}
	if _, statErr := os.Stat(archive); statErr != nil {
		t.Fatal("failed extract must keep the cache")
	}
}
