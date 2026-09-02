//go:build !linux

package iojail

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// OpenBeneath is the portable jail used in unit tests. Production Linux
// uses openat2.
func OpenBeneath(root, rel string, flags int, perm os.FileMode) (*os.File, string, error) {
	abs, err := joinUnder(root, rel)
	if err != nil {
		return nil, "", err
	}
	if err := deniedHost(root, abs); err != nil {
		return nil, "", err
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		if !os.IsNotExist(err) || flags&os.O_CREATE == 0 {
			if os.IsNotExist(err) && flags&os.O_CREATE == 0 {
				return nil, "", err
			}
			if !os.IsNotExist(err) {
				return nil, "", err
			}
			resolved = abs
		} else {
			parent := filepath.Dir(abs)
			pref, perr := filepath.EvalSymlinks(parent)
			if perr != nil {
				return nil, "", perr
			}
			resolved = filepath.Join(pref, filepath.Base(abs))
		}
	}
	rootAbs, err := filepath.EvalSymlinks(root)
	if err != nil {
		rootAbs = filepath.Clean(root)
	}
	relOut, err := filepath.Rel(rootAbs, resolved)
	if err != nil || relOut == ".." || strings.HasPrefix(filepath.ToSlash(relOut), "../") {
		return nil, "", fmt.Errorf("path escapes the jail")
	}
	if err := deniedHost(root, resolved); err != nil {
		return nil, "", err
	}
	f, err := os.OpenFile(abs, flags, perm)
	if err != nil {
		return nil, "", err
	}
	return f, resolved, nil
}
