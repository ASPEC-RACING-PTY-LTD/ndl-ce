package agentrpc

import (
	"context"
	"encoding/json"
	"fmt"
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

// relocateBackupRepo points the agent at a new repository directory, for
// example on a large storage pool instead of the root disk. It never moves or
// deletes backup data: it refuses while the current repository holds restore
// points or is busy, so nothing can be stranded. The repository key is kept
// outside both directories and is reused.
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
	if n := host.RestorePoints(); n > 0 {
		return backuphost.Result{}, fmt.Errorf("the current repository at %s holds %d restore point(s); expire them or move the directory yourself before changing its location, so no backup is stranded", cur, n)
	}
	if !host.Idle() {
		return backuphost.Result{}, fmt.Errorf("a backup, upload or cleanup is running; try again when it finishes")
	}
	if entries, err := os.ReadDir(dest); err == nil && len(entries) > 0 {
		if _, err := os.Stat(filepath.Join(dest, "packs")); err != nil {
			return backuphost.Result{}, fmt.Errorf("%s is not empty and is not a backup repository", dest)
		}
	}
	if err := os.MkdirAll(dest, 0o750); err != nil {
		return backuphost.Result{}, err
	}
	// Carry remembered targets so pending remote cleanup keeps working.
	if src := filepath.Join(cur, "state", "targets"); dirExists(src) {
		_ = copyFlatDir(src, filepath.Join(dest, "state", "targets"))
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
	host.Close()
	h.mu.Lock()
	h.BackupHost = nil
	h.mu.Unlock()
	next, err := h.backupHost()
	if err != nil {
		return backuphost.Result{}, err
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
