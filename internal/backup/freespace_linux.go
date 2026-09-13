//go:build linux

package backup

import "syscall"

// hostFreeBytes returns the free bytes available to an unprivileged process on
// the filesystem backing path.
func hostFreeBytes(path string) (int64, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return 0, err
	}
	return int64(st.Bavail) * int64(st.Bsize), nil
}
