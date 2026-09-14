//go:build linux

package lxc

import (
	"fmt"
	"os"
	"syscall"
)

func cgroupAllowFromStat(dev string) string {
	st, err := os.Lstat(dev)
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
	return fmt.Sprintf("lxc.cgroup2.devices.allow = c %d:%d rwm\n", maj, min)
}
