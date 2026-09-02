//go:build linux

package iojail

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"

	"golang.org/x/sys/unix"
)

// OpenBeneath opens rel under root using openat2 RESOLVE_BENEATH.
func OpenBeneath(root, rel string, flags int, perm os.FileMode) (*os.File, string, error) {
	rel, err := cleanRel(rel)
	if err != nil {
		return nil, "", err
	}
	root = filepath.Clean(root)
	if planned, jerr := joinUnder(root, rel); jerr == nil {
		if err := deniedHost(root, planned); err != nil {
			return nil, "", err
		}
	}
	dir, err := os.OpenFile(root, unix.O_PATH|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, "", fmt.Errorf("jail root: %w", err)
	}
	defer dir.Close()
	how := unix.OpenHow{
		Flags:   uint64(flags) | unix.O_CLOEXEC,
		Mode:    uint64(perm.Perm()),
		Resolve: unix.RESOLVE_BENEATH | unix.RESOLVE_NO_MAGICLINKS,
	}
	if flags&os.O_WRONLY != 0 || flags&os.O_RDWR != 0 || flags&os.O_CREATE != 0 {
		how.Flags |= unix.O_NOFOLLOW
		how.Resolve |= unix.RESOLVE_NO_SYMLINKS
	}
	name := rel
	if name == "." {
		name = "."
	}
	fd, err := unix.Openat2(int(dir.Fd()), name, &how)
	if err != nil {
		return nil, "", fmt.Errorf("openat2: %w", err)
	}
	f := os.NewFile(uintptr(fd), filepath.Join(root, filepath.FromSlash(rel)))
	abs, err := filepath.Abs(f.Name())
	if err != nil {
		_ = f.Close()
		return nil, "", err
	}
	if err := deniedHost(root, abs); err != nil {
		_ = f.Close()
		return nil, "", err
	}
	return f, abs, nil
}

func init() {
	_ = syscall.O_RDONLY
}
