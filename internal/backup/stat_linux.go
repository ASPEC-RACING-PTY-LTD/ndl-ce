//go:build linux

package backup

import (
	"io/fs"
	"syscall"
)

// statMeta extracts owner, inode, and change-time from a Linux stat result.
func statMeta(info fs.FileInfo) (uid int, gid int, inode uint64, ctimeNS int64) {
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok || st == nil {
		return 0, 0, 0, info.ModTime().UnixNano()
	}
	return int(st.Uid), int(st.Gid), st.Ino, st.Ctim.Sec*1_000_000_000 + st.Ctim.Nsec
}
