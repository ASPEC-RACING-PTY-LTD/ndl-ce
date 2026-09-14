//go:build unix

package iojail

import (
	"os"
	"os/user"
	"strconv"
	"syscall"
)

func fillOwner(meta *FileMeta, info os.FileInfo) {
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok || st == nil {
		return
	}
	meta.UID = uint32(st.Uid)
	meta.GID = uint32(st.Gid)
	if u, err := user.LookupId(strconv.FormatUint(uint64(st.Uid), 10)); err == nil {
		meta.Owner = u.Username
	}
	if g, err := user.LookupGroupId(strconv.FormatUint(uint64(st.Gid), 10)); err == nil {
		meta.Group = g.Name
	}
}
