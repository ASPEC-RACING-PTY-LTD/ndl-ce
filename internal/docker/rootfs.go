package docker

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"strings"

	"golang.org/x/sys/unix"
)

// A system container's files are reached through /proc/<pid>/root. Paths
// below it are resolved with RESOLVE_IN_ROOT, so a symlink inside the
// container (even an absolute one) can never lead to a file on the host.

const maxRootFile = 1 << 20

func openRoot(root string) (int, error) {
	return unix.Open(root, unix.O_PATH|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
}

func cleanRel(p string) (string, error) {
	if !strings.HasPrefix(p, "/") {
		return "", fmt.Errorf("path %q is not absolute", p)
	}
	c := path.Clean(p)
	if c == "/" {
		return ".", nil
	}
	return strings.TrimPrefix(c, "/"), nil
}

func openInRoot(root, p string, flags uint64) (int, error) {
	rel, err := cleanRel(p)
	if err != nil {
		return -1, err
	}
	rfd, err := openRoot(root)
	if err != nil {
		return -1, err
	}
	defer unix.Close(rfd)
	return unix.Openat2(rfd, rel, &unix.OpenHow{
		Flags:   flags | unix.O_CLOEXEC,
		Resolve: unix.RESOLVE_IN_ROOT | unix.RESOLVE_NO_MAGICLINKS,
	})
}

// readInRoot reads a regular file inside root.
func readInRoot(root, p string) ([]byte, error) {
	fd, err := openInRoot(root, p, unix.O_RDONLY|unix.O_NONBLOCK)
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(fd), p)
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !st.Mode().IsRegular() {
		return nil, fmt.Errorf("%s is not a regular file", p)
	}
	return io.ReadAll(io.LimitReader(f, maxRootFile))
}

// existsInRoot reports whether p exists inside root.
func existsInRoot(root, p string) bool {
	fd, err := openInRoot(root, p, unix.O_PATH)
	if err != nil {
		return false
	}
	_ = unix.Close(fd)
	return true
}

// listInRoot lists the names in a directory inside root.
func listInRoot(root, dir string) ([]string, error) {
	fd, err := openInRoot(root, dir, unix.O_RDONLY|unix.O_DIRECTORY)
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(fd), dir)
	defer f.Close()
	return f.Readdirnames(4096)
}

// dirInRoot opens dir inside root, creating missing directories owned by the
// container's root user when create is set.
func dirInRoot(root, dir string, create bool) (int, error) {
	rel, err := cleanRel(dir)
	if err != nil {
		return -1, err
	}
	uid, gid := rootOwner(root)
	cur := "/"
	if rel == "." {
		return openInRoot(root, "/", unix.O_PATH|unix.O_DIRECTORY)
	}
	var fd int = -1
	for _, part := range strings.Split(rel, "/") {
		next := path.Join(cur, part)
		nfd, err := openInRoot(root, next, unix.O_PATH|unix.O_DIRECTORY)
		if errors.Is(err, unix.ENOENT) && create {
			pfd, perr := openInRoot(root, cur, unix.O_PATH|unix.O_DIRECTORY)
			if perr != nil {
				return -1, perr
			}
			if merr := unix.Mkdirat(pfd, part, 0o755); merr != nil && !errors.Is(merr, unix.EEXIST) {
				_ = unix.Close(pfd)
				return -1, merr
			}
			_ = unix.Fchownat(pfd, part, uid, gid, unix.AT_SYMLINK_NOFOLLOW)
			_ = unix.Close(pfd)
			nfd, err = openInRoot(root, next, unix.O_PATH|unix.O_DIRECTORY)
		}
		if err != nil {
			return -1, err
		}
		if fd >= 0 {
			_ = unix.Close(fd)
		}
		fd, cur = nfd, next
	}
	return fd, nil
}

// writeInRoot replaces a file inside root atomically. The file is owned by
// the container's root user.
func writeInRoot(root, p string, data []byte, mode os.FileMode) error {
	dfd, err := dirInRoot(root, path.Dir(p), true)
	if err != nil {
		return err
	}
	defer unix.Close(dfd)
	name := path.Base(p)
	tmp := "." + name + ".ndl-tmp"
	_ = unix.Unlinkat(dfd, tmp, 0)
	// O_PATH directory fds cannot create files; reopen it for writing.
	wfd, err := unix.Openat(dfd, ".", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return err
	}
	defer unix.Close(wfd)
	fd, err := unix.Openat(wfd, tmp, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC, uint32(mode.Perm()))
	if err != nil {
		return err
	}
	f := os.NewFile(uintptr(fd), tmp)
	uid, gid := rootOwner(root)
	_, werr := f.Write(data)
	if werr == nil {
		werr = f.Chmod(mode.Perm())
	}
	if werr == nil {
		werr = f.Chown(uid, gid)
	}
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	if werr != nil {
		_ = unix.Unlinkat(wfd, tmp, 0)
		return werr
	}
	return unix.Renameat(wfd, tmp, wfd, name)
}

// removeInRoot deletes a file inside root. A missing file is not an error.
func removeInRoot(root, p string) error {
	dfd, err := dirInRoot(root, path.Dir(p), false)
	if errors.Is(err, unix.ENOENT) {
		return nil
	}
	if err != nil {
		return err
	}
	defer unix.Close(dfd)
	wfd, err := unix.Openat(dfd, ".", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return err
	}
	defer unix.Close(wfd)
	if err := unix.Unlinkat(wfd, path.Base(p), 0); err != nil && !errors.Is(err, unix.ENOENT) {
		return err
	}
	return nil
}
