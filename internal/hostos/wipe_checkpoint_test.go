package hostos

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWipeCheckpointOnlyTouchesItsOwnFiles(t *testing.T) {
	dir := t.TempDir()
	old := checkpointDir
	checkpointDir = dir
	t.Cleanup(func() { checkpointDir = old })

	id := "0b9f6a3e-6f0e-4c39-9a51-3f2c1f0c7d11"
	keep := "1c2d3e4f-0000-4000-8000-000000000000"
	for _, name := range []string{id + ".tar", id + ".sql", keep + ".tar"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for _, bad := range []string{"", "../etc/passwd", id + "/../x", "ABC"} {
		if res := wipeCheckpoint(UpdateRequest{CheckpointID: bad}, UpdateResult{}, false); res.Status != "failed" {
			t.Fatalf("id %q must be refused: %+v", bad, res)
		}
	}
	if res := wipeCheckpoint(UpdateRequest{CheckpointID: id}, UpdateResult{}, true); res.Status != "planned" {
		t.Fatalf("dry run %+v", res)
	}
	if _, err := os.Stat(filepath.Join(dir, id+".tar")); err != nil {
		t.Fatal("a dry run must not delete anything")
	}
	if res := wipeCheckpoint(UpdateRequest{CheckpointID: id}, UpdateResult{}, false); res.Status != "succeeded" {
		t.Fatalf("wipe %+v", res)
	}
	for _, name := range []string{id + ".tar", id + ".sql"} {
		if _, err := os.Stat(filepath.Join(dir, name)); !os.IsNotExist(err) {
			t.Fatalf("%s should be gone", name)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, keep+".tar")); err != nil {
		t.Fatal("another checkpoint was deleted")
	}
}
