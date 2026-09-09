package lxc

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
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

func TestUsernsExtractArgsReadStdinNotCachePath(t *testing.T) {
	archive := "/var/lib/ndl/cache/lxc-images/debian/trixie/amd64/default/deadbeef.tar.xz"
	args := usernsExtractArgs(DefaultUIDMap, DefaultGIDMap, archive, ".")
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "-m u:0:100000:65536") || !strings.Contains(joined, "-m g:0:100000:65536") {
		t.Fatalf("map flags: %v", args)
	}
	if args[4] != "--" || args[5] != BinTar {
		t.Fatalf("must exec tar through usernsexec: %v", args)
	}
	if !strings.Contains(joined, "--numeric-owner") || !strings.Contains(joined, "-xJ") {
		t.Fatalf("%v", args)
	}
	if !strings.Contains(joined, "-f -") {
		t.Fatalf("mapped tar must read stdin: %v", args)
	}
	for _, a := range args {
		if a == archive || strings.Contains(a, "lxc-images") {
			t.Fatalf("cache path must not enter the user namespace: %v", args)
		}
	}
}

func TestUnpackTarUnprivilegedReadsRestrictedCacheViaStdin(t *testing.T) {
	dir := t.TempDir()
	cache := filepath.Join(dir, "cache", "debian", "trixie", "amd64", "default")
	if err := os.MkdirAll(cache, 0o750); err != nil {
		t.Fatal(err)
	}
	archive := filepath.Join(cache, "a4b36e63a2d16d508e7191e8ed20bed908e71e928bb92b30ddd46861c7c6edf9.tar.xz")
	if err := os.WriteFile(archive, []byte("ndl-restricted-image"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Join(dir, "cache"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(cache, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(archive, 0o640); err != nil {
		t.Fatal(err)
	}
	rootfs := filepath.Join(dir, "rootfs")
	if err := os.MkdirAll(rootfs, 0o750); err != nil {
		t.Fatal(err)
	}

	var gotName string
	var gotArgs []string
	var gotStdin []byte
	e := &Engine{
		RunStdin: func(_ context.Context, name string, stdin *os.File, args ...string) ([]byte, error) {
			gotName = name
			gotArgs = append([]string{}, args...)
			if stdin == nil {
				t.Fatal("privileged parent must pass the open archive")
			}
			buf := make([]byte, 64)
			n, err := stdin.Read(buf)
			if err != nil {
				t.Fatal(err)
			}
			gotStdin = buf[:n]
			return nil, nil
		},
	}
	if err := e.unpackTar(context.Background(), Spec{UIDMap: DefaultUIDMap, GIDMap: DefaultGIDMap}, archive, rootfs); err != nil {
		t.Fatal(err)
	}
	if gotName != BinUsernsExec {
		t.Fatalf("name=%s", gotName)
	}
	joined := strings.Join(gotArgs, " ")
	if !strings.Contains(joined, "-f -") || !strings.Contains(joined, "-C .") {
		t.Fatalf("must extract from stdin into cwd: %v", gotArgs)
	}
	for _, a := range gotArgs {
		if a == archive {
			t.Fatal("mapped tar must not receive the cache pathname")
		}
	}
	if string(gotStdin) != "ndl-restricted-image" {
		t.Fatalf("stdin %q", gotStdin)
	}
	if _, err := os.Stat(archive); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" {
		st, err := os.Stat(archive)
		if err != nil || st.Mode().Perm() != 0o640 {
			t.Fatalf("cache mode changed: %v %v", st, err)
		}
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
	dir := t.TempDir()
	archive := filepath.Join(dir, "a.tar.xz")
	rootfs := filepath.Join(dir, "rootfs")
	if err := os.WriteFile(archive, []byte("x"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(rootfs, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := e.unpackTar(context.Background(), Spec{UIDMap: DefaultUIDMap, GIDMap: DefaultGIDMap}, archive, rootfs); err != nil {
		t.Fatal(err)
	}
	if gotName != BinUsernsExec {
		t.Fatalf("name=%s", gotName)
	}
	want := usernsExtractArgs(DefaultUIDMap, DefaultGIDMap, archive, ".")
	if strings.Join(gotArgs, " ") != strings.Join(want, " ") {
		t.Fatalf("%v", gotArgs)
	}
}

func TestUnpackTarPrivilegedUsesHostTar(t *testing.T) {
	var gotName string
	var gotArgs []string
	e := &Engine{
		Run: func(_ context.Context, name string, args ...string) ([]byte, error) {
			gotName = name
			gotArgs = append([]string{}, args...)
			return nil, nil
		},
	}
	if err := e.unpackTar(context.Background(), Spec{Privileged: true}, "/img/a.tar.xz", "/rootfs"); err != nil {
		t.Fatal(err)
	}
	if gotName != BinTar {
		t.Fatalf("name=%s", gotName)
	}
	if !strings.Contains(strings.Join(gotArgs, " "), "/img/a.tar.xz") {
		t.Fatalf("privileged extract may open the cache path: %v", gotArgs)
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
