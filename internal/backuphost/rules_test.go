package backuphost

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/no-dal/ndl-ce/internal/backup"
)

func write(t *testing.T, p, body string) {
	t.Helper()
	_ = os.MkdirAll(filepath.Dir(p), 0o755)
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestRulesSaveLoadAndValidate(t *testing.T) {
	root := t.TempDir()
	if r, err := LoadRules(root); err != nil || len(r.Skip)+len(r.Keep) != 0 {
		t.Fatalf("no rules yet: %+v %v", r, err)
	}
	saved, err := SaveRules(root, WorkloadRules{Skip: []string{"/var/cache/", "/var/cache", " /srv/media "}, Keep: []string{"/var/cache/app/keep"}})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(saved.Skip, ",") != "/srv/media,/var/cache" || saved.Version != 1 {
		t.Fatalf("normalized %+v", saved)
	}
	got, err := LoadRules(root)
	if err != nil || strings.Join(got.Keep, ",") != "/var/cache/app/keep" {
		t.Fatalf("loaded %+v %v", got, err)
	}
	for _, bad := range []WorkloadRules{{Skip: []string{"/"}}, {Skip: []string{"relative"}}, {Skip: []string{"/a"}, Keep: []string{"/a"}}} {
		if _, err := SaveRules(root, bad); err == nil {
			t.Fatalf("%+v must be refused", bad)
		}
	}
}

func TestRulesRefuseALinkOutOfTheWorkload(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, ".ndl-backup")); err != nil {
		t.Fatal(err)
	}
	if _, err := SaveRules(root, WorkloadRules{Skip: []string{"/tmp"}}); err == nil {
		t.Fatal("writing through a guest symlink must be refused")
	}
	if entries, _ := os.ReadDir(outside); len(entries) != 0 {
		t.Fatal("nothing may be written outside the workload")
	}
	if _, _, err := resolveGuestDir(root, "/.ndl-backup"); err == nil {
		t.Fatal("browsing through a link must be refused")
	}
}

func TestBrowseSizesLabelsAndRules(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "var/lib/docker/volumes/nextcloud_data/_data/a.bin"), strings.Repeat("x", 1000))
	write(t, filepath.Join(root, "var/log/syslog"), "log")
	_, _ = SaveRules(root, WorkloadRules{Skip: []string{"/var/log"}})
	l, err := Browse(context.Background(), root, "/var", 0)
	if err != nil {
		t.Fatal(err)
	}
	by := map[string]BrowseEntry{}
	for _, e := range l.Entries {
		by[e.Name] = e
	}
	if by["log"].Label != "Logs" || by["log"].Rule != "skip" || by["lib"].Size != 1000 {
		t.Fatalf("entries %+v", l.Entries)
	}
	vols, _ := Browse(context.Background(), root, "/var/lib/docker/volumes", 0)
	if len(vols.Entries) != 1 || vols.Entries[0].Label != "Docker: nextcloud" {
		t.Fatalf("volumes %+v", vols.Entries)
	}
	inner, _ := Browse(context.Background(), root, "/var/log", 0)
	if inner.Entries[0].Effective != "skip" || inner.Entries[0].Rule != "" {
		t.Fatalf("a file inside a skipped folder is skipped through it: %+v", inner.Entries)
	}
}

func TestRulesShapeWhatACaptureKeeps(t *testing.T) {
	src := t.TempDir()
	rules := WorkloadRules{Skip: []string{"/var/cache"}, Keep: []string{"/var/cache/app/important"}}
	// A Full capture lists nothing to include: everything is kept.
	inc, exc := applyRules(nil, nil, rules)
	cases := map[string]bool{
		"var/cache/junk":               false,
		"var/cache/app/important/a.db": true,
		"etc/hostname":                 true,
	}
	for rel, want := range cases {
		if got := backup.Captured(src, rel, inc, exc); got != want {
			t.Fatalf("%s captured=%v, want %v (inc=%v exc=%v)", rel, got, want, inc, exc)
		}
	}
	// A narrow scope keeps its own includes and gains the kept path.
	inc, exc = applyRules([]string{"/srv"}, nil, rules)
	if !backup.Captured(src, "var/cache/app/important/a.db", inc, exc) || backup.Captured(src, "etc/hostname", inc, exc) {
		t.Fatalf("narrow scope inc=%v exc=%v", inc, exc)
	}
}
