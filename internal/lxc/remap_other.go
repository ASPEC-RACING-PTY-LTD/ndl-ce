//go:build !linux

package lxc

import (
	"os"
	"path/filepath"
)

func chownMappedRoot(string, int, int) error { return nil }

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
		return os.Lchown(p, uidBase, gidBase)
	})
}
