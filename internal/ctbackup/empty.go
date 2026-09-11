package ctbackup

import (
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

func emptySameFS(root string) error {
	info, err := os.Lstat(root)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("container rootfs must be a directory")
	}
	rootDev, err := fileDev(root)
	if err != nil {
		return err
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if e.Name() == "lost+found" {
			continue
		}
		child := filepath.Join(root, e.Name())
		dev, err := fileDev(child)
		if err != nil {
			return err
		}
		if dev != rootDev {
			continue
		}
		if err := os.RemoveAll(child); err != nil {
			return fmt.Errorf("clear rootfs: %w", err)
		}
	}
	return nil
}

func fileDev(path string) (uint64, error) {
	var st unix.Stat_t
	if err := unix.Lstat(path, &st); err != nil {
		return 0, err
	}
	return st.Dev, nil
}
