package backuphost

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/no-dal/ndl-ce/internal/backupscope"
)

// gitSaveMaxFileBytes leaves large untracked files out of an automatic
// commit; they are reported instead.
const gitSaveMaxFileBytes = 10 << 20

var (
	gitSaveLXCPath = "/var/lib/ndl/runtime/lxc"
	gitSaveTimeout = 2 * time.Minute
	guestIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$`)
	// guestGit runs git inside the guest, as the guest's root, with the
	// guest's own Git configuration and credentials. Tests replace it.
	guestGit = runGuestGit
)

func runGuestGit(ctx context.Context, guestID, repo string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, gitSaveTimeout)
	defer cancel()
	argv := []string{"-P", gitSaveLXCPath, "-n", guestID, "--clear-env",
		"--set-var", "HOME=/root", "--set-var", "PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
		"--set-var", "GIT_TERMINAL_PROMPT=0", "--",
		"git", "-c", "safe.directory=*", "-C", repo}
	out, err := exec.CommandContext(ctx, "/usr/bin/lxc-attach", append(argv, args...)...).CombinedOutput()
	msg := strings.TrimSpace(string(out))
	if err != nil {
		if msg == "" {
			msg = err.Error()
		}
		return msg, fmt.Errorf("git %s: %s", args[0], lastLine(msg))
	}
	return msg, nil
}

func lastLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.LastIndex(s, "\n"); i >= 0 {
		return s[i+1:]
	}
	return s
}

// gitSave commits and pushes the uncommitted work of the listed repositories
// from inside the running guest. Tracked changes are staged with add -u;
// untracked files are added only when they are not secret-like and not
// large. File contents are never read here. Each repository is reported on
// its own; one failing does not stop the others.
func (h *Host) gitSave(ctx context.Context, source string, req Request) (Result, error) {
	id := strings.TrimSpace(req.WorkloadID)
	if !guestIDPattern.MatchString(id) {
		return Result{}, fmt.Errorf("a workload id is required")
	}
	if len(req.Repos) == 0 {
		return Result{}, fmt.Errorf("no repositories were named")
	}
	out := Result{WorkloadID: id}
	for _, repo := range req.Repos {
		out.GitSave = append(out.GitSave, saveRepo(ctx, source, id, repo))
	}
	return out, nil
}

func saveRepo(ctx context.Context, source, guestID, repo string) GitSaveResult {
	res := GitSaveResult{Path: repo}
	clean := path.Clean("/" + strings.TrimSpace(repo))
	if clean != repo || clean == "/" {
		res.Error = "the repository path is invalid"
		return res
	}
	if source != "" {
		if st, err := os.Stat(filepath.Join(source, clean, ".git")); err != nil || st == nil {
			res.Error = "this is not a Git repository"
			return res
		}
	}
	if _, err := guestGit(ctx, guestID, clean, "add", "-u"); err != nil {
		res.Error = err.Error()
		return res
	}
	var add []string
	if source != "" {
		files, err := backupscope.UntrackedFiles(source, clean)
		if err != nil {
			res.Error = "could not list new files"
			return res
		}
		for _, f := range files {
			st, err := os.Lstat(filepath.Join(source, clean, f))
			switch {
			case err != nil || !st.Mode().IsRegular() && st.Mode()&os.ModeSymlink == 0:
				res.Skipped = append(res.Skipped, f)
			case backupscope.SecretLike(f), st.Size() > gitSaveMaxFileBytes:
				res.Skipped = append(res.Skipped, f)
			default:
				add = append(add, f)
			}
		}
	}
	for len(add) > 0 {
		n := min(len(add), 200)
		if _, err := guestGit(ctx, guestID, clean, append([]string{"add", "--"}, add[:n]...)...); err != nil {
			res.Error = err.Error()
			return res
		}
		add = add[n:]
	}
	if _, err := guestGit(ctx, guestID, clean, "diff", "--cached", "--quiet"); err != nil {
		args := []string{}
		if email, _ := guestGit(ctx, guestID, clean, "config", "user.email"); strings.TrimSpace(email) == "" {
			args = append(args, "-c", "user.name=NDL-CE", "-c", "user.email=ndl-ce@localhost")
		}
		args = append(args, "commit", "-q", "-m", "Save uncommitted work before backup (NDL-CE)")
		if _, err := guestGit(ctx, guestID, clean, args...); err != nil {
			res.Error = err.Error()
			return res
		}
		res.Committed = true
	}
	if c, err := guestGit(ctx, guestID, clean, "rev-parse", "HEAD"); err == nil {
		res.Commit = strings.TrimSpace(c)
	}
	branch, err := guestGit(ctx, guestID, clean, "symbolic-ref", "--short", "-q", "HEAD")
	if err != nil || strings.TrimSpace(branch) == "" {
		res.Error = "the repository is not on a branch (detached HEAD); push it yourself"
		return res
	}
	if _, err := guestGit(ctx, guestID, clean, "rev-parse", "--abbrev-ref", "--symbolic-full-name", "@{u}"); err == nil {
		_, err = guestGit(ctx, guestID, clean, "push", "-q")
		if err != nil {
			res.Error = err.Error()
			return res
		}
	} else {
		remotes, _ := guestGit(ctx, guestID, clean, "remote")
		remote := ""
		for _, r := range strings.Fields(remotes) {
			if remote == "" || r == "origin" {
				remote = r
			}
		}
		if remote == "" {
			res.Error = "the repository has no remote to push to"
			return res
		}
		if _, err := guestGit(ctx, guestID, clean, "push", "-q", "-u", remote, "HEAD"); err != nil {
			res.Error = err.Error()
			return res
		}
	}
	res.Pushed = true
	return res
}
