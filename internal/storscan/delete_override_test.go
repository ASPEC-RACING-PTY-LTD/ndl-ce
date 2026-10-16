package storscan

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDeleteOverrideSkipsNodalProtection(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "keep", "data"), 0o755); err != nil {
		t.Fatal(err)
	}
	none := func() ([]string, error) { return nil, nil }
	res := Delete(root, []string{"keep"}, []string{filepath.Join(root, "keep")}, nil, none)
	if res[0].Deleted {
		t.Fatal("protected item deleted without override")
	}
	res = DeleteOverride(root, []string{"keep"}, none)
	if !res[0].Deleted {
		t.Fatalf("override refused: %s", res[0].Error)
	}
	mounted := filepath.Join(root, "mnt")
	_ = os.Mkdir(mounted, 0o755)
	res = DeleteOverride(root, []string{"mnt"}, func() ([]string, error) { return []string{mounted}, nil })
	if res[0].Deleted {
		t.Fatal("override deleted a mounted filesystem")
	}
}
