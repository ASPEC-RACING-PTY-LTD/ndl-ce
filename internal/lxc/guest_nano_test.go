package lxc

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/google/uuid"
)

func TestEnsureGuestNanoInstallsWhenHostHasIt(t *testing.T) {
	if _, err := os.Lstat("/usr/bin/nano"); err != nil {
		t.Skip("host nano is required")
	}
	root := t.TempDir()
	if err := ensureGuestNano(root); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(root, "usr", "bin", "nano")
	st, err := os.Stat(dst)
	if err != nil {
		t.Fatal("nano must be copied into the guest")
	}
	if st.Mode()&0o111 == 0 {
		t.Fatal("guest nano must be executable")
	}
	want, err := os.ReadFile("/usr/bin/nano")
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(want) {
		t.Fatalf("copied nano size %d, want %d", len(got), len(want))
	}
	if _, err := os.Stat(filepath.Join(root, "etc", "nanorc")); err != nil {
		t.Fatal("nanorc must be copied")
	}
}

func TestEnsureGuestNanoDoesNotOverwrite(t *testing.T) {
	root := t.TempDir()
	bin := filepath.Join(root, "usr", "bin", "nano")
	if err := os.MkdirAll(filepath.Dir(bin), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bin, []byte("guest-nano\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := ensureGuestNano(root); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(bin)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "guest-nano\n" {
		t.Fatal("existing guest nano must not be overwritten")
	}
}

func TestEnsureGuestNanoSkipsWhenHostMissing(t *testing.T) {
	prev := hostNanoBin
	hostNanoBin = filepath.Join(t.TempDir(), "missing-nano")
	defer func() { hostNanoBin = prev }()
	root := t.TempDir()
	if err := ensureGuestNano(root); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "usr", "bin", "nano")); err == nil {
		t.Fatal("missing host nano must not invent a guest binary")
	}
}

func TestApplyGuestFilesInstallsNano(t *testing.T) {
	if _, err := os.Lstat("/usr/bin/nano"); err != nil {
		t.Skip("host nano is required")
	}
	dir := t.TempDir()
	e := &Engine{DataDir: dir}
	id := uuid.NewString()
	root := filepath.Join(dir, "rootfs", id)
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	spec := Spec{
		WorkloadID: id, Name: "nano", ImagePin: "imported",
		VolumeID: uuid.NewString(), RootfsPath: root, SkipImage: true,
		Privileged: false, UIDMap: DefaultUIDMap, GIDMap: DefaultGIDMap,
	}
	if err := e.writeApplied(spec, true, "abc"); err != nil {
		t.Fatal(err)
	}
	if err := e.ApplyGuestFiles(id); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(root, "usr", "bin", "nano")
	st, err := os.Lstat(dst)
	if err != nil {
		t.Fatal("ApplyGuestFiles must install nano")
	}
	sys, ok := st.Sys().(*syscall.Stat_t)
	if !ok {
		t.Fatal("stat")
	}
	if sys.Uid != 100000 || sys.Gid != 100000 {
		t.Fatalf("unprivileged nano owner %d:%d", sys.Uid, sys.Gid)
	}
}

func TestApplyGuestFilesSkipHostCmdsDoesNotCopy(t *testing.T) {
	dir := t.TempDir()
	e := &Engine{DataDir: dir, SkipHostCmds: true, FakeUnpack: true}
	id := uuid.NewString()
	root := filepath.Join(dir, "rootfs", id)
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	spec := Spec{
		WorkloadID: id, Name: "skip", ImagePin: "imported",
		VolumeID: uuid.NewString(), RootfsPath: root, SkipImage: true,
	}
	if err := e.writeApplied(spec, true, "abc"); err != nil {
		t.Fatal(err)
	}
	if err := e.ApplyGuestFiles(id); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "usr", "bin", "nano")); err == nil {
		t.Fatal("SkipHostCmds must not copy host nano")
	}
}

func TestReconcileRuntimeConfigsInstallsNano(t *testing.T) {
	if _, err := os.Lstat("/usr/bin/nano"); err != nil {
		t.Skip("host nano is required")
	}
	dir := t.TempDir()
	e := &Engine{DataDir: dir}
	id := uuid.NewString()
	root := filepath.Join(dir, "rootfs", id)
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	spec := Spec{
		WorkloadID: id, Name: "reconcile-nano", ImagePin: "imported",
		VolumeID: uuid.NewString(), RootfsPath: root, SkipImage: true,
	}
	if err := e.writeApplied(spec, true, "abc"); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(e.configPath(id)), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(e.configPath(id), []byte("lxc.uts.name = reconcile-nano\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	e.ReconcileRuntimeConfigs()
	if _, err := os.Stat(filepath.Join(root, "usr", "bin", "nano")); err != nil {
		t.Fatal("reconcile must install nano into a mounted rootfs")
	}
}
