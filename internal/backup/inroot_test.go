package backup

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestInRootNeverLeavesTheRoot(t *testing.T) {
	root := t.TempDir()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(os.MkdirAll(filepath.Join(root, "usr/lib"), 0o755))
	must(os.Symlink("usr/lib", filepath.Join(root, "lib")))
	must(os.Symlink("/run", filepath.Join(root, "var-run")))
	must(os.Symlink("../../../../etc", filepath.Join(root, "up")))
	for rel, want := range map[string]string{
		"lib/x.so":       "usr/lib/x.so",
		"var-run/a.pid":  "run/a.pid",
		"up/passwd":      "etc/passwd",
		"../../etc/shad": "etc/shad",
	} {
		got, err := inRoot(root, rel, false)
		if err != nil || got != filepath.Join(root, want) {
			t.Errorf("%s: %s %v, want %s", rel, got, err, want)
		}
	}
	if got, _ := inRoot(root, "lib", false); got != filepath.Join(root, "lib") {
		t.Errorf("the last element is not followed: %s", got)
	}
}

func TestRestoreOverABaseImageStaysInside(t *testing.T) {
	e := testEngine(t, smallCfg())
	src := t.TempDir()
	writeFile(t, filepath.Join(src, "etc/localtime"), []byte("restored zone"), 0o644)
	writeFile(t, filepath.Join(src, "var/lib/app/data.db"), []byte("db"), 0o644)
	man, _, err := e.Capture(context.Background(), CaptureOptions{Source: src, WorkloadID: "w", WorkloadName: "w"})
	if err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	victim := filepath.Join(outside, "zone")
	writeFile(t, victim, []byte("host zone"), 0o644)
	dest := t.TempDir()
	_ = os.MkdirAll(filepath.Join(dest, "etc"), 0o755)
	// A base image link that points out of the tree, absolute.
	if err := os.Symlink(victim, filepath.Join(dest, "etc/localtime")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dest, "var")); err != nil {
		t.Fatal(err)
	}
	if err := e.Repo().Restore(context.Background(), man, RestoreOptions{Dest: dest}); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(victim); string(b) != "host zone" {
		t.Fatal("restore wrote through a link out of the tree")
	}
	if b, _ := os.ReadFile(filepath.Join(dest, "etc/localtime")); string(b) != "restored zone" {
		t.Fatalf("the link must be replaced by the restored file: %q", b)
	}
	if _, err := os.Stat(filepath.Join(outside, "lib/app/data.db")); err == nil {
		t.Fatal("restore followed an absolute directory link out of the tree")
	}
}
