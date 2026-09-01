//go:build linux

package storage

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"golang.org/x/sys/unix"
)

func readMountsLive() (string, error) {
	b, err := os.ReadFile("/proc/self/mountinfo")
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func statFSLive(path string) (FSStat, error) {
	var st unix.Statfs_t
	if err := unix.Statfs(path, &st); err != nil {
		return FSStat{}, err
	}
	var stat unix.Stat_t
	dev := uint64(0)
	if err := unix.Stat(path, &stat); err == nil {
		dev = uint64(stat.Dev)
	}
	return FSStat{
		BlockSize:   int64(st.Bsize),
		Blocks:      uint64(st.Blocks),
		BlocksFree:  uint64(st.Bfree),
		BlocksAvail: uint64(st.Bavail),
		Dev:         dev,
	}, nil
}

func evalLive(p string) (string, error) {
	resolved, err := filepath.EvalSymlinks(p)
	if err != nil {
		return resolveExistingPrefix(p)
	}
	return filepath.ToSlash(resolved), nil
}

func resolveExistingPrefix(p string) (string, error) {
	cur := p
	for cur != "/" && cur != "." {
		if resolved, err := filepath.EvalSymlinks(cur); err == nil {
			rest := strings.TrimPrefix(p, cur)
			return filepath.ToSlash(filepath.Join(resolved, filepath.FromSlash(rest))), nil
		}
		next := filepath.Dir(cur)
		if next == cur {
			break
		}
		cur = next
	}
	return filepath.ToSlash(p), nil
}

func lookupUUIDLive(device string) string {
	if device == "" {
		return ""
	}
	ents, err := os.ReadDir("/dev/disk/by-uuid")
	if err != nil {
		return ""
	}
	want, err := filepath.EvalSymlinks(device)
	if err != nil {
		want = device
	}
	want = filepath.Clean(want)
	for _, e := range ents {
		target, err := filepath.EvalSymlinks(filepath.Join("/dev/disk/by-uuid", e.Name()))
		if err != nil {
			continue
		}
		if filepath.Clean(target) == want {
			return e.Name()
		}
	}
	return ""
}

func setXattrLive(path, name, value string) error {
	return unix.Setxattr(path, name, []byte(value), 0)
}

func getXattrLive(path, name string) (string, error) {
	sz, err := unix.Getxattr(path, name, nil)
	if err != nil {
		return "", err
	}
	buf := make([]byte, sz)
	n, err := unix.Getxattr(path, name, buf)
	if err != nil {
		return "", err
	}
	return string(buf[:n]), nil
}

func classifyXattrErr(err error) string {
	if err == nil {
		return XattrOK
	}
	if errors.Is(err, unix.ENOTSUP) || errors.Is(err, unix.EOPNOTSUPP) {
		return XattrUnsupported
	}
	if errors.Is(err, unix.ENODATA) || errors.Is(err, unix.ENOENT) {
		return XattrMissing
	}
	if errors.Is(err, unix.EACCES) || errors.Is(err, unix.EPERM) {
		return XattrInaccessible
	}
	return XattrInaccessible
}

func allocatedLive(path string) (int64, error) {
	st, err := os.Stat(path)
	if err != nil {
		return 0, err
	}
	if sys, ok := st.Sys().(*syscall.Stat_t); ok {
		return sys.Blocks * 512, nil
	}
	return st.Size(), nil
}

func walkSizeLive(root string) (allocated, logical int64, err error) {
	err = filepath.WalkDir(root, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		if d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		logical += info.Size()
		if sys, ok := info.Sys().(*syscall.Stat_t); ok {
			allocated += sys.Blocks * 512
		} else {
			allocated += info.Size()
		}
		return nil
	})
	return allocated, logical, err
}
