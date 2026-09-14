package iojail

import (
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
)

func splitRel(rel string) (parent, base string) {
	rel, err := cleanRel(rel)
	if err != nil {
		return ".", "."
	}
	if rel == "." {
		return ".", "."
	}
	parent = path.Dir(rel)
	base = path.Base(rel)
	if parent == "" || parent == "/" {
		parent = "."
	}
	return parent, base
}

// RemoveBeneath deletes rel under root without following symlinks.
// The jail root itself cannot be deleted.
func RemoveBeneath(root, rel string) error {
	rel, err := cleanRel(rel)
	if err != nil {
		return err
	}
	if rel == "." {
		return fmt.Errorf("cannot delete the jail root")
	}
	if planned, jerr := joinUnder(root, rel); jerr == nil {
		if err := coversDeniedHost(root, planned); err != nil {
			return err
		}
	}
	return removeBeneath(root, rel)
}

// MkdirBeneath creates a directory under root.
func MkdirBeneath(root, rel string, mode fs.FileMode) error {
	rel, err := cleanRel(rel)
	if err != nil {
		return err
	}
	if rel == "." {
		return fmt.Errorf("mkdir path is required")
	}
	parent, base := splitRel(rel)
	if base == "." || base == "" {
		return fmt.Errorf("mkdir path is required")
	}
	if planned, jerr := joinUnder(root, rel); jerr == nil {
		if err := deniedHost(root, planned); err != nil {
			return err
		}
	}
	return mkdirBeneath(root, parent, base, mode)
}

// RenameBeneath moves src to dest under the same jail without following dest escapes.
func RenameBeneath(root, src, dest string) error {
	src, err := cleanRel(src)
	if err != nil {
		return err
	}
	dest, err = cleanRel(dest)
	if err != nil {
		return err
	}
	if src == "." || dest == "." {
		return fmt.Errorf("cannot rename the jail root")
	}
	if planned, jerr := joinUnder(root, src); jerr == nil {
		if err := coversDeniedHost(root, planned); err != nil {
			return err
		}
	}
	if planned, jerr := joinUnder(root, dest); jerr == nil {
		if err := coversDeniedHost(root, planned); err != nil {
			return err
		}
	}
	return renameBeneath(root, src, dest)
}

// ChmodBeneath sets permission bits under root without following the
// final symlink.
func ChmodBeneath(root, rel string, mode fs.FileMode) error {
	rel, err := cleanRel(rel)
	if err != nil {
		return err
	}
	if rel == "." {
		return fmt.Errorf("chmod path is required")
	}
	if planned, jerr := joinUnder(root, rel); jerr == nil {
		if err := deniedHost(root, planned); err != nil {
			return err
		}
	}
	return chmodBeneath(root, rel, mode)
}

// ChownBeneath sets uid/gid under root without following the final symlink.
// uid or gid of -1 leaves that id unchanged.
func ChownBeneath(root, rel string, uid, gid int) error {
	rel, err := cleanRel(rel)
	if err != nil {
		return err
	}
	if rel == "." {
		return fmt.Errorf("chown path is required")
	}
	if planned, jerr := joinUnder(root, rel); jerr == nil {
		if err := deniedHost(root, planned); err != nil {
			return err
		}
	}
	return chownBeneath(root, rel, uid, gid)
}

func removePortable(root, rel string) error {
	abs, err := joinUnder(root, rel)
	if err != nil {
		return err
	}
	if err := deniedHost(root, abs); err != nil {
		return err
	}
	info, err := os.Lstat(abs)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return os.Remove(abs)
	}
	entries, err := os.ReadDir(abs)
	if err != nil {
		return err
	}
	for _, e := range entries {
		child := path.Join(rel, e.Name())
		if err := removePortable(root, child); err != nil {
			return err
		}
	}
	return os.Remove(abs)
}

func mkdirPortable(root, parent, base string, mode fs.FileMode) error {
	parentAbs, err := joinUnder(root, parent)
	if err != nil {
		if parent == "." {
			parentAbs = filepath.Clean(root)
		} else {
			return err
		}
	}
	if err := deniedHost(root, parentAbs); err != nil {
		return err
	}
	info, err := os.Lstat(parentAbs)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return fmt.Errorf("mkdir parent is not a directory")
	}
	err = os.Mkdir(filepath.Join(parentAbs, base), mode)
	return mkdirExistOK(root, parent, base, err)
}

func mkdirExistOK(root, parent, base string, err error) error {
	if err == nil {
		return nil
	}
	if !os.IsExist(err) {
		return err
	}
	rel := base
	if parent != "." {
		rel = path.Join(parent, base)
	}
	f, _, oerr := OpenBeneath(root, rel, os.O_RDONLY, 0)
	if oerr != nil {
		return err
	}
	info, sterr := f.Stat()
	_ = f.Close()
	if sterr != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("path exists and is not a directory")
	}
	return nil
}

func renamePortable(root, src, dest string) error {
	srcAbs, err := joinUnder(root, src)
	if err != nil {
		return err
	}
	destAbs, err := joinUnder(root, dest)
	if err != nil {
		return err
	}
	if err := deniedHost(root, srcAbs); err != nil {
		return err
	}
	if err := deniedHost(root, destAbs); err != nil {
		return err
	}
	if info, err := os.Lstat(destAbs); err == nil {
		if info.IsDir() {
			return fmt.Errorf("destination exists")
		}
		if err := os.Remove(destAbs); err != nil {
			return err
		}
	}
	return os.Rename(srcAbs, destAbs)
}

func chmodPortable(root, rel string, mode fs.FileMode) error {
	abs, err := joinUnder(root, rel)
	if err != nil {
		return err
	}
	if err := deniedHost(root, abs); err != nil {
		return err
	}
	info, err := os.Lstat(abs)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("refusing to chmod a symlink")
	}
	return os.Chmod(abs, mode.Perm())
}

func chownPortable(root, rel string, uid, gid int) error {
	abs, err := joinUnder(root, rel)
	if err != nil {
		return err
	}
	if err := deniedHost(root, abs); err != nil {
		return err
	}
	info, err := os.Lstat(abs)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("refusing to chown a symlink")
	}
	return os.Chown(abs, uid, gid)
}
