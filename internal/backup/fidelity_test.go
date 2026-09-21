package backup

import (
	"context"
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"golang.org/x/sys/unix"
)

func TestMetadataFidelityRoundTrip(t *testing.T) {
	e := testEngine(t, smallCfg())
	src := t.TempDir()
	writeFile(t, filepath.Join(src, "data.bin"), []byte("payload"), 0o640)
	if err := os.Symlink("data.bin", filepath.Join(src, "link")); err != nil {
		t.Fatal(err)
	}
	if err := unix.Mkfifo(filepath.Join(src, "pipe"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(filepath.Join(src, "data.bin"), filepath.Join(src, "alias.bin")); err != nil {
		t.Fatal(err)
	}
	if err := unix.Lsetxattr(filepath.Join(src, "data.bin"), "user.ndl", []byte("keep"), 0); err != nil {
		t.Skip("xattrs are not supported on this filesystem")
	}

	man, _, err := e.Capture(context.Background(), CaptureOptions{Source: src, WorkloadID: "fid", WorkloadName: "fid"})
	if err != nil {
		t.Fatal(err)
	}
	var sawFIFO, sawLink, sawXattr bool
	hard := 0
	for _, f := range man.Files {
		if f.Hardlink != "" {
			hard++
		}
		switch f.Path {
		case "pipe":
			sawFIFO = f.Type == EntryFIFO
		case "data.bin", "alias.bin":
			if f.Xattrs["user.ndl"] != "" {
				sawXattr = true
			}
		case "link":
			sawLink = f.Type == EntrySymlink
		}
	}
	if !sawFIFO || !sawLink || !sawXattr || hard == 0 {
		t.Fatalf("manifest missing metadata fifo=%v link=%v xattr=%v hard=%d files=%+v", sawFIFO, sawLink, sawXattr, hard, man.Files)
	}

	dst := t.TempDir()
	if err := e.Repo().Restore(context.Background(), man, RestoreOptions{Dest: dst, Chown: false}); err != nil {
		t.Fatal(err)
	}
	st, err := os.Lstat(filepath.Join(dst, "pipe"))
	if err != nil || st.Mode()&os.ModeNamedPipe == 0 {
		t.Fatalf("fifo not restored: %v %v", st, err)
	}
	infoA, _ := os.Stat(filepath.Join(dst, "data.bin"))
	infoB, _ := os.Stat(filepath.Join(dst, "alias.bin"))
	sa, _ := infoA.Sys().(*syscall.Stat_t)
	sb, _ := infoB.Sys().(*syscall.Stat_t)
	if sa == nil || sb == nil || sa.Ino != sb.Ino {
		t.Fatalf("hard link not restored")
	}
	buf := make([]byte, 32)
	n, err := unix.Lgetxattr(filepath.Join(dst, "data.bin"), "user.ndl", buf)
	if err != nil || string(buf[:n]) != "keep" {
		t.Fatalf("xattr not restored: %q %v", buf[:n], err)
	}
}

func TestConsistencyRequiresSuccessfulHook(t *testing.T) {
	e := testEngine(t, smallCfg())
	src := t.TempDir()
	writeFile(t, filepath.Join(src, "f"), []byte("x"), 0o644)
	man, _, err := e.Capture(context.Background(), CaptureOptions{
		Source: src, WorkloadID: "c", WorkloadName: "c",
		Consistency:     ConsistencyApp,
		ConsistencyInfo: ConsistencyReport{Requested: ConsistencyApp, Unit: "nodal-ct@x.service", HookPath: "/etc/ndl/hooks/backup-pre"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if man.Consistency != ConsistencyCrash {
		t.Fatalf("configured unit without a successful hook must stay crash-consistent, got %s", man.Consistency)
	}
	man2, _, err := e.Capture(context.Background(), CaptureOptions{
		Source: src, WorkloadID: "c2", WorkloadName: "c2",
		Consistency:     ConsistencyApp,
		ConsistencyInfo: ConsistencyReport{Requested: ConsistencyApp, HookRan: true, HookOK: true, Result: ConsistencyApp},
	})
	if err != nil {
		t.Fatal(err)
	}
	if man2.Consistency != ConsistencyApp {
		t.Fatalf("successful hook should raise consistency, got %s", man2.Consistency)
	}
}
