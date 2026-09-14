//go:build linux

package lxc

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func TestWriteBaselineMarkerChownsMappedRoot(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("chown to mapped uid needs root")
	}
	root := t.TempDir()
	spec := Spec{UIDMap: DefaultUIDMap, GIDMap: DefaultGIDMap}
	e := &Engine{}
	if err := e.writeBaselineMarker(root, spec, true); err != nil {
		t.Fatal(err)
	}
	st, err := os.Lstat(filepath.Join(root, guestBaselineRel))
	if err != nil {
		t.Fatal(err)
	}
	sys, ok := st.Sys().(*syscall.Stat_t)
	if !ok {
		t.Fatal("stat")
	}
	want := uint32(hostMapStart(DefaultUIDMap))
	if sys.Uid != want || sys.Gid != want {
		t.Fatalf("guest-baseline uid/gid %d:%d want %d", sys.Uid, sys.Gid, want)
	}
}

func TestWriteGuestDNSFallbackChownsMappedRoot(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("chown to mapped uid needs root")
	}
	root := t.TempDir()
	spec := Spec{UIDMap: DefaultUIDMap, GIDMap: DefaultGIDMap}
	if err := writeGuestDNSFallback(root, spec); err != nil {
		t.Fatal(err)
	}
	st, err := os.Lstat(filepath.Join(root, guestDNSFallbackRel))
	if err != nil {
		t.Fatal(err)
	}
	sys, ok := st.Sys().(*syscall.Stat_t)
	if !ok {
		t.Fatal("stat")
	}
	want := uint32(hostMapStart(DefaultUIDMap))
	if sys.Uid != want {
		t.Fatalf("dns fallback uid %d want %d", sys.Uid, want)
	}
}
