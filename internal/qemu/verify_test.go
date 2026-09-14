package qemu

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCheckOfflineChecksumMismatchIsUnverified(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "disk.qcow2")
	if err := os.WriteFile(src, []byte("qcow-fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	e := &Engine{SkipHostCmds: true}
	res, err := e.CheckOffline(t.Context(), src, "deadbeef")
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != VerifyUnverified || res.Reason != "checksum mismatch" {
		t.Fatalf("%+v", res)
	}
	if res.QEMUImgOK {
		t.Fatal("mismatch must not report qemu-img ok")
	}
}

func TestCheckOfflineSkipHostCmdsDoesNotInventVerified(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "disk.qcow2")
	if err := os.WriteFile(src, []byte("qcow-fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	sum, _, err := checksumFile(src)
	if err != nil {
		t.Fatal(err)
	}
	e := &Engine{SkipHostCmds: true}
	res, err := e.CheckOffline(t.Context(), src, sum)
	if err != nil {
		t.Fatal(err)
	}
	if res.Status == VerifyVerified || res.QEMUImgOK {
		t.Fatalf("skip-host must not invent verified: %+v", res)
	}
}

func TestExtractJailAndSkipHostUnavailable(t *testing.T) {
	e := &Engine{SkipHostCmds: true}
	if _, err := e.ExtractOffline(t.Context(), "/var/lib/ndl/backups/a.qcow2", "../etc/passwd", "/var/lib/ndl/restore-files/x"); err == nil {
		t.Fatal("traversal must fail")
	}
	res, err := e.ExtractOffline(t.Context(), "/var/lib/ndl/backups/a.qcow2", "/etc/hostname", "/var/lib/ndl/restore-files/hostname")
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != VerifyUnavailable {
		t.Fatalf("%+v", res)
	}
}
