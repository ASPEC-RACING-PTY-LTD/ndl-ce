//go:build !linux

package lxc

import "os"

func deviceOf(os.FileInfo) uint64 { return 0 }
