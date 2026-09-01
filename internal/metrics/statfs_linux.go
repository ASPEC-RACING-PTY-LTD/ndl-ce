//go:build linux

package metrics

import "syscall"

func statfsAvail(path string) (uint64, bool) {
	if path == "" {
		return 0, false
	}
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return 0, false
	}
	return st.Bavail * uint64(st.Bsize), true
}
