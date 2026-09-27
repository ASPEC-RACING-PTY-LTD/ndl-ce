//go:build linux

package lxc

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// cgroupRuleFromStat reads the host node's major:minor. Symlinks such as
// /dev/dri/by-path/* resolve to the node lxc.mount.entry will bind, and must
// stay inside /dev.
func cgroupRuleFromStat(dev string) string {
	resolved, err := filepath.EvalSymlinks(dev)
	if err != nil || !strings.HasPrefix(resolved, "/dev/") {
		return ""
	}
	st, err := os.Lstat(resolved)
	if err != nil {
		return ""
	}
	if st.Mode()&os.ModeCharDevice == 0 {
		return ""
	}
	sys, ok := st.Sys().(*syscall.Stat_t)
	if !ok {
		return ""
	}
	rdev := uint64(sys.Rdev)
	maj := unixMajor(rdev)
	min := unixMinor(rdev)
	if maj == 0 && min == 0 {
		return ""
	}
	return fmt.Sprintf("c %d:%d rwm", maj, min)
}
