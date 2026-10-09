//go:build linux

package diskguard

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

func statPath(path string) (statInfo, error) {
	var fs unix.Statfs_t
	if err := unix.Statfs(path, &fs); err != nil {
		return statInfo{}, err
	}
	var st unix.Stat_t
	if err := unix.Stat(path, &st); err != nil {
		return statInfo{}, err
	}
	bs := int64(fs.Bsize)
	return statInfo{device: uint64(st.Dev), total: int64(fs.Blocks) * bs, avail: int64(fs.Bavail) * bs}, nil
}

// preallocate makes path hold size bytes of real blocks without writing
// them. Blocks it already holds are kept. Filesystems without fallocate
// (ZFS, some network mounts) are reported as unsupported rather than filled
// with zeros, which compression would undo.
func preallocate(path string, size int64) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := unix.Fallocate(int(f.Fd()), 0, 0, size); err != nil {
		if errors.Is(err, unix.EOPNOTSUPP) || errors.Is(err, unix.ENOSYS) {
			return errReserveUnsupported
		}
		return err
	}
	return f.Sync()
}

func deviceOf(fi os.FileInfo) uint64 {
	if st, ok := fi.Sys().(*unix.Stat_t); ok {
		return uint64(st.Dev)
	}
	return 0
}

// allocatedBytes is the space a file really uses, so sparse disk images
// count by what they hold rather than their apparent size.
func allocatedBytes(fi os.FileInfo) int64 {
	if st, ok := fi.Sys().(*unix.Stat_t); ok {
		return int64(st.Blocks) * 512
	}
	return fi.Size()
}
