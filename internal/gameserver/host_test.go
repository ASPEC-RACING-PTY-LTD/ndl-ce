package gameserver

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestArchiveDirCopiesExactHeaderSize(t *testing.T) {
	src := t.TempDir()
	dest := filepath.Join(t.TempDir(), "snap.tar.gz")
	if err := os.WriteFile(filepath.Join(src, "server.jar"), bytes.Repeat([]byte("j"), 256<<10), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := archiveDir(src, dest); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(dest)
	if err != nil || st.Size() < 100 {
		t.Fatalf("archive %v %v", st, err)
	}
}

func TestLocalHostKeepsEveryOperationInsideTheServerFolder(t *testing.T) {
	root := t.TempDir()
	h := NewLocalHost(root)
	ctx := context.Background()
	id := "0f8c7c1e-1d2b-4b7a-9f1e-3f2a1b0c9d8e"
	if _, err := h.EnsureData(ctx, id); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"", "../etc", "a/b", ".hidden"} {
		if _, err := h.EnsureData(ctx, bad); err == nil {
			t.Fatalf("id %q must be refused", bad)
		}
		if err := h.WriteFile(ctx, bad, "x", []byte("y")); err == nil {
			t.Fatalf("write for id %q must be refused", bad)
		}
	}
	if err := h.WriteFile(ctx, id, "config/server.properties", []byte("motd=hi\n")); err != nil {
		t.Fatal(err)
	}
	if err := h.WriteFile(ctx, id, "../escape.txt", []byte("no")); err == nil {
		t.Fatal("writes must stay inside the server folder")
	}
	body, err := h.ReadFile(ctx, id, "config/server.properties", 1<<20)
	if err != nil || string(body) != "motd=hi\n" {
		t.Fatalf("%q %v", body, err)
	}
	if _, err := h.ReadFile(ctx, id, "config/server.properties", 3); err == nil {
		t.Fatal("read limit must apply")
	}
	listing, err := h.ListDir(ctx, id, "config")
	if err != nil || len(listing.Items) != 1 || listing.Items[0].Name != "server.properties" {
		t.Fatalf("%+v %v", listing, err)
	}
	if err := h.Rename(ctx, id, "config/server.properties", "config/server.properties.disabled"); err != nil {
		t.Fatal(err)
	}

	b, err := h.Backup(ctx, id, "b1")
	if err != nil || !strings.HasPrefix(b.Path, filepath.Join(root, "game-backups", id)) || b.Bytes == 0 {
		t.Fatalf("%+v %v", b, err)
	}
	if _, err := h.Backup(ctx, id, "../../x"); err == nil {
		t.Fatal("backup ids must be validated")
	}
	if err := h.Remove(ctx, id, "config"); err != nil {
		t.Fatal(err)
	}
	if err := h.RestoreBackup(ctx, id, "b1"); err != nil {
		t.Fatal(err)
	}
	if _, err := h.ReadFile(ctx, id, "config/server.properties.disabled", 1<<20); err != nil {
		t.Fatalf("restore lost the file: %v", err)
	}
	if err := h.DeleteBackup(ctx, id, "b1"); err != nil {
		t.Fatal(err)
	}
	if err := h.RemoveData(ctx, id); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "gameservers", id)); !os.IsNotExist(err) {
		t.Fatalf("server folder must be removed: %v", err)
	}
}

func TestHumanErrorExplainsMissingDocker(t *testing.T) {
	if got := HumanError("fork/exec /usr/bin/docker: no such file or directory"); !strings.Contains(got, "apt-get install docker.io") {
		t.Fatal(got)
	}
	if got := HumanError("Cannot connect to the Docker daemon at unix:///var/run/docker.sock. Is the docker daemon running?"); !strings.Contains(got, "not running") {
		t.Fatal(got)
	}
}
