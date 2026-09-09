package lxc

import (
	"archive/tar"
	"bytes"
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func TestTarExtractArgsNumericOwner(t *testing.T) {
	xz := tarExtractArgs("/var/lib/ndl/cache/lxc-images/debian/13.tar.xz", "/mnt/root")
	joined := strings.Join(xz, " ")
	if !strings.Contains(joined, "--numeric-owner") || !strings.Contains(joined, "-xJ") {
		t.Fatalf("%v", xz)
	}
	if strings.Contains(joined, "--ignore-failed-read") || strings.Contains(joined, "--warning=no-") {
		t.Fatal("must not ignore tar errors")
	}
}

func TestUsernsExtractArgsMapAndTar(t *testing.T) {
	args := usernsExtractArgs(DefaultUIDMap, DefaultGIDMap, "/img/root.tar.xz", ".")
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "-m u:0:100000:65536") || !strings.Contains(joined, "-m g:0:100000:65536") {
		t.Fatalf("map flags: %v", args)
	}
	if args[4] != "--" || args[5] != BinTar {
		t.Fatalf("must exec tar through usernsexec: %v", args)
	}
	if !strings.Contains(joined, "--numeric-owner") {
		t.Fatalf("%v", args)
	}
	if !strings.Contains(joined, "-f -") || strings.Contains(joined, "/img/root.tar.xz") {
		t.Fatalf("mapped tar must read stdin, not the cache path: %v", args)
	}
}

func TestUnpackTarUnprivilegedUsesUserns(t *testing.T) {
	var gotName string
	var gotArgs []string
	e := &Engine{
		Run: func(_ context.Context, name string, args ...string) ([]byte, error) {
			gotName = name
			gotArgs = append([]string{}, args...)
			return nil, nil
		},
	}
	if err := e.unpackTar(context.Background(), Spec{UIDMap: DefaultUIDMap, GIDMap: DefaultGIDMap}, "/img/a.tar.xz", "/rootfs"); err != nil {
		t.Fatal(err)
	}
	if gotName != BinUsernsExec {
		t.Fatalf("name=%s", gotName)
	}
	want := usernsExtractArgs(DefaultUIDMap, DefaultGIDMap, "/img/a.tar.xz", ".")
	if strings.Join(gotArgs, " ") != strings.Join(want, " ") {
		t.Fatalf("%v", gotArgs)
	}
}

func TestUnpackTarPrivilegedUsesHostTar(t *testing.T) {
	var gotName string
	e := &Engine{
		Run: func(_ context.Context, name string, args ...string) ([]byte, error) {
			gotName = name
			return nil, nil
		},
	}
	if err := e.unpackTar(context.Background(), Spec{Privileged: true}, "/img/a.tar.xz", "/rootfs"); err != nil {
		t.Fatal(err)
	}
	if gotName != BinTar {
		t.Fatalf("name=%s", gotName)
	}
}

func TestCleanupFailedRootfsKeepsImageCache(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "rootfs")
	cache := filepath.Join(dir, "cache", "pin", "abc.tar.xz")
	if err := os.MkdirAll(filepath.Join(root, "var", "lib", "apt"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(cache), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cache, []byte("img"), 0o640); err != nil {
		t.Fatal(err)
	}
	e := &Engine{DataDir: dir}
	e.cleanupFailedRootfs(root)
	if _, err := os.Stat(root); err == nil {
		t.Fatal("partial rootfs must be removed")
	}
	if _, err := os.Stat(cache); err != nil {
		t.Fatal("cached image must remain")
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
