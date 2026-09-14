package migration

import (
	"archive/tar"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeTar(t *testing.T, dest string, files map[string][]byte) {
	t.Helper()
	f, err := os.Create(dest)
	if err != nil {
		t.Fatal(err)
	}
	tw := tar.NewWriter(f)
	for name, body := range files {
		mode := int64(0o644)
		if strings.Contains(name, "init") {
			mode = 0o755
		}
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: mode, Size: int64(len(body))}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write(body); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestExtractVzdumpOrRootfsUnwrapsInnerBackup(t *testing.T) {
	dir := t.TempDir()
	inner := filepath.Join(dir, "inner.tar")
	writeTar(t, inner, map[string][]byte{
		"sbin/init":      []byte("#!/bin/sh\n"),
		"usr/bin/sh":     []byte("x"),
		"bin/ls":         []byte("x"),
		"etc/os-release": []byte("ID=debian\n"),
	})
	innerBody, err := os.ReadFile(inner)
	if err != nil {
		t.Fatal(err)
	}
	outer := filepath.Join(dir, "vzdump-lxc-1.tar")
	writeTar(t, outer, map[string][]byte{
		"etc/vzdump/pct.conf": []byte("ostype: debian\n"),
		"backup":              innerBody,
	})
	dest := filepath.Join(dir, "rootfs")
	if err := ExtractVzdumpOrRootfs(outer, dest); err != nil {
		t.Fatal(err)
	}
	if err := VerifyCopiedRootfs(dest); err != nil {
		t.Fatal(err)
	}
}

func TestExtractVzdumpOrRootfsPlainRootfs(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "rootfs.tar")
	writeTar(t, src, map[string][]byte{
		"sbin/init":      []byte("#!/bin/sh\n"),
		"usr/bin/sh":     []byte("x"),
		"bin/ls":         []byte("x"),
		"etc/os-release": []byte("ID=debian\n"),
	})
	dest := filepath.Join(dir, "out")
	if err := ExtractVzdumpOrRootfs(src, dest); err != nil {
		t.Fatal(err)
	}
}
