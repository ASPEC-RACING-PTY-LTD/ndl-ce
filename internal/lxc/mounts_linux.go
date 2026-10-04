//go:build linux

package lxc

import (
	"os"
	"syscall"
)

func deviceOf(st os.FileInfo) uint64 {
	if sys, ok := st.Sys().(*syscall.Stat_t); ok {
		return uint64(sys.Dev)
	}
	return 0
}
