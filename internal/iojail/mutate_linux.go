//go:build linux

package iojail

import (
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"

	"golang.org/x/sys/unix"
)

func removeBeneath(root, rel string) error {
	parent, base := splitRel(rel)
	pfd, err := openDirBeneath(root, parent)
	if err != nil {
		return err
	}
	defer pfd.Close()
	return unlinkTree(int(pfd.Fd()), base)
}

func unlinkTree(parentFd int, name string) error {
	if name == "" || name == "." || name == ".." {
		return fmt.Errorf("invalid delete name")
	}
	err := unix.Unlinkat(parentFd, name, 0)
	if err == nil {
		return nil
	}
	childFd, oerr := unix.Openat(parentFd, name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if oerr != nil {
		return err
	}
	f := os.NewFile(uintptr(childFd), name)
	names, readErr := f.Readdirnames(-1)
	if readErr != nil {
		_ = f.Close()
		return readErr
	}
	for _, child := range names {
		if err := unlinkTree(childFd, child); err != nil {
			_ = f.Close()
			return err
		}
	}
	if err := f.Close(); err != nil {
		return err
	}
	return unix.Unlinkat(parentFd, name, unix.AT_REMOVEDIR)
}

func mkdirBeneath(root, parent, base string, mode fs.FileMode) error {
	if base == "" || base == "." || base == ".." {
		return fmt.Errorf("mkdir path is required")
	}
	pfd, err := openDirBeneath(root, parent)
	if err != nil {
		return err
	}
	defer pfd.Close()
	err = unix.Mkdirat(int(pfd.Fd()), base, uint32(mode.Perm()))
	if err == nil || err == unix.EEXIST {
		return mkdirExistOK(root, parent, base, err)
	}
	return err
}

func renameBeneath(root, src, dest string) error {
	srcParent, srcBase := splitRel(src)
	destParent, destBase := splitRel(dest)
	if srcBase == "" || srcBase == "." || destBase == "" || destBase == "." {
		return fmt.Errorf("cannot rename the jail root")
	}
	sfd, err := openDirBeneath(root, srcParent)
	if err != nil {
		return err
	}
	defer sfd.Close()
	dfd, err := openDirBeneath(root, destParent)
	if err != nil {
		return err
	}
	defer dfd.Close()
	return unix.Renameat(int(sfd.Fd()), srcBase, int(dfd.Fd()), destBase)
}

func chmodBeneath(root, rel string, mode fs.FileMode) error {
	parent, base := splitRel(rel)
	if base == "" || base == "." || base == ".." {
		return fmt.Errorf("chmod path is required")
	}
	pfd, err := openDirBeneath(root, parent)
	if err != nil {
		return err
	}
	defer pfd.Close()
	return unix.Fchmodat(int(pfd.Fd()), base, uint32(mode.Perm()), unix.AT_SYMLINK_NOFOLLOW)
}

func chownBeneath(root, rel string, uid, gid int) error {
	parent, base := splitRel(rel)
	if base == "" || base == "." || base == ".." {
		return fmt.Errorf("chown path is required")
	}
	pfd, err := openDirBeneath(root, parent)
	if err != nil {
		return err
	}
	defer pfd.Close()
	return unix.Fchownat(int(pfd.Fd()), base, uid, gid, unix.AT_SYMLINK_NOFOLLOW)
}

func openDirBeneath(root, rel string) (*os.File, error) {
	rel, err := cleanRel(rel)
	if err != nil {
		return nil, err
	}
	root = filepath.Clean(root)
	if planned, jerr := joinUnder(root, rel); jerr == nil {
		if err := deniedHost(root, planned); err != nil {
			return nil, err
		}
	}
	dir, err := os.OpenFile(root, unix.O_PATH|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, fmt.Errorf("jail root: %w", err)
	}
	if rel == "." {
		return dir, nil
	}
	fd, err := unix.Openat2(int(dir.Fd()), rel, &unix.OpenHow{
		Flags:   uint64(unix.O_RDONLY | unix.O_DIRECTORY | unix.O_CLOEXEC | unix.O_NOFOLLOW),
		Resolve: unix.RESOLVE_BENEATH | unix.RESOLVE_NO_MAGICLINKS | unix.RESOLVE_NO_SYMLINKS,
	})
	_ = dir.Close()
	if err != nil {
		return nil, fmt.Errorf("openat2: %w", err)
	}
	return os.NewFile(uintptr(fd), path.Join(root, rel)), nil
}
