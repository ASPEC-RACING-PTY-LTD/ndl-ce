package ctbackup

import (
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

// AvailableBytes reports free bytes on the filesystem of path.
func AvailableBytes(path string) (int64, error) {
	if path == "" {
		return 0, fmt.Errorf("statfs path is required")
	}
	if _, err := os.Stat(path); err != nil {
		if !os.IsNotExist(err) {
			return 0, err
		}
		if err := os.MkdirAll(path, 0o750); err != nil {
			return 0, err
		}
	}
	var st unix.Statfs_t
	if err := unix.Statfs(path, &st); err != nil {
		return 0, fmt.Errorf("statfs: %w", err)
	}
	return int64(st.Bavail) * int64(st.Bsize), nil
}
