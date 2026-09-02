//go:build !linux

package iojail

import "io/fs"

func removeBeneath(root, rel string) error {
	return removePortable(root, rel)
}

func mkdirBeneath(root, parent, base string, mode fs.FileMode) error {
	return mkdirPortable(root, parent, base, mode)
}

func renameBeneath(root, src, dest string) error {
	return renamePortable(root, src, dest)
}
