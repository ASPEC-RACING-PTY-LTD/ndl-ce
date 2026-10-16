package backuphost

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func gitIn(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir, "-c", "user.email=t@example.com", "-c", "user.name=t"}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v %s", args, err, out)
	}
}

func TestCaptureIsHeldForUncommittedGitWorkOnlyWhenAsked(t *testing.T) {
	h, err := Open(Options{Root: t.TempDir(), Settings: Settings{MaxLocalBytes: 1 << 30, MinHostFreeBytes: 1}})
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()
	root := t.TempDir()
	repo := filepath.Join(root, "root/app")
	mustWrite(t, filepath.Join(repo, "main.go"), []byte("package main"))
	mustWrite(t, filepath.Join(repo, ".gitignore"), []byte(".env\n"))
	gitIn(t, repo, "init", "-q", "-b", "main")
	gitIn(t, repo, "add", ".")
	gitIn(t, repo, "commit", "-q", "-m", "init")
	gitIn(t, repo, "remote", "add", "origin", "https://git.example.com/me/app.git")
	gitIn(t, repo, "update-ref", "refs/remotes/origin/main", "HEAD")
	mustWrite(t, filepath.Join(repo, ".env"), []byte("K=never-read"))
	mustWrite(t, filepath.Join(root, "var/lib/app/state.json"), []byte("{}"))

	capture := func(hold bool) Result {
		t.Helper()
		res, err := h.Handle(context.Background(), ActionCapture, root, mustJSON(Request{
			Action: ActionCapture, WorkloadID: "ct-1", WorkloadName: "app", CaptureMode: "smart", HoldUncommittedGit: hold,
		}))
		if err != nil {
			t.Fatal(err)
		}
		var out Result
		_ = json.Unmarshal([]byte(res.Extra), &out)
		return out
	}
	if out := capture(true); len(out.GitHold) != 0 || out.BackupID == "" {
		t.Fatalf("a clean, pushed repository with only a kept .env must not hold: %+v", out.GitHold)
	}
	mustWrite(t, filepath.Join(repo, "main.go"), []byte("package main // edited"))
	out := capture(true)
	if len(out.GitHold) != 1 || out.BackupID != "" || !slices.Equal(out.GitHold[0].Unsaved(), []string{"main.go"}) {
		t.Fatalf("dirty repository must hold the capture: %+v", out)
	}
	if out := capture(false); len(out.GitHold) != 0 || out.BackupID == "" {
		t.Fatalf("ignoring is the default: %+v", out.GitHold)
	}
}

func TestGitSaveCommitsTrackedWorkAndLeavesSecretsOut(t *testing.T) {
	root := t.TempDir()
	repo := filepath.Join(root, "root/app")
	mustWrite(t, filepath.Join(repo, "main.go"), []byte("package main"))
	gitIn(t, repo, "init", "-q", "-b", "main")
	gitIn(t, repo, "add", ".")
	gitIn(t, repo, "commit", "-q", "-m", "init")
	for _, f := range []string{"notes.md", ".env", "certs/server.key", "data/app.sqlite", "api_token.txt"} {
		mustWrite(t, filepath.Join(repo, f), []byte("x"))
	}
	if err := os.WriteFile(filepath.Join(repo, "big.bin"), make([]byte, gitSaveMaxFileBytes+1), 0o644); err != nil {
		t.Fatal(err)
	}
	var calls [][]string
	old := guestGit
	guestGit = func(_ context.Context, id, dir string, args ...string) (string, error) {
		if id != "ct-1" || dir != "/root/app" {
			t.Fatalf("guest %s dir %s", id, dir)
		}
		calls = append(calls, args)
		switch args[0] {
		case "diff":
			return "", os.ErrInvalid // staged changes exist
		case "config":
			return "t@example.com", nil
		case "symbolic-ref":
			return "main", nil
		case "rev-parse":
			return strings.Repeat("b", 40), nil
		}
		return "", nil
	}
	defer func() { guestGit = old }()

	h := &Host{}
	res, err := h.gitSave(context.Background(), root, Request{WorkloadID: "ct-1", Repos: []string{"/root/app"}})
	if err != nil {
		t.Fatal(err)
	}
	got := res.GitSave[0]
	if got.Error != "" || !got.Committed || !got.Pushed {
		t.Fatalf("save %+v", got)
	}
	var added []string
	for _, c := range calls {
		if c[0] == "add" && c[1] == "--" {
			added = append(added, c[2:]...)
		}
	}
	if !slices.Equal(added, []string{"notes.md"}) {
		t.Fatalf("only non-secret, small new files are added: %v", added)
	}
	slices.Sort(got.Skipped)
	if !slices.Equal(got.Skipped, []string{".env", "api_token.txt", "big.bin", "certs/server.key", "data/app.sqlite"}) {
		t.Fatalf("skipped %v", got.Skipped)
	}
	if bad, err := h.gitSave(context.Background(), root, Request{WorkloadID: "ct-1", Repos: []string{"/root/../etc"}}); err != nil || bad.GitSave[0].Error == "" {
		t.Fatalf("a path that is not clean must be refused: %+v", bad)
	}
}
