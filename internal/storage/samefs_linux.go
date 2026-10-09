//go:build linux

package storage

import (
	"os"
	"path/filepath"
	"syscall"
)

// SameFilesystem reports whether a and b live on the same filesystem. A path
// that does not exist yet is judged by its nearest existing parent.
func SameFilesystem(a, b string) (bool, error) {
	da, err := deviceOf(a)
	if err != nil {
		return false, err
	}
	db, err := deviceOf(b)
	if err != nil {
		return false, err
	}
	return da == db, nil
}

func deviceOf(p string) (uint64, error) {
	p = filepath.Clean(p)
	for {
		var st syscall.Stat_t
		err := syscall.Stat(p, &st)
		if err == nil {
			return uint64(st.Dev), nil
		}
		if !os.IsNotExist(err) {
			return 0, err
		}
		parent := filepath.Dir(p)
		if parent == p {
			return 0, err
		}
		p = parent
	}
}
