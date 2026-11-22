package gameserver

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

// jvsScope is a Garry's Mod scope shaped like the JvS one.
var jvsScope = BackupScope{
	Include: []string{
		"garrysmod/gamemodes/starwarsrp",
		"garrysmod/addons",
		"garrysmod/sv.db",
		"garrysmod/data/thesenate",
		"garrysmod/cfg",
	},
	Exclude: []string{
		"garrysmod/addons/ws_*",
		"garrysmod/addons/[0-9]*",
		"garrysmod/addons/*.zip",
		"garrysmod/data/thesenate/backups",
	},
	Newest: []string{"garrysmod/data/thesenate/backups/*"},
}

func writeTree(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for rel, body := range files {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func archiveNames(t *testing.T, path string) []string {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	tr := tar.NewReader(gz)
	var out []string
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if h.Typeflag == tar.TypeReg {
			out = append(out, h.Name)
		}
	}
	sort.Strings(out)
	return out
}

func TestScopedBackupTakesOnlyTheScope(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, map[string]string{
		"srcds_run":                                     "install",
		"steam_cache/content/big.bin":                   "cache",
		"sourceengine/x.vpk":                            "install",
		"garrysmod/garrysmod_000.vpk":                   "install",
		"garrysmod/cache/srcds/a":                       "cache",
		"garrysmod/logs/l.log":                          "log",
		"garrysmod/gamemodes/base/init.lua":             "stock",
		"garrysmod/gamemodes/starwarsrp/gamemode/i.lua": "code",
		"garrysmod/addons/ts_tools/lua/a.lua":           "code",
		"garrysmod/addons/WOS&GUNS/key.lua":             "code",
		"garrysmod/addons/workshop-3520969473.txt":      "list",
		"garrysmod/addons/ws_12345/addon.json":          "workshop",
		"garrysmod/addons/3520969473/addon.json":        "workshop",
		"garrysmod/addons/pack.zip":                     "zip",
		"garrysmod/sv.db":                               "db",
		"garrysmod/data/thesenate/config.json":          "cfg",
		"garrysmod/data/thesenate/backups/old.json":     "old",
		"garrysmod/data/thesenate/backups/new.json":     "new",
		"garrysmod/data/wos/dynabase/x":                 "other",
		"garrysmod/cfg/server.cfg":                      "cfg",
		"garrysmod/maps/graphs/rp_ag_jvs.ain":           "graph",
	})
	old := time.Now().Add(-time.Hour)
	_ = os.Chtimes(filepath.Join(root, "garrysmod/data/thesenate/backups/old.json"), old, old)
	settled := time.Now().Add(-10 * time.Minute)
	_ = os.Chtimes(filepath.Join(root, "garrysmod/data/thesenate/backups/new.json"), settled, settled)
	// Still being written: too fresh to trust.
	writeTree(t, root, map[string]string{"garrysmod/data/thesenate/backups/writing.json": "half"})

	dest := filepath.Join(t.TempDir(), "b.tar.gz")
	if err := archiveDir(context.Background(), root, dest, jvsScope); err != nil {
		t.Fatal(err)
	}
	got := strings.Join(archiveNames(t, dest), "\n")
	want := strings.Join([]string{
		".ndl-backup.json",
		"garrysmod/addons/WOS&GUNS/key.lua",
		"garrysmod/addons/ts_tools/lua/a.lua",
		"garrysmod/addons/workshop-3520969473.txt",
		"garrysmod/cfg/server.cfg",
		"garrysmod/data/thesenate/backups/new.json",
		"garrysmod/data/thesenate/config.json",
		"garrysmod/gamemodes/starwarsrp/gamemode/i.lua",
		"garrysmod/sv.db",
	}, "\n")
	if got != want {
		t.Fatalf("archive has:\n%s\nwant:\n%s", got, want)
	}

	// Restore: the scope is put back exactly, the rest is untouched.
	writeTree(t, root, map[string]string{
		"garrysmod/sv.db":                       "changed",
		"garrysmod/addons/ts_tools/lua/new.lua": "added after the backup",
		"garrysmod/data/thesenate/backups/x":    "newer rotating copy",
	})
	_ = os.Remove(filepath.Join(root, "garrysmod/cfg/server.cfg"))
	if err := restoreArchive(root, dest); err != nil {
		t.Fatal(err)
	}
	read := func(rel string) string {
		b, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			return "<missing>"
		}
		return string(b)
	}
	checks := map[string]string{
		"garrysmod/sv.db":                           "db",
		"garrysmod/cfg/server.cfg":                  "cfg",
		"garrysmod/addons/ts_tools/lua/new.lua":     "<missing>",
		"garrysmod/data/thesenate/backups/x":        "newer rotating copy",
		"garrysmod/data/thesenate/backups/old.json": "old",
		"srcds_run":                            "install",
		"steam_cache/content/big.bin":          "cache",
		"garrysmod/addons/ws_12345/addon.json": "workshop",
		"garrysmod/gamemodes/base/init.lua":    "stock",
	}
	for rel, want := range checks {
		if got := read(rel); got != want {
			t.Errorf("%s = %q, want %q", rel, got, want)
		}
	}
}

func TestRestoreKeepsLargeFilesAndModes(t *testing.T) {
	src := t.TempDir()
	big := filepath.Join(src, "sv.db")
	f, err := os.Create(big)
	if err != nil {
		t.Fatal(err)
	}
	// Bigger than the old 128 MiB restore cut-off.
	if err := f.Truncate(130 << 20); err != nil {
		t.Fatal(err)
	}
	_, _ = f.WriteAt([]byte("tail"), (130<<20)-4)
	_ = f.Close()
	if err := os.WriteFile(filepath.Join(src, "srcds_run"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(t.TempDir(), "b.tar.gz")
	if err := archiveDir(context.Background(), src, dest, BackupScope{}); err != nil {
		t.Fatal(err)
	}
	out := t.TempDir()
	if err := restoreArchive(out, dest); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(filepath.Join(out, "sv.db"))
	if err != nil || st.Size() != 130<<20 {
		t.Fatalf("sv.db restored at %v bytes (%v)", st, err)
	}
	if st, err := os.Stat(filepath.Join(out, "srcds_run")); err != nil || st.Mode().Perm()&0o100 == 0 {
		t.Fatalf("srcds_run lost its execute bit: %v %v", st, err)
	}
}

func TestBackupScopeStoredOutsideTheServerFolder(t *testing.T) {
	h := &LocalHost{RT: NewRuntime(t.TempDir())}
	ctx := context.Background()
	if s, err := h.BackupScope(ctx, "srv1"); err != nil || s.Scoped() {
		t.Fatalf("default scope: %v %v", s, err)
	}
	if _, err := h.SetBackupScope(ctx, "srv1", BackupScope{Include: []string{"../etc"}}); err == nil {
		t.Fatal("a path outside the server folder must be refused")
	}
	if _, err := h.SetBackupScope(ctx, "srv1", BackupScope{Exclude: []string{"a"}}); err == nil {
		t.Fatal("an exclude with nothing included must be refused")
	}
	got, err := h.SetBackupScope(ctx, "srv1", BackupScope{Include: []string{" /garrysmod/cfg/ ", "garrysmod/cfg"}})
	if err != nil || len(got.Include) != 1 || got.Include[0] != "garrysmod/cfg" {
		t.Fatalf("clean scope: %v %v", got, err)
	}
	p, _ := h.backupScopePath("srv1")
	if !strings.HasPrefix(p, filepath.Join(h.RT.Root, "game-backups")) {
		t.Fatalf("scope stored at %s", p)
	}
	if read, _ := h.BackupScope(ctx, "srv1"); !read.Scoped() {
		t.Fatal("scope did not persist")
	}
	if _, err := h.SetBackupScope(ctx, "srv1", BackupScope{}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(p); !os.IsNotExist(err) {
		t.Fatal("clearing the scope should remove the file")
	}
}
