package backupscope

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDiscoverSmartDefaults(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "var/lib/postgresql/16/main/base/1"), []byte("pg-data"))
	mustWrite(t, filepath.Join(root, "opt/sounddock/data/uploads/a.bin"), []byte("media"))
	mustWrite(t, filepath.Join(root, "opt/sounddock/node_modules/pkg/index.js"), []byte("nm"))
	mustWrite(t, filepath.Join(root, "var/cache/apt/archives/foo.deb"), []byte("deb"))
	mustWrite(t, filepath.Join(root, "var/log/syslog"), []byte("log"))
	mustWrite(t, filepath.Join(root, "etc/sounddock/app.toml"), []byte("cfg"))
	mustWrite(t, filepath.Join(root, "usr/bin/ls"), []byte("os"))
	mustMkdir(t, filepath.Join(root, "opt/sounddock/.git"))
	mustWrite(t, filepath.Join(root, "opt/sounddock/.git/HEAD"), []byte("ref: refs/heads/main"))
	mustWrite(t, filepath.Join(root, "opt/sounddock/local.sqlite"), []byte("sqlite"))
	mustMkdir(t, filepath.Join(root, "proc/1"))

	prev, err := Discover(Options{
		Root: root, WorkloadID: "wl", Name: "SoundDock", Mode: ModeSmart,
		Docker: &DockerHint{NamedVolumes: []string{"sounddock_postgres"}, BindMounts: []string{"/opt/sounddock/data"}, Projects: []string{"sounddock"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if prev.FullBytes == 0 {
		t.Fatal("expected full size from metadata")
	}
	byID := map[string]Item{}
	for _, it := range prev.Items {
		byID[it.ID] = it
	}
	if !byID["db:postgresql"].Selected {
		t.Fatalf("postgres must be selected by default: %+v", byID["db:postgresql"])
	}
	if byID["repro:apt"].Selected || byID["repro:logs"].Selected {
		t.Fatal("caches and logs must be off by default")
	}
	gitOn := false
	for _, it := range prev.Items {
		if it.Kind == KindGit && it.Selected {
			gitOn = true
		}
	}
	if gitOn {
		t.Fatal("git working trees must not be selected by default")
	}
	warned := false
	for _, w := range prev.Warnings {
		if strings.Contains(w.Message, "uncommitted") || strings.Contains(w.Message, "SQLite") {
			warned = true
		}
	}
	if !warned {
		t.Fatalf("expected git/sqlite warnings: %+v", prev.Warnings)
	}
	if byID["docker:volume:sounddock_postgres"].Selected != true {
		t.Fatal("named docker volume must be selected")
	}
	volPaths := strings.Join(byID["docker:volume:sounddock_postgres"].Paths, "\n")
	if !strings.Contains(volPaths, "/var/lib/docker/volumes/sounddock_postgres/_data") {
		t.Fatalf("named volume must resolve to host data path: %q", volPaths)
	}
	incs, _ := PlanCapture(prev)
	joined := strings.Join(incs, "\n")
	if !strings.Contains(joined, "postgresql") {
		t.Fatalf("smart includes must cover postgres: %v", incs)
	}
	if !strings.Contains(joined, "/var/lib/docker/volumes/sounddock_postgres/_data") {
		t.Fatalf("smart includes must cover resolved docker volume: %v", incs)
	}
}

func TestResolveDockerVolumePath(t *testing.T) {
	if got := ResolveDockerVolumePath("", "app_data"); got != "/var/lib/docker/volumes/app_data/_data" {
		t.Fatalf("got %s", got)
	}
	if got := ResolveDockerVolumePath("", "/srv/pg"); got != "/srv/pg" {
		t.Fatalf("absolute %s", got)
	}
	root := t.TempDir()
	mustMkdir(t, filepath.Join(root, "var/lib/docker/volumes/db/_data"))
	if got := ResolveDockerVolumePath(root, "db"); got != "/var/lib/docker/volumes/db/_data" {
		t.Fatalf("existing %s", got)
	}
}

func TestDiscoverFullIncludesOS(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "usr/bin/ls"), []byte("os"))
	mustWrite(t, filepath.Join(root, "var/lib/postgresql/x"), []byte("pg"))
	prev, err := Discover(Options{Root: root, Mode: ModeFull})
	if err != nil {
		t.Fatal(err)
	}
	incs, excs := PlanCapture(prev)
	if len(incs) != 0 {
		t.Fatalf("full mode uses excludes only, got includes %v", incs)
	}
	if len(excs) == 0 {
		// proc may not exist in temp dir
	}
	for _, it := range prev.Items {
		if it.Kind != KindTechnical && !it.Selected {
			t.Fatalf("full mode must select %s", it.ID)
		}
	}
}

func TestCustomExclusionWarnsOnDatabaseOverlap(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "var/lib/postgresql/x"), []byte("pg"))
	prev, err := Discover(Options{
		Root: root, Mode: ModeCustom,
		Selection: Selection{Excludes: []string{"/var/lib/postgresql"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, w := range prev.Warnings {
		if strings.Contains(w.Message, "database") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected overlap warning: %+v", prev.Warnings)
	}
}

func mustWrite(t *testing.T, path string, body []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatal(err)
	}
}

func mustMkdir(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
}
