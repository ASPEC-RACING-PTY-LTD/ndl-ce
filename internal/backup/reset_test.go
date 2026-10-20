package backup

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResetEmptiesTheRepositoryButKeepsKeyAndTargets(t *testing.T) {
	root := t.TempDir()
	keys := testKeys(t)
	repo, err := OpenRepository(root, keys)
	if err != nil {
		t.Fatal(err)
	}
	plain := randBytes(7, 20<<10)
	id := keys.ID(plain)
	frame, _ := compressChunk(plain, 0, false)
	sealed, _ := keys.Seal(id, frame)
	pw := repo.newPackWriter(0)
	if _, err := pw.add(id, sealed); err != nil {
		t.Fatal(err)
	}
	if err := pw.flush(); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"master.key", "state/targets/r2.json", "state/remote-uploaded/r2.txt", "state/queue/r2/x.job", "snapshots/ns/b.snap"} {
		p := filepath.Join(root, f)
		_ = os.MkdirAll(filepath.Dir(p), 0o750)
		if err := os.WriteFile(p, []byte("x"), 0o640); err != nil {
			t.Fatal(err)
		}
	}
	freed, err := repo.Reset()
	if err != nil || freed == 0 {
		t.Fatalf("reset: freed %d, %v", freed, err)
	}
	for _, gone := range []string{"state/remote-uploaded", "state/queue", "snapshots/ns"} {
		if _, err := os.Stat(filepath.Join(root, gone)); !os.IsNotExist(err) {
			t.Fatalf("%s must be gone", gone)
		}
	}
	if entries, _ := os.ReadDir(filepath.Join(root, "packs")); len(entries) != 0 {
		t.Fatalf("packs must be empty: %v", entries)
	}
	for _, kept := range []string{"master.key", "state/targets/r2.json"} {
		if _, err := os.Stat(filepath.Join(root, kept)); err != nil {
			t.Fatalf("%s must stay: %v", kept, err)
		}
	}
	if _, ok := repo.index[id]; ok {
		t.Fatal("the in-memory index must be empty")
	}
	// The repository still works after a reset.
	pw = repo.newPackWriter(0)
	if isNew, err := pw.add(id, sealed); err != nil || !isNew {
		t.Fatalf("a chunk must be stored again after reset: %v %v", isNew, err)
	}
	if err := pw.flush(); err != nil {
		t.Fatal(err)
	}
}
