package backup

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// inRoot returns the host path of rel inside root, resolving symlinks that
// already exist there as if root were "/": an absolute link is taken from
// root, and ".." never climbs above it. A restore into a tree that already
// holds a base image (whose links point at /usr, /run and so on) therefore
// never writes outside the tree. With followLast unset the last element is
// not resolved, so a link there can be replaced rather than written through.
func inRoot(root, rel string, followLast bool) (string, error) {
	pending := splitPath(rel)
	cur := ""
	links := 0
	for len(pending) > 0 {
		p := pending[0]
		pending = pending[1:]
		switch p {
		case "", ".":
			continue
		case "..":
			cur = path.Dir("/" + cur)[1:]
			continue
		}
		next := path.Join(cur, p)
		if len(pending) == 0 && !followLast {
			cur = next
			break
		}
		fi, err := os.Lstat(filepath.Join(root, filepath.FromSlash(next)))
		if err != nil || fi.Mode()&os.ModeSymlink == 0 {
			cur = next
			continue
		}
		links++
		if links > 64 {
			return "", fmt.Errorf("too many links resolving %s", rel)
		}
		target, err := os.Readlink(filepath.Join(root, filepath.FromSlash(next)))
		if err != nil {
			return "", err
		}
		if path.IsAbs(target) {
			cur = ""
		}
		pending = append(splitPath(target), pending...)
	}
	return filepath.Join(root, filepath.FromSlash(cur)), nil
}

func splitPath(p string) []string {
	return strings.Split(filepath.ToSlash(p), "/")
}
