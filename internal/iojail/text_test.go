package iojail

import "testing"

func TestLooksBinary(t *testing.T) {
	if LooksBinary("notes.txt", []byte("hello\nworld")) {
		t.Fatal("text must not be binary")
	}
	if !LooksBinary("photo.png", []byte("hello")) {
		t.Fatal("png extension is binary")
	}
	if !LooksBinary("blob", []byte("a\x00b")) {
		t.Fatal("NUL is binary")
	}
}

func TestChmodBeneathRejectsEscape(t *testing.T) {
	root := t.TempDir()
	if err := ChmodBeneath(root, "../passwd", 0o644); err == nil {
		t.Fatal("chmod escape must fail")
	}
}
