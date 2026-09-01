package qemu

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestValidateWorkloadIDRejectsNonUUID(t *testing.T) {
	for _, id := range []string{"", "x", "not-a-uuid", "nodal-vm@x", "../escape"} {
		if err := ValidateWorkloadID(id); err == nil {
			t.Fatalf("expected reject for %q", id)
		}
	}
	ok := "33333333-3333-3333-3333-333333333333"
	if err := ValidateWorkloadID(ok); err != nil {
		t.Fatal(err)
	}
	e := &Engine{DataDir: t.TempDir(), SkipHostCmds: true}
	if err := e.Start(context.Background(), "not-a-uuid"); err == nil {
		t.Fatal("Start must reject a non-UUID")
	}
	if err := e.ForceStop(context.Background(), "x"); err == nil {
		t.Fatal("ForceStop must reject a non-UUID")
	}
	if _, err := e.Discover("lab"); err == nil {
		t.Fatal("Discover must reject a non-UUID")
	}
}

func TestCleanupFailedLaunchKeepsDiskAndLastApplied(t *testing.T) {
	root := t.TempDir()
	e := &Engine{DataDir: root, SkipHostCmds: true, Now: func() time.Time { return time.Unix(1, 0).UTC() }}
	id := "44444444-4444-4444-4444-444444444444"
	disk := filepath.Join(root, "volumes", id+".qcow2")
	if err := os.MkdirAll(filepath.Dir(disk), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(disk, []byte("qcow2-identity"), 0o640); err != nil {
		t.Fatal(err)
	}
	locator := "/var/lib/ndl/storage/p/volumes/vm-disk/" + id + ".qcow2"
	if _, err := e.Prepare(Spec{
		WorkloadID: id,
		VolumeID:   "vol-keep",
		DiskPath:   locator,
		Accel:      "tcg",
		Machine:    DefaultMachine,
	}); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(e.runtimeDir(id), 0o750); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{e.qmpPath(id), e.serialPath(id), e.vncPath(id), e.qgaPath(id)} {
		if err := os.WriteFile(p, []byte("stale"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := e.CleanupFailedLaunch(id); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(disk); err != nil {
		t.Fatalf("VolumeHandle disk must remain: %v", err)
	}
	if _, err := os.Stat(e.appliedPath(id)); err != nil {
		t.Fatalf("last-applied is identity and must remain: %v", err)
	}
	if _, err := os.Stat(e.argvPath(id)); err != nil {
		t.Fatalf("frozen argv must remain: %v", err)
	}
	for _, p := range []string{e.qmpPath(id), e.serialPath(id), e.vncPath(id), e.qgaPath(id)} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Fatalf("stale socket %s must be removed", p)
		}
	}
}

func TestDiscoverUsesLastAppliedIdentity(t *testing.T) {
	root := t.TempDir()
	e := &Engine{DataDir: root, SkipHostCmds: true}
	id := "55555555-5555-5555-5555-555555555555"
	disk := "/var/lib/ndl/storage/p/volumes/vm-disk/" + id + ".qcow2"
	if _, err := e.Prepare(Spec{
		WorkloadID: id,
		VolumeID:   "vol-disc",
		DiskPath:   disk,
		Accel:      "tcg",
		Machine:    DefaultMachine,
	}); err != nil {
		t.Fatal(err)
	}
	got, err := e.Discover(id)
	if err != nil {
		t.Fatal(err)
	}
	if got.WorkloadID != id || got.VolumeID != "vol-disc" || got.DiskPath != disk {
		t.Fatalf("%+v", got)
	}
	if got.Applied.SchemaVersion != LastAppliedSchema {
		t.Fatal(got.Applied.SchemaVersion)
	}
	if got.Status != StatusStopped {
		t.Fatal(got.Status)
	}
}

func TestPackageHasNoHostExec(t *testing.T) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("caller")
	}
	dir := filepath.Dir(file)
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	hostExec := []byte("Host" + "." + "Exec")
	runCmd := []byte("Run" + "Command")
	for _, ent := range entries {
		if ent.IsDir() || !strings.HasSuffix(ent.Name(), ".go") || strings.HasSuffix(ent.Name(), "_test.go") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, ent.Name()))
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(raw, hostExec) {
			t.Fatalf("%s must not contain %s", ent.Name(), hostExec)
		}
		if bytes.Contains(raw, runCmd) {
			t.Fatalf("%s must not invent %s", ent.Name(), runCmd)
		}
	}
}
