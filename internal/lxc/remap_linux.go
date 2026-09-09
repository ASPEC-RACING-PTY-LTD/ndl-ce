//go:build linux

package lxc

import (
	"os"
	"path/filepath"
	"syscall"
)

// shiftRootfs adds the host map base to inodes that still have namespace UIDs.
// Files already extracted through lxc-usernsexec (uid >= base) are left alone.
// Guest network files written as host root afterwards are shifted to mapped root.
func shiftRootfs(rootfs string, uidBase, gidBase int) error {
	if uidBase < 1 {
		uidBase = 100000
	}
	if gidBase < 1 {
		gidBase = 100000
	}
	return filepath.Walk(rootfs, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		st, ok := info.Sys().(*syscall.Stat_t)
		if !ok {
			return nil
		}
		uid := int(st.Uid)
		gid := int(st.Gid)
		newUID, newGID := uid, gid
		if uid < uidBase {
			newUID = uid + uidBase
		}
		if gid < gidBase {
			newGID = gid + gidBase
		}
		if newUID == uid && newGID == gid {
			return nil
		}
		return os.Lchown(p, newUID, newGID)
	})
}
