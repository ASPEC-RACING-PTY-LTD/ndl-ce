package control

import (
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"
)

func readAll(t *testing.T, fsys fs.FS, name string) string {
	t.Helper()
	f, err := fsys.Open(name)
	if err != nil {
		return "ERR " + err.Error()
	}
	defer f.Close()
	b, _ := io.ReadAll(f)
	return string(b)
}

func TestLiveUISwitchesWithoutRestartAndKeepsOldChunks(t *testing.T) {
	root := t.TempDir()
	packaged := fstest.MapFS{"index.html": {Data: []byte("packaged")}}
	ui := newLiveUI(root, packaged)
	if got := readAll(t, ui, "index.html"); got != "packaged" {
		t.Fatalf("without a live release: %q", got)
	}
	mk := func(id, file, body string) string {
		dir := filepath.Join(root, "releases", id, "files", "ui")
		_ = os.MkdirAll(filepath.Join(dir, "assets"), 0o755)
		_ = os.WriteFile(filepath.Join(dir, file), []byte(body), 0o644)
		return dir
	}
	r1 := mk("r1", "index.html", "one")
	mk("r1", "assets/old.js", "old chunk")
	r2 := mk("r2", "index.html", "two")
	_ = os.Symlink(r1, filepath.Join(root, "ui"))
	if got := readAll(t, ui, "index.html"); got != "one" {
		t.Fatalf("live r1: %q", got)
	}
	_ = os.Remove(filepath.Join(root, "ui"))
	_ = os.Symlink(r2, filepath.Join(root, "ui"))
	if got := readAll(t, ui, "index.html"); got != "two" {
		t.Fatalf("switch to r2 without reopening: %q", got)
	}
	if got := readAll(t, ui, "assets/old.js"); got != "old chunk" {
		t.Fatalf("a page from r1 still loads its chunks: %q", got)
	}
}
