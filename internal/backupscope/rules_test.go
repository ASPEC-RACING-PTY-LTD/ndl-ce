package backupscope

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/no-dal/ndl-ce/internal/backup"
)

func sparse(t *testing.T, path string, size int64) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(size); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
}

func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir, "-c", "user.email=t@example.com", "-c", "user.name=t"}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v %s", args, err, out)
	}
}

func itemByID(prev Preview, id string) *Item {
	for i := range prev.Items {
		if prev.Items[i].ID == id {
			return &prev.Items[i]
		}
	}
	return nil
}

func TestSmartLeavesOutRebuildableData(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "var/lib/containerd/io.containerd.content.v1.content/blobs/sha256/aa"), []byte("layer"))
	mustWrite(t, filepath.Join(root, "var/lib/docker/overlay2/x/diff/bin/sh"), []byte("layer"))
	mustWrite(t, filepath.Join(root, "var/lib/docker/buildkit/metadata_v2.db"), []byte("bk"))
	mustWrite(t, filepath.Join(root, "var/lib/docker/volumes/app_data/_data/db.sqlite"), []byte("db"))
	mustWrite(t, filepath.Join(root, "var/lib/docker/volumes/app_data/_data/cache/transcode/a.ts"), []byte("tc"))
	mustWrite(t, filepath.Join(root, "var/lib/myservice/state.json"), []byte("{}"))
	mustWrite(t, filepath.Join(root, "root/.rustup/toolchains/x/bin/rustc"), []byte("rust"))
	mustWrite(t, filepath.Join(root, "root/go/pkg/mod/x/y.go"), []byte("go"))
	mustWrite(t, filepath.Join(root, "root/app/cache/jellyfin-media/a.media"), []byte("cache"))
	sparse(t, filepath.Join(root, "srv/media/movies/film.mkv"), 900<<20)
	mustWrite(t, filepath.Join(root, "srv/media/movies/film.nfo"), []byte("nfo"))
	sparse(t, filepath.Join(root, "srv/inbox/big.flac"), 300<<20)
	sparse(t, filepath.Join(root, "srv/inbox/notes.zip"), 600<<20)
	for _, job := range []string{"a", "b", "c", "d", "e"} {
		sparse(t, filepath.Join(root, "srv/app/inbox/jobs", job, "track.m4a"), 15<<20)
		mustWrite(t, filepath.Join(root, "srv/app/inbox/jobs", job, "job.json"), []byte("{}"))
	}
	mustWrite(t, filepath.Join(root, "srv/app/settings.json"), []byte("{}"))
	mustWrite(t, filepath.Join(root, "game/RSDragonwilds/Content/Paks/a.pak"), []byte("pak"))
	mustWrite(t, filepath.Join(root, "game/RSDragonwilds/Saved/save.sav"), []byte("save"))

	prev, err := Discover(Options{Root: root, Mode: ModeSmart, Docker: &DockerHint{NamedVolumes: []string{"app_data"}}})
	if err != nil {
		t.Fatal(err)
	}
	incs, excs := PlanCapture(prev)
	captured := func(rel string) bool { return backup.Captured(root, rel, incs, excs) }
	for _, rel := range []string{
		"var/lib/docker/volumes/app_data/_data/db.sqlite",
		"var/lib/myservice/state.json",
		"game/RSDragonwilds/Saved/save.sav",
		"srv/app/settings.json",
	} {
		if !captured(rel) {
			t.Errorf("%s must be kept", rel)
		}
	}
	for _, rel := range []string{
		"var/lib/containerd/io.containerd.content.v1.content/blobs/sha256/aa",
		"var/lib/docker/overlay2/x/diff/bin/sh",
		"var/lib/docker/buildkit/metadata_v2.db",
		"var/lib/docker/volumes/app_data/_data/cache/transcode/a.ts",
		"root/.rustup/toolchains/x/bin/rustc",
		"root/go/pkg/mod/x/y.go",
		"root/app/cache/jellyfin-media/a.media",
		"srv/media/movies/film.mkv",
		"srv/media/movies/film.nfo", // goes with its media folder
		"srv/inbox/big.flac",
		"game/RSDragonwilds/Content/Paks/a.pak",
		"srv/app/inbox/jobs/a/track.m4a",
		"srv/app/inbox/jobs/a/job.json",
	} {
		if captured(rel) {
			t.Errorf("%s must be left out", rel)
		}
	}
	if captured("srv/inbox/notes.zip") == false {
		t.Error("a non-media file next to a big media file stays")
	}
	if it := itemByID(prev, "media:srv/media"); it == nil || it.Kind != KindMedia || it.Selected {
		t.Fatalf("a folder of video is media and off: %+v", it)
	}
}

func TestSmartKeepsOnlyUntrackedConfigInGitTrees(t *testing.T) {
	root := t.TempDir()
	repo := filepath.Join(root, "root/viewdock")
	mustWrite(t, filepath.Join(repo, "compose.yml"), []byte("services: {}"))
	mustWrite(t, filepath.Join(repo, "src/main.go"), []byte("package main"))
	mustWrite(t, filepath.Join(repo, ".gitignore"), []byte(".env\nconfig/\ndist/\nnotes.txt\n"))
	git(t, repo, "init", "-q", "-b", "main")
	git(t, repo, "add", ".")
	git(t, repo, "commit", "-q", "-m", "init")
	git(t, repo, "remote", "add", "origin", "https://user:s3cret@git.example.com/me/viewdock.git")
	mustWrite(t, filepath.Join(repo, ".env"), []byte("TOKEN=never-read"))
	mustWrite(t, filepath.Join(repo, "config/viewdock.db"), []byte("db"))
	mustWrite(t, filepath.Join(repo, "config/settings.xml"), []byte("<x/>"))
	mustWrite(t, filepath.Join(repo, "dist/bundle.json"), []byte("{}"))
	mustWrite(t, filepath.Join(repo, "notes.txt"), []byte("scratch"))
	mustWrite(t, filepath.Join(repo, "src/main.go"), []byte("package main // edited"))

	prev, err := Discover(Options{Root: root, Mode: ModeSmart, Docker: &DockerHint{BindMounts: []string{"/root/viewdock"}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(prev.Repos) != 1 {
		t.Fatalf("repos %+v", prev.Repos)
	}
	r := prev.Repos[0]
	if r.Path != "/root/viewdock" || r.Branch != "main" || len(r.Commit) != 40 || !r.Dirty || !r.Unpushed {
		t.Fatalf("repo record %+v", r)
	}
	if len(r.Remotes) != 1 || strings.Contains(r.Remotes[0].URL, "s3cret") || strings.Contains(r.Remotes[0].URL, "user") {
		t.Fatalf("remote URL must carry no credentials: %+v", r.Remotes)
	}
	if !slices.Contains(r.Changed, "src/main.go") {
		t.Fatalf("changed paths %v", r.Changed)
	}
	want := []string{"/root/viewdock/.env", "/root/viewdock/config/settings.xml", "/root/viewdock/config/viewdock.db"}
	if !slices.Equal(r.Kept, want) {
		t.Fatalf("kept %v, want %v", r.Kept, want)
	}
	incs, excs := PlanCapture(prev)
	for _, f := range want {
		if !backup.Captured(root, f, incs, excs) {
			t.Errorf("%s must be captured", f)
		}
	}
	for _, f := range []string{"/root/viewdock/compose.yml", "/root/viewdock/src/main.go", "/root/viewdock/notes.txt", "/root/viewdock/dist/bundle.json"} {
		if backup.Captured(root, f, incs, excs) {
			t.Errorf("%s must be left to Git", f)
		}
	}
}

func TestSafeRemoteURL(t *testing.T) {
	for in, want := range map[string]string{
		"https://x-access-token:abc@github.com/o/r.git": "https://github.com/o/r.git",
		"git@github.com:o/r.git":                        "git@github.com:o/r.git",
		"ssh://git@host:22/o/r":                         "ssh://host:22/o/r",
	} {
		if got := SafeRemoteURL(in); got != want {
			t.Errorf("%s: %s, want %s", in, got, want)
		}
	}
}
