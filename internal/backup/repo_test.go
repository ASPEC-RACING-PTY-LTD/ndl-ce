package backup

import (
	"testing"
)

func TestRepositoryDedupAndIndexRebuild(t *testing.T) {
	root := t.TempDir()
	keys := testKeys(t)
	repo, err := OpenRepository(root, keys)
	if err != nil {
		t.Fatal(err)
	}
	plain := randBytes(51, 40<<10)
	id := keys.ID(plain)
	frame, err := compressChunk(plain)
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := keys.Seal(id, frame)
	if err != nil {
		t.Fatal(err)
	}

	pw := repo.newPackWriter(0)
	isNew, err := pw.add(id, sealed)
	if err != nil || !isNew {
		t.Fatalf("first add should be new: new=%v err=%v", isNew, err)
	}
	// Adding the identical chunk again must dedup within the session.
	isNew2, err := pw.add(id, sealed)
	if err != nil || isNew2 {
		t.Fatalf("duplicate add should not be new: new=%v err=%v", isNew2, err)
	}
	if err := pw.flush(); err != nil {
		t.Fatal(err)
	}

	// Reopen the repository from scratch: the index is rebuilt from pack
	// sidecars, proving the repository is self-describing.
	repo2, err := OpenRepository(root, keys)
	if err != nil {
		t.Fatal(err)
	}
	if !repo2.Has(id) {
		t.Fatalf("rebuilt index missing chunk")
	}
	got, err := repo2.GetChunk(id)
	if err != nil {
		t.Fatal(err)
	}
	roundtrip, err := decompressChunk(got)
	if err != nil {
		t.Fatal(err)
	}
	if string(roundtrip) != string(plain) {
		t.Fatalf("chunk roundtrip mismatch")
	}

	// A second, already-present chunk must not be stored again.
	pw2 := repo2.newPackWriter(0)
	isNew3, err := pw2.add(id, sealed)
	if err != nil || isNew3 {
		t.Fatalf("already-stored chunk must dedup across sessions: new=%v err=%v", isNew3, err)
	}
}
