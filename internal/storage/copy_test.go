package storage

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCopyFileChecksum(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.qcow2")
	dst := filepath.Join(dir, "dst.qcow2")
	if err := os.WriteFile(src, []byte("qcow2-payload"), 0o640); err != nil {
		t.Fatal(err)
	}
	res, err := CopyFile(src, dst)
	if err != nil {
		t.Fatal(err)
	}
	if res.Size != 13 || res.SHA256 == "" {
		t.Fatalf("%+v", res)
	}
	got, _ := os.ReadFile(dst)
	if string(got) != "qcow2-payload" {
		t.Fatal(string(got))
	}
}

func TestCopyReplaceOverwrites(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "a.qcow2")
	dst := filepath.Join(dir, "b.qcow2")
	if err := os.WriteFile(src, []byte("one"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dst, []byte("old"), 0o640); err != nil {
		t.Fatal(err)
	}
	res, err := CopyReplace(src, dst)
	if err != nil {
		t.Fatal(err)
	}
	if res.Size != 3 {
		t.Fatalf("%+v", res)
	}
	got, _ := os.ReadFile(dst)
	if string(got) != "one" {
		t.Fatal(string(got))
	}
}

func TestCopyFileRejectsTraversal(t *testing.T) {
	if _, err := CopyFile("/etc/passwd", "/tmp/out"); err == nil {
		t.Fatal("tmp dest")
	}
}
