package lxc

import (
	"context"
	"os"
	"path/filepath"
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

func TestUsernsExtractArgsMapAndTar(t *testing.T) {
	args := usernsExtractArgs(DefaultUIDMap, DefaultGIDMap, "/img/root.tar.xz", "/var/lib/ndl/rootfs/x")
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
	if strings.Join(gotArgs, " ") != strings.Join(usernsExtractArgs(DefaultUIDMap, DefaultGIDMap, "/img/a.tar.xz", "/rootfs"), " ") {
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
