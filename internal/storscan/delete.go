package storscan

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// SystemProtected are never deleted, nor anything inside them, nor any
// folder that contains them.
var SystemProtected = []string{
	"/", "/bin", "/boot", "/dev", "/etc", "/lib", "/lib32", "/lib64", "/libx32", "/proc", "/run", "/sbin", "/sys", "/usr",
	"/var/lib/apt", "/var/lib/dpkg", "/var/lib/postgresql", "/var/lib/systemd", "/var/lib/ndl/control", "/var/lib/ndl/keys",
	"/var/lib/ndl/secrets", "/var/lib/ndl/runtime", "/root/.ssh", "/home/*/.ssh",
}

// DeleteResult is the outcome for one item.
type DeleteResult struct {
	Path    string `json:"path"`
	Deleted bool   `json:"deleted"`
	Freed   int64  `json:"freed"`
	Error   string `json:"error,omitempty"`
}

// MountPoints reads the mount points visible to this process.
func MountPoints() ([]string, error) {
	f, err := os.Open("/proc/self/mountinfo")
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []string
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) > 4 {
			out = append(out, unescapeMount(fields[4]))
		}
	}
	return out, sc.Err()
}

func unescapeMount(s string) string {
	return strings.NewReplacer(`\040`, " ", `\011`, "\t", `\012`, "\n", `\134`, `\`).Replace(s)
}

func within(path, dir string) bool {
	return path == dir || dir == "/" || strings.HasPrefix(path, strings.TrimRight(dir, "/")+"/")
}

func protectedBy(abs string, protect []string) string {
	for _, p := range protect {
		matches := []string{p}
		if strings.Contains(p, "*") {
			matches, _ = filepath.Glob(p)
		}
		for _, m := range matches {
			m = filepath.Clean(m)
			if m == "/" {
				if abs == "/" {
					return m
				}
				continue
			}
			if within(abs, m) || within(m, abs) {
				return m
			}
		}
	}
	return ""
}

// Resolve checks that rel names an existing item strictly inside root that
// may be deleted, and returns its absolute path. protect covers whole trees;
// keep names folders that must stay (with their parents) though their
// contents may go, such as a storage pool's root.
func Resolve(root, rel string, protect, mounts []string, keep ...string) (string, error) {
	return resolve(root, rel, SystemProtected, protect, mounts, keep)
}

// OverrideProtected is all an Owner override still refuses: the root and
// the kernel's virtual filesystems.
var OverrideProtected = []string{"/", "/proc", "/sys", "/dev"}

func resolve(root, rel string, system, protect, mounts, keep []string) (string, error) {
	root = filepath.Clean(root)
	if !filepath.IsAbs(root) {
		return "", fmt.Errorf("scan root is invalid")
	}
	if strings.ContainsRune(rel, 0) {
		return "", fmt.Errorf("path is invalid")
	}
	abs := filepath.Join(root, filepath.Clean("/"+rel))
	if abs == root || !within(abs, root) {
		return "", fmt.Errorf("only items inside the scanned folder can be deleted")
	}
	// No folder between root and the item may be a link: the real parent
	// must be exactly the real root plus the same relative path.
	rootReal, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", fmt.Errorf("scan root no longer exists")
	}
	parent, err := filepath.EvalSymlinks(filepath.Dir(abs))
	if err != nil {
		return "", fmt.Errorf("no longer exists")
	}
	relDir, _ := filepath.Rel(root, filepath.Dir(abs))
	if parent != filepath.Join(rootReal, relDir) {
		return "", fmt.Errorf("a folder on the way is a link; refusing to follow it")
	}
	if _, err := os.Lstat(abs); err != nil {
		return "", fmt.Errorf("no longer exists")
	}
	if p := protectedBy(abs, append(append([]string{}, system...), protect...)); p != "" {
		return "", fmt.Errorf("protected: %s holds system or No-dal data", p)
	}
	for _, k := range keep {
		if within(filepath.Clean(k), abs) {
			return "", fmt.Errorf("protected: %s must stay", filepath.Clean(k))
		}
	}
	for _, m := range mounts {
		m = filepath.Clean(m)
		if m != "/" && within(m, abs) {
			return "", fmt.Errorf("a filesystem is mounted at %s; it cannot be deleted from here", m)
		}
	}
	return abs, nil
}

func freeBytes(path string) int64 {
	var s syscall.Statfs_t
	if syscall.Statfs(path, &s) != nil {
		return 0
	}
	return int64(s.Bavail) * int64(s.Bsize)
}

// Delete removes the items after checking each one. Symlinks are removed
// themselves, never followed. One refused item does not stop the others.
func Delete(root string, rels, protect, keep []string, mounts func() ([]string, error)) []DeleteResult {
	return deleteItems(root, rels, SystemProtected, protect, keep, mounts)
}

// DeleteOverride deletes as an Owner who has chosen to override No-dal's
// protection: only the root, the kernel's virtual filesystems, links on the
// way and mounted filesystems are still refused.
func DeleteOverride(root string, rels []string, mounts func() ([]string, error)) []DeleteResult {
	return deleteItems(root, rels, OverrideProtected, nil, nil, mounts)
}

func deleteItems(root string, rels, system, protect, keep []string, mounts func() ([]string, error)) []DeleteResult {
	if mounts == nil {
		mounts = MountPoints
	}
	mps, err := mounts()
	out := make([]DeleteResult, 0, len(rels))
	for _, rel := range rels {
		r := DeleteResult{Path: rel}
		if err != nil {
			r.Error = "could not read mount points: " + err.Error()
			out = append(out, r)
			continue
		}
		abs, rerr := resolve(root, rel, system, protect, mps, keep)
		if rerr != nil {
			r.Error = rerr.Error()
			out = append(out, r)
			continue
		}
		before := freeBytes(root)
		if err := os.RemoveAll(abs); err != nil {
			r.Error = compactErr(err)
		} else {
			r.Deleted = true
		}
		if after := freeBytes(root); after > before {
			r.Freed = after - before
		}
		out = append(out, r)
	}
	return out
}
