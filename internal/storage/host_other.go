//go:build !linux

package storage

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
)

var errHostLinuxOnly = errors.New("directory storage host operations require Linux")

func readMountsLive() (string, error) { return "", errHostLinuxOnly }

func statFSLive(string) (FSStat, error) { return FSStat{}, errHostLinuxOnly }

func evalLive(p string) (string, error) {
	resolved, err := filepath.EvalSymlinks(p)
	if err != nil {
		return filepath.ToSlash(p), nil
	}
	return filepath.ToSlash(resolved), nil
}

func lookupUUIDLive(string) string { return "" }

func setXattrLive(string, string, string) error { return unixENOTSUP }

func getXattrLive(string, string) (string, error) { return "", unixENOTSUP }

var unixENOTSUP = errors.New("xattr unsupported")

func classifyXattrErr(err error) string {
	if err == nil {
		return XattrOK
	}
	return XattrUnsupported
}

func allocatedLive(path string) (int64, error) {
	st, err := os.Stat(path)
	if err != nil {
		return 0, err
	}
	return st.Size(), nil
}

func walkSizeLive(root string) (allocated, logical int64, err error) {
	err = filepath.WalkDir(root, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil || d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		logical += info.Size()
		allocated += info.Size()
		return nil
	})
	return allocated, logical, err
}
