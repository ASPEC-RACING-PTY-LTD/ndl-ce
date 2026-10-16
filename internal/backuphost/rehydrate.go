package backuphost

import (
	"context"
	"crypto/rand"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/no-dal/ndl-ce/internal/backup"
)

// Rehydration puts back what Smart left out of a restored workload: Git
// repositories are cloned again at the recorded commit, container images
// come back from a kept copy, by digest, or are rebuilt from the checked-out
// Dockerfile, and compose projects are started. Anything that needs a
// decision (credentials, a tag instead of a missing digest) stops and is
// reported instead of guessed.

// Repo outcomes.
const (
	RepoCloned           = "cloned"
	RepoPresent          = "present"
	RepoNeedsCredentials = "needs_credentials"
	RepoFailed           = "failed"
)

// Image outcomes.
const (
	ImageLoaded           = "loaded"
	ImagePulled           = "pulled"
	ImagePulledByTag      = "pulled_by_tag"
	ImageBuilt            = "built"
	ImageNeedsTagApproval = "needs_tag_approval"
	ImageFailed           = "failed"
)

// RepoRestore is the outcome of one repository.
type RepoRestore struct {
	Path   string `json:"path"`
	Status string `json:"status"`
	// Commit was checked out; Wanted was recorded.
	Commit string `json:"commit,omitempty"`
	Wanted string `json:"wanted,omitempty"`
	Branch string `json:"branch,omitempty"`
	// Via says whose credentials cloned it: guest, host, or token.
	Via     string `json:"via,omitempty"`
	Warning string `json:"warning,omitempty"`
	// NotBackedUp are changes that were neither pushed nor in the backup.
	NotBackedUp     []string `json:"not_backed_up,omitempty"`
	UnpushedCommits int      `json:"unpushed_commits,omitempty"`
	Error           string   `json:"error,omitempty"`
}

// ImageRestore is the outcome of one image.
type ImageRestore struct {
	Ref    string `json:"ref"`
	Digest string `json:"digest,omitempty"`
	Status string `json:"status"`
	Error  string `json:"error,omitempty"`
}

// ComposeRestore is the outcome of one compose project.
type ComposeRestore struct {
	Dir    string `json:"dir"`
	Status string `json:"status"`
	Error  string `json:"error,omitempty"`
}

// RehydrateResult reports a rehydration.
type RehydrateResult struct {
	Repos          []RepoRestore    `json:"repos,omitempty"`
	Images         []ImageRestore   `json:"images,omitempty"`
	Compose        []ComposeRestore `json:"compose,omitempty"`
	NeedsAttention bool             `json:"needs_attention,omitempty"`
}

var randRead = rand.Read

var (
	// guestExec runs a command inside the guest as its root. Tests replace
	// it.
	guestExec = runGuestCommand
	// hostClone clones on the host with the host's CodeHold login. Tests
	// replace it.
	hostClone = runHostClone
)

const rehydrateTimeout = 30 * time.Minute

var (
	dockerWaitTries = 30
	dockerWaitStep  = 3 * time.Second
)

func runGuestCommand(ctx context.Context, guestID string, env []string, argv ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, rehydrateTimeout)
	defer cancel()
	args := []string{"-P", gitSaveLXCPath, "-n", guestID, "--clear-env",
		"--set-var", "HOME=/root", "--set-var", "PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
		"--set-var", "GIT_TERMINAL_PROMPT=0"}
	for _, e := range env {
		args = append(args, "--set-var", e)
	}
	args = append(args, "--")
	out, err := exec.CommandContext(ctx, "/usr/bin/lxc-attach", append(args, argv...)...).CombinedOutput()
	msg := strings.TrimSpace(string(out))
	if err != nil {
		if msg == "" {
			msg = err.Error()
		}
		return msg, fmt.Errorf("%s: %s", argv[0], lastLine(msg))
	}
	return msg, nil
}

// runHostClone clones with ch, which holds the host's CodeHold (and other)
// Git logins. Only https and ssh remotes are allowed.
func runHostClone(ctx context.Context, url, dest string) error {
	ch, err := exec.LookPath("ch")
	if err != nil {
		for _, p := range []string{"/usr/local/bin/ch", "/usr/bin/ch"} {
			if _, serr := os.Stat(p); serr == nil {
				ch = p
				break
			}
		}
	}
	if ch == "" {
		return fmt.Errorf("the CodeHold CLI is not installed on this host")
	}
	ctx, cancel := context.WithTimeout(ctx, rehydrateTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, ch, "clone", "--no-checkout", "--", url, dest)
	cmd.Env = []string{"HOME=/root", "PATH=/usr/local/bin:/usr/bin:/bin", "GIT_TERMINAL_PROMPT=0",
		"GIT_CONFIG_COUNT=2", "GIT_CONFIG_KEY_0=protocol.file.allow", "GIT_CONFIG_VALUE_0=never",
		"GIT_CONFIG_KEY_1=protocol.ext.allow", "GIT_CONFIG_VALUE_1=never"}
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("ch clone: %s", lastLine(string(out)))
	}
	return nil
}

func cloneableURL(u string) bool {
	u = strings.ToLower(strings.TrimSpace(u))
	if strings.HasPrefix(u, "https://") || strings.HasPrefix(u, "ssh://") || strings.HasPrefix(u, "http://") {
		return true
	}
	// scp-like git@host:path
	return strings.Contains(u, "@") && strings.Contains(u, ":") && !strings.Contains(u, "://") && !strings.HasPrefix(u, "-")
}

// rehydrate restores what Smart left out of a running, restored guest.
func (h *Host) rehydrate(ctx context.Context, rootfs string, req Request) (Result, error) {
	id := strings.TrimSpace(req.WorkloadID)
	if !guestIDPattern.MatchString(id) {
		return Result{}, fmt.Errorf("a workload id is required")
	}
	if rootfs == "" || !filepath.IsAbs(rootfs) {
		return Result{}, fmt.Errorf("the workload root is required")
	}
	res := &RehydrateResult{}
	repos := append([]backup.GitRepo(nil), req.Blueprint.Repos...)
	sort.Slice(repos, func(i, j int) bool { return repos[i].Path < repos[j].Path })
	for _, r := range repos {
		out := rehydrateRepo(ctx, rootfs, id, r, req.RepoTokens[r.Path])
		if out.Status == RepoNeedsCredentials || out.Status == RepoFailed {
			res.NeedsAttention = true
		}
		res.Repos = append(res.Repos, out)
	}
	if d := req.Blueprint.Docker; d != nil && (len(d.ImagePins) > 0 || len(d.ComposeDirs) > 0) {
		// Docker comes up with the guest; give it a little time.
		var err error
		for i := 0; i < dockerWaitTries; i++ {
			if _, err = guestExec(ctx, id, nil, "docker", "version", "--format", "{{.Server.Version}}"); err == nil || ctx.Err() != nil {
				break
			}
			time.Sleep(dockerWaitStep)
		}
		if err != nil {
			res.NeedsAttention = true
			res.Compose = append(res.Compose, ComposeRestore{Status: RepoFailed, Error: "Docker is not running in the workload: " + err.Error()})
		} else {
			rehydrateDocker(ctx, rootfs, id, d, req.UseTags, res)
		}
	}
	return Result{Rehydrate: res}, nil
}

func rehydrateRepo(ctx context.Context, rootfs, guestID string, r backup.GitRepo, token string) RepoRestore {
	out := RepoRestore{Path: r.Path, Wanted: r.Commit, Branch: r.Branch, NotBackedUp: r.Unsaved(), UnpushedCommits: r.UnpushedCommits}
	clean := path.Clean("/" + r.Path)
	if clean != r.Path || clean == "/" {
		out.Status, out.Error = RepoFailed, "the repository path is invalid"
		return out
	}
	hostDir := filepath.Join(rootfs, strings.TrimPrefix(clean, "/"))
	if _, err := os.Stat(filepath.Join(hostDir, ".git")); err == nil {
		out.Status = RepoPresent
		return out
	}
	var remote backup.GitRemote
	for _, rm := range r.Remotes {
		if remote.URL == "" || rm.Name == "origin" {
			remote = rm
		}
	}
	if remote.URL == "" || !cloneableURL(remote.URL) {
		out.Status, out.Error = RepoFailed, "the repository has no remote it can be cloned from"
		return out
	}
	want := r.Commit
	if r.Unpushed {
		if r.PushedBase != "" {
			want = r.PushedBase
			out.Warning = fmt.Sprintf("%d commit(s) were on no remote and are lost; checked out the newest pushed ancestor", max(r.UnpushedCommits, 1))
		} else {
			want = ""
			out.Warning = "the recorded commit was never pushed; checked out the branch as it is on the remote"
		}
	}
	tmp := clean + ".ndl-clone"
	hostTmp := filepath.Join(rootfs, strings.TrimPrefix(tmp, "/"))
	_ = os.RemoveAll(hostTmp)
	defer func() { _ = os.RemoveAll(hostTmp) }()
	name := firstNonEmpty(remote.Name, "origin")
	// The guest's own credentials, kept in its backup, first.
	_, err := guestExec(ctx, guestID, nil, "git", "clone", "--no-checkout", "--origin", name, "--", remote.URL, tmp)
	out.Via = "guest"
	if err != nil {
		_ = os.RemoveAll(hostTmp)
		// The host's CodeHold login.
		if herr := hostClone(ctx, remote.URL, hostTmp); herr == nil {
			out.Via = "host"
			err = nil
			if name != "origin" {
				_, _ = guestExec(ctx, guestID, nil, "git", "-c", "safe.directory=*", "-C", tmp, "remote", "rename", "origin", name)
			}
			chownLikeParent(hostTmp)
		} else if token != "" {
			_ = os.RemoveAll(hostTmp)
			// A token the Owner gave for this clone. It is used once, from a
			// private file removed right after, never on a command line.
			err = cloneWithToken(ctx, rootfs, guestID, remote.URL, tmp, name, token)
			out.Via = "token"
		}
	}
	if err != nil {
		out.Status = RepoNeedsCredentials
		out.Via = ""
		out.Error = "no credentials could clone " + remote.URL + "; add them in the workload, or give a token for this clone"
		return out
	}
	gitIn := func(args ...string) (string, error) {
		return guestExec(ctx, guestID, nil, append([]string{"git", "-c", "safe.directory=*", "-C", clean}, args...)...)
	}
	if _, err := guestExec(ctx, guestID, nil, "sh", "-c", `mkdir -p -- "$1" && mv -- "$2/.git" "$1/.git"`, "sh", clean, tmp); err != nil {
		out.Status, out.Error = RepoFailed, err.Error()
		return out
	}
	if want != "" {
		if _, err := gitIn("cat-file", "-e", want+"^{commit}"); err != nil {
			if r.PushedBase != "" && want != r.PushedBase {
				want = r.PushedBase
				out.Warning = "the recorded commit is not on the remote any more; checked out the newest pushed ancestor"
			} else {
				want = ""
				out.Warning = "the recorded commit is not on the remote; checked out the branch as it is on the remote"
			}
		}
	}
	if want == "" {
		if r.Branch == "" {
			out.Status, out.Error = RepoFailed, "neither the commit nor a branch to check out is on the remote"
			return out
		}
		want = name + "/" + r.Branch
	}
	var args []string
	if r.Branch != "" {
		args = []string{"checkout", "-q", "-f", "-B", r.Branch, want}
	} else {
		args = []string{"checkout", "-q", "-f", "--detach", want}
	}
	if _, err := gitIn(args...); err != nil {
		out.Status, out.Error = RepoFailed, err.Error()
		return out
	}
	if r.Branch != "" {
		_, _ = gitIn("branch", "-q", "--set-upstream-to="+name+"/"+r.Branch)
	}
	if c, err := gitIn("rev-parse", "HEAD"); err == nil {
		out.Commit = strings.TrimSpace(c)
	}
	out.Status = RepoCloned
	return out
}

// chownLikeParent gives a tree cloned on the host the owner of the
// directory it was cloned into, so it belongs to the guest's user.
func chownLikeParent(dir string) {
	st, err := os.Stat(filepath.Dir(dir))
	if err != nil {
		return
	}
	sys, ok := st.Sys().(*syscall.Stat_t)
	if !ok {
		return
	}
	_ = filepath.Walk(dir, func(p string, _ os.FileInfo, err error) error {
		if err == nil {
			_ = os.Lchown(p, int(sys.Uid), int(sys.Gid))
		}
		return nil
	})
}

func rehydrateDocker(ctx context.Context, rootfs, guestID string, d *backup.DockerInfo, useTags []string, res *RehydrateResult) {
	approved := map[string]bool{}
	for _, t := range useTags {
		approved[t] = true
	}
	seen := map[string]bool{}
	var local []backup.ImagePin
	for _, p := range d.ImagePins {
		key := firstNonEmpty(p.ImageID, p.Ref)
		if seen[key] {
			continue
		}
		seen[key] = true
		out := ImageRestore{Ref: p.Ref, Digest: p.Digest}
		if _, err := guestExec(ctx, guestID, nil, "docker", "image", "inspect", "--format", "{{.Id}}", firstNonEmpty(p.ImageID, p.Ref)); err == nil {
			out.Status = ImageLoaded
			res.Images = append(res.Images, out)
			continue
		}
		kept := path.Join(KeptImagesDir, keptImageName(p.ImageID))
		if p.ImageID != "" {
			if st, err := os.Stat(filepath.Join(rootfs, strings.TrimPrefix(kept, "/"))); err == nil && st.Size() > 0 {
				if _, err := guestExec(ctx, guestID, nil, "docker", "load", "-q", "-i", kept); err == nil {
					out.Status = ImageLoaded
					res.Images = append(res.Images, out)
					continue
				}
			}
		}
		switch {
		case p.Digest != "":
			if _, err := guestExec(ctx, guestID, nil, "docker", "pull", "-q", p.Digest); err == nil {
				if p.Ref != "" && p.Ref != p.Digest {
					_, _ = guestExec(ctx, guestID, nil, "docker", "tag", p.Digest, p.Ref)
				}
				out.Status = ImagePulled
			} else if approved[p.Ref] {
				if _, err := guestExec(ctx, guestID, nil, "docker", "pull", "-q", p.Ref); err == nil {
					out.Status = ImagePulledByTag
				} else {
					out.Status, out.Error = ImageFailed, err.Error()
				}
			} else {
				out.Status = ImageNeedsTagApproval
				out.Error = "the exact image " + p.Digest + " is no longer available; approve using the tag " + p.Ref + " instead"
			}
		case p.Local:
			local = append(local, p)
			continue
		default:
			if _, err := guestExec(ctx, guestID, nil, "docker", "pull", "-q", p.Ref); err == nil {
				out.Status = ImagePulled
			} else {
				out.Status, out.Error = ImageFailed, err.Error()
			}
		}
		if out.Status == ImageNeedsTagApproval || out.Status == ImageFailed {
			res.NeedsAttention = true
		}
		res.Images = append(res.Images, out)
	}
	dirs := append([]string(nil), d.ComposeDirs...)
	sort.Strings(dirs)
	// Locally built images are built again from the Dockerfiles of the
	// checked-out commit.
	if len(local) > 0 {
		for _, dir := range dirs {
			if _, err := os.Stat(filepath.Join(rootfs, strings.TrimPrefix(dir, "/"))); err != nil {
				continue
			}
			_, _ = guestExec(ctx, guestID, nil, "sh", "-c", `cd -- "$1" && docker compose build`, "sh", dir)
		}
		for _, p := range local {
			out := ImageRestore{Ref: p.Ref, Status: ImageBuilt}
			if _, err := guestExec(ctx, guestID, nil, "docker", "image", "inspect", "--format", "{{.Id}}", p.Ref); err != nil {
				out.Status = ImageFailed
				out.Error = "it was built locally and could not be rebuilt from its compose project"
				res.NeedsAttention = true
			}
			res.Images = append(res.Images, out)
		}
	}
	for _, dir := range dirs {
		out := ComposeRestore{Dir: dir, Status: "started"}
		if _, err := os.Stat(filepath.Join(rootfs, strings.TrimPrefix(dir, "/"))); err != nil {
			out.Status, out.Error = RepoFailed, "the project directory is missing"
			res.NeedsAttention = true
		} else if _, err := guestExec(ctx, guestID, nil, "sh", "-c", `cd -- "$1" && docker compose up -d --no-build --pull never`, "sh", dir); err != nil {
			out.Status, out.Error = RepoFailed, err.Error()
			res.NeedsAttention = true
		}
		res.Compose = append(res.Compose, out)
	}
}

func cloneWithToken(ctx context.Context, rootfs, guestID, url, dest, remote, token string) error {
	b := make([]byte, 8)
	if _, err := randRead(b); err != nil {
		return err
	}
	name := fmt.Sprintf("/root/.ndl-git-token-%x", b)
	host := filepath.Join(rootfs, strings.TrimPrefix(name, "/"))
	if err := os.MkdirAll(filepath.Dir(host), 0o700); err != nil {
		return err
	}
	if err := os.WriteFile(host, []byte(token), 0o600); err != nil {
		return err
	}
	defer func() { _ = os.Remove(host) }()
	chownLikeParent(host)
	helper := `!f() { echo username=x-access-token; printf "password="; cat ` + name + `; echo; }; f`
	_, err := guestExec(ctx, guestID, nil, "git", "-c", "credential.helper=", "-c", "credential.helper="+helper,
		"clone", "--no-checkout", "--origin", remote, "--", url, dest)
	return err
}
