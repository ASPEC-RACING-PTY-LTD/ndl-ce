package agentrpc

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/no-dal/ndl-ce/internal/backuphost"
)

func (h *Handler) agentDataDir() string {
	if h.Ident.Dir != "" {
		return h.Ident.Dir
	}
	return "/var/lib/ndl"
}

// backupRepoPathFile records a repository moved off the default location.
func (h *Handler) backupRepoPathFile() string {
	return filepath.Join(h.agentDataDir(), "agent", "backup-repo.path")
}

// backupRepoRoot is the configured repository directory, or the default.
func (h *Handler) backupRepoRoot() string {
	if raw, err := os.ReadFile(h.backupRepoPathFile()); err == nil {
		if p := strings.TrimSpace(string(raw)); filepath.IsAbs(p) {
			return filepath.Clean(p)
		}
	}
	return filepath.Join(h.agentDataDir(), "backup-repo")
}

// backupKeyPath keeps the repository key outside the repository directory.
func (h *Handler) backupKeyPath() string {
	return filepath.Join(h.agentDataDir(), "keys", "backup-master.key")
}

func (h *Handler) backupHost() (*backuphost.Host, error) {
	h.mu.Lock()
	if h.backupMoving {
		h.mu.Unlock()
		return nil, fmt.Errorf("the backup repository is being moved; try again when the move finishes")
	}
	if h.BackupHost != nil {
		host := h.BackupHost
		h.mu.Unlock()
		return host, nil
	}
	h.mu.Unlock()
	host, err := backuphost.Open(backuphost.Options{
		Root:        h.backupRepoRoot(),
		Docker:      h.docker(),
		KeyPath:     h.backupKeyPath(),
		ProtectHost: true,
	})
	if err != nil {
		return nil, err
	}
	h.mu.Lock()
	if h.BackupHost == nil {
		h.BackupHost = host
	} else {
		host = h.BackupHost
	}
	h.mu.Unlock()
	return host, nil
}

// relocateBackupRepo moves the backup repository to a new directory, for
// example a dataset on a large storage pool instead of the root disk. While
// it runs, no backup can open the repository. A repository that holds data is
// copied file by file, the copy is checked (same files, same sizes, same
// restore points once reopened), and only then does the agent switch to it and
// remove the old directory. If anything fails before the switch, the old
// repository stays in use untouched. The repository key is kept outside both
// directories and is reused.
func (h *Handler) relocateBackupRepo(ctx context.Context, raw string) (backuphost.Result, error) {
	var req backuphost.Request
	if err := json.Unmarshal([]byte(raw), &req); err != nil {
		return backuphost.Result{}, fmt.Errorf("relocate request: %w", err)
	}
	dest := strings.TrimSpace(req.Path)
	if !filepath.IsAbs(dest) {
		return backuphost.Result{}, fmt.Errorf("the backup repository path must be absolute")
	}
	dest = filepath.Clean(dest)
	if dest == "/" {
		return backuphost.Result{}, fmt.Errorf("/ cannot hold the backup repository")
	}
	for _, bad := range []string{"/proc", "/sys", "/dev", "/run", "/boot", "/etc", "/usr", "/bin", "/sbin", "/lib"} {
		if isUnder(dest, bad) {
			return backuphost.Result{}, fmt.Errorf("%s cannot hold the backup repository", dest)
		}
	}
	if _, err := os.Stat(filepath.Dir(dest)); err != nil {
		return backuphost.Result{}, fmt.Errorf("the parent directory of %s does not exist", dest)
	}
	host, err := h.backupHost()
	if err != nil {
		return backuphost.Result{}, err
	}
	cur := filepath.Clean(host.Root())
	if dest == cur {
		return backuphost.Result{Locator: cur}, nil
	}
	if isUnder(dest, cur) || isUnder(cur, dest) {
		return backuphost.Result{}, fmt.Errorf("the new repository cannot be inside the current one or contain it")
	}
	points := host.RestorePoints()
	if !host.Idle() {
		return backuphost.Result{}, fmt.Errorf("a backup, upload or cleanup is running; try again when it finishes")
	}
	if entries, err := os.ReadDir(dest); err == nil && len(entries) > 0 {
		if _, err := os.Stat(filepath.Join(dest, "packs")); err != nil || points > 0 {
			return backuphost.Result{}, fmt.Errorf("%s is not empty; choose an empty location", dest)
		}
	}
	if err := os.MkdirAll(dest, 0o750); err != nil {
		return backuphost.Result{}, err
	}
	// Nothing may open the repository while it is copied.
	h.mu.Lock()
	h.backupMoving = true
	h.BackupHost = nil
	h.mu.Unlock()
	host.Close()
	moved := false
	defer func() {
		h.mu.Lock()
		h.backupMoving = false
		h.mu.Unlock()
		if !moved {
			// Back to the old repository; the partial copy is not used.
			_ = os.RemoveAll(filepath.Join(dest, "packs"))
			_ = os.RemoveAll(filepath.Join(dest, "snapshots"))
			_ = os.RemoveAll(filepath.Join(dest, "state"))
			_ = os.RemoveAll(filepath.Join(dest, "cache"))
		}
	}()
	files, bytes, err := copyTree(ctx, cur, dest)
	if err != nil {
		return backuphost.Result{}, fmt.Errorf("copying the repository failed, nothing was changed: %w", err)
	}
	gotFiles, gotBytes := treeSize(dest)
	if gotFiles < files || gotBytes < bytes {
		return backuphost.Result{}, fmt.Errorf("the copy is incomplete (%d of %d files, %d of %d bytes); nothing was changed", gotFiles, files, gotBytes, bytes)
	}
	if err := os.MkdirAll(filepath.Dir(h.backupRepoPathFile()), 0o750); err != nil {
		return backuphost.Result{}, err
	}
	tmp := h.backupRepoPathFile() + ".tmp"
	if err := os.WriteFile(tmp, []byte(dest+"\n"), 0o640); err != nil {
		return backuphost.Result{}, err
	}
	if err := os.Rename(tmp, h.backupRepoPathFile()); err != nil {
		return backuphost.Result{}, err
	}
	h.mu.Lock()
	h.backupMoving = false
	h.mu.Unlock()
	next, err := h.backupHost()
	if err != nil {
		_ = os.WriteFile(h.backupRepoPathFile(), []byte(cur+"\n"), 0o640)
		return backuphost.Result{}, fmt.Errorf("the copied repository could not be opened, so the old one stays in use: %w", err)
	}
	if got := next.RestorePoints(); got < points {
		next.Close()
		h.mu.Lock()
		h.BackupHost = nil
		h.mu.Unlock()
		_ = os.WriteFile(h.backupRepoPathFile(), []byte(cur+"\n"), 0o640)
		return backuphost.Result{}, fmt.Errorf("the copy shows %d of %d restore points, so the old repository stays in use", got, points)
	}
	moved = true
	// The copy is verified and in use: free the old location.
	if points > 0 || files > 0 {
		_ = os.RemoveAll(cur)
	}
	res, err := next.Handle(ctx, backuphost.ActionStatus, "", "{}")
	if err != nil {
		return backuphost.Result{Locator: dest}, nil
	}
	var out backuphost.Result
	_ = json.Unmarshal([]byte(res.Extra), &out)
	out.Locator = dest
	return out, nil
}

// copyTree copies every regular file under src to the same place under dst
// and returns how many files and bytes it copied. Temporary files are
// skipped.
func copyTree(ctx context.Context, src, dst string) (files int, bytes int64, err error) {
	err = filepath.WalkDir(src, func(p string, d os.DirEntry, werr error) error {
		if werr != nil {
			return werr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o750)
		}
		if !d.Type().IsRegular() || strings.HasSuffix(d.Name(), ".tmp") {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		n, err := copyFile(p, target, info.Mode().Perm())
		if err != nil {
			return err
		}
		files++
		bytes += n
		return nil
	})
	return files, bytes, err
}

func copyFile(src, dst string, mode os.FileMode) (int64, error) {
	in, err := os.Open(src)
	if err != nil {
		return 0, err
	}
	defer in.Close()
	tmp := dst + ".copy"
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
	if err != nil {
		return 0, err
	}
	n, err := io.Copy(out, in)
	if err == nil {
		err = out.Sync()
	}
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		_ = os.Remove(tmp)
		return 0, err
	}
	return n, os.Rename(tmp, dst)
}

// treeSize counts the regular files and bytes under dir.
func treeSize(dir string) (files int, bytes int64) {
	_ = filepath.WalkDir(dir, func(_ string, d os.DirEntry, err error) error {
		if err != nil || !d.Type().IsRegular() {
			return nil
		}
		if info, err := d.Info(); err == nil {
			files++
			bytes += info.Size()
		}
		return nil
	})
	return files, bytes
}

// isUnder reports whether p is dir or below it.
func isUnder(p, dir string) bool {
	return p == dir || strings.HasPrefix(p, strings.TrimSuffix(dir, "/")+"/")
}

func dirExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.IsDir()
}

func copyFlatDir(src, dst string) error {
	if err := os.MkdirAll(dst, 0o700); err != nil {
		return err
	}
	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(src, e.Name()))
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(dst, e.Name()), raw, 0o600); err != nil {
			return err
		}
	}
	return nil
}
