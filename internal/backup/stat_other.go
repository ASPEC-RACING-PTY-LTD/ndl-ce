//go:build !linux

package backup

import "io/fs"

// statMeta falls back to portable metadata on non-Linux platforms. Inode and
// change-time are unavailable, so the cache is more conservative (more rereads).
func statMeta(info fs.FileInfo) (uid int, gid int, inode uint64, ctimeNS int64) {
	return 0, 0, 0, info.ModTime().UnixNano()
}
