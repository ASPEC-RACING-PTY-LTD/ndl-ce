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

func chmodBeneath(root, rel string, mode fs.FileMode) error {
	return chmodPortable(root, rel, mode)
}

func chownBeneath(root, rel string, uid, gid int) error {
	return chownPortable(root, rel, uid, gid)
}
