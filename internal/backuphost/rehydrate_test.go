package backuphost

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/no-dal/ndl-ce/internal/backup"
)

// fakeGuest answers commands as a guest with Git and Docker would.
type fakeGuest struct {
	root      string
	calls     []string
	cloneOK   map[string]bool // url -> the guest's own credentials work
	images    map[string]bool // present image refs/ids
	pullable  map[string]bool
	tokenSeen string
}

func (g *fakeGuest) exec(_ context.Context, _ string, _ []string, argv ...string) (string, error) {
	line := strings.Join(argv, " ")
	g.calls = append(g.calls, line)
	switch {
	case argv[0] == "docker" && argv[1] == "version":
		return "27.0", nil
	case argv[0] == "docker" && argv[1] == "image":
		if g.images[argv[len(argv)-1]] {
			return "sha256:x", nil
		}
		return "", errors.New("no such image")
	case argv[0] == "docker" && argv[1] == "load":
		return "Loaded", nil
	case argv[0] == "docker" && argv[1] == "pull":
		if g.pullable[argv[len(argv)-1]] {
			g.images[argv[len(argv)-1]] = true
			return "", nil
		}
		return "", errors.New("manifest unknown")
	case argv[0] == "docker":
		return "", nil
	case argv[0] == "sh" && strings.Contains(argv[2], "docker compose build"):
		g.images["app-web"] = true
		return "", nil
	case argv[0] == "sh" && strings.Contains(argv[2], "mv --"):
		dst, src := argv[4], argv[5]
		_ = os.MkdirAll(filepath.Join(g.root, dst), 0o755)
		return "", os.Rename(filepath.Join(g.root, src, ".git"), filepath.Join(g.root, dst, ".git"))
	case argv[0] == "sh":
		return "", nil
	case argv[0] == "git" && slices.Contains(argv, "clone"):
		url, dest := argv[len(argv)-2], argv[len(argv)-1]
		ok := g.cloneOK[url]
		for _, a := range argv {
			if strings.HasPrefix(a, "credential.helper=!") {
				// The token helper reads its file; the test checks it is there.
				name := strings.Fields(a[strings.Index(a, "cat ")+4:])[0]
				name = strings.TrimSuffix(name, ";")
				b, err := os.ReadFile(filepath.Join(g.root, name))
				if err == nil {
					g.tokenSeen = string(b)
					ok = true
				}
			}
		}
		if !ok {
			return "", errors.New("Authentication failed")
		}
		return "", os.MkdirAll(filepath.Join(g.root, dest, ".git"), 0o755)
	case argv[0] == "git" && slices.Contains(argv, "cat-file"):
		if strings.HasPrefix(argv[len(argv)-1], "gone") {
			return "", errors.New("missing")
		}
		return "", nil
	case argv[0] == "git" && slices.Contains(argv, "rev-parse"):
		return "c0ffee", nil
	}
	return "", nil
}

func TestRehydrateClonesAtTheRecordedCommitAndAsksWhenItCannot(t *testing.T) {
	root := t.TempDir()
	g := &fakeGuest{root: root, cloneOK: map[string]bool{"https://git.example.com/me/app.git": true},
		images: map[string]bool{}, pullable: map[string]bool{"postgres@sha256:aaa": true, "redis:7": true}}
	oldExec, oldClone := guestExec, hostClone
	guestExec = g.exec
	hostClone = func(context.Context, string, string) error { return errors.New("no host login") }
	defer func() { guestExec, hostClone = oldExec, oldClone }()
	mustWrite(t, filepath.Join(root, "srv/app/.env"), []byte("K=v"))
	mustWrite(t, filepath.Join(root, KeptImagesDir[1:], keptImageName("sha256:kept000000000000000000000000000000000000")), []byte("tar"))
	_ = os.MkdirAll(filepath.Join(root, "srv/app"), 0o755)

	bp := backup.Blueprint{
		Repos: []backup.GitRepo{
			{Path: "/srv/app", Branch: "main", Commit: "abc123", Remotes: []backup.GitRemote{{Name: "origin", URL: "https://git.example.com/me/app.git"}},
				Dirty: true, Changed: []string{".env", "src/new.go"}, Kept: []string{"/srv/app/.env"}},
			{Path: "/srv/tool", Branch: "dev", Commit: "def456", Unpushed: true, PushedBase: "base789", UnpushedCommits: 3,
				Remotes: []backup.GitRemote{{Name: "origin", URL: "https://git.example.com/me/private.git"}}},
		},
		Docker: &backup.DockerInfo{
			ImagePins: []backup.ImagePin{
				{Ref: "postgres:16", Digest: "postgres@sha256:aaa", ImageID: "sha256:p"},
				{Ref: "redis:7", Digest: "redis@sha256:gone", ImageID: "sha256:r"},
				{Ref: "cache:1", ImageID: "sha256:kept000000000000000000000000000000000000"},
				{Ref: "app-web", ImageID: "sha256:w", Local: true},
			},
			ComposeDirs: []string{"/srv/app"},
		},
	}
	h := &Host{}
	res, err := h.rehydrate(context.Background(), root, Request{WorkloadID: "ct-1", Blueprint: bp})
	if err != nil {
		t.Fatal(err)
	}
	r := res.Rehydrate
	app, tool := r.Repos[0], r.Repos[1]
	if app.Status != RepoCloned || app.Via != "guest" || !slices.Equal(app.NotBackedUp, []string{"src/new.go"}) {
		t.Fatalf("app %+v", app)
	}
	if !slices.Contains(g.calls, "git -c safe.directory=* -C /srv/app checkout -q -f -B main abc123") {
		t.Fatalf("checkout at the recorded commit: %v", g.calls)
	}
	if b, _ := os.ReadFile(filepath.Join(root, "srv/app/.env")); string(b) != "K=v" {
		t.Fatal("the restored .env must stay")
	}
	if tool.Status != RepoNeedsCredentials || !r.NeedsAttention {
		t.Fatalf("a private repo nobody can clone stops and asks: %+v", tool)
	}
	byRef := map[string]ImageRestore{}
	for _, im := range r.Images {
		byRef[im.Ref] = im
	}
	if byRef["postgres:16"].Status != ImagePulled || byRef["redis:7"].Status != ImageNeedsTagApproval ||
		byRef["cache:1"].Status != ImageLoaded || byRef["app-web"].Status != ImageBuilt {
		t.Fatalf("images %+v", r.Images)
	}
	if !slices.Contains(g.calls, "docker pull -q postgres@sha256:aaa") || slices.Contains(g.calls, "docker pull -q redis:7") {
		t.Fatalf("pull by digest, never by tag unasked: %v", g.calls)
	}
	if r.Compose[0].Status != "started" {
		t.Fatalf("compose %+v", r.Compose)
	}

	// The Owner gives a token and approves the tag.
	g.calls = nil
	res, err = h.rehydrate(context.Background(), root, Request{WorkloadID: "ct-1", Blueprint: bp, UseTags: []string{"redis:7"}, RepoTokens: map[string]string{"/srv/tool": "tok-123"}})
	if err != nil {
		t.Fatal(err)
	}
	r = res.Rehydrate
	tool = r.Repos[1]
	if r.Repos[0].Status != RepoPresent || tool.Status != RepoCloned || tool.Via != "token" || g.tokenSeen != "tok-123" {
		t.Fatalf("token clone %+v seen=%q", tool, g.tokenSeen)
	}
	if !strings.Contains(tool.Warning, "3 commit(s)") || !slices.Contains(g.calls, "git -c safe.directory=* -C /srv/tool checkout -q -f -B dev base789") {
		t.Fatalf("unpushed work falls back to the pushed ancestor with a warning: %+v %v", tool, g.calls)
	}
	for _, c := range g.calls {
		if strings.Contains(c, "tok-123") {
			t.Fatalf("the token must not be on a command line: %s", c)
		}
	}
	matches, _ := filepath.Glob(filepath.Join(root, "root/.ndl-git-token-*"))
	if len(matches) != 0 {
		t.Fatal("the token file must be removed")
	}
	for _, im := range r.Images {
		if im.Ref == "redis:7" && im.Status != ImagePulledByTag {
			t.Fatalf("approved tag %+v", im)
		}
	}
}
