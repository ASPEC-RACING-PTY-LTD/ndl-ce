package lxc

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRaiseSysctlDoesNotLower(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "maxkeys")
	if err := os.WriteFile(p, []byte("200\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	raiseSysctl(p, 200000)
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != "200000\n" {
		t.Fatalf("%q", b)
	}
	raiseSysctl(p, 1000)
	b, _ = os.ReadFile(p)
	if string(b) != "200000\n" {
		t.Fatalf("must not lower an already sufficient value: %q", b)
	}
}
