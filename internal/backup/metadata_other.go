//go:build !linux

package backup

import "os"

func fileNlinkRdev(info os.FileInfo) (nlink uint32, rdev uint64) {
	return 1, 0
}

func readXattrs(path string) (map[string]string, []string) {
	return nil, []string{"xattr"}
}

func applyXattrs(path string, attrs map[string]string) []string {
	if len(attrs) == 0 {
		return nil
	}
	return []string{"xattr"}
}

func detectHoles(f *os.File, size int64) ([]Hole, []string) {
	return nil, []string{"sparse"}
}

func punchHoles(f *os.File, holes []Hole) {}

func makeSpecial(path string, typ EntryType, mode uint32, rdev uint64) error {
	return os.ErrInvalid
}
