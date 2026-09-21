//go:build linux

package backup

import (
	"encoding/base64"
	"io"
	"os"
	"sort"
	"strings"
	"syscall"

	"golang.org/x/sys/unix"
)

func fileNlinkRdev(info os.FileInfo) (nlink uint32, rdev uint64) {
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok || st == nil {
		return 1, 0
	}
	return uint32(st.Nlink), uint64(st.Rdev)
}

func readXattrs(path string) (map[string]string, []string) {
	sz, err := unix.Llistxattr(path, nil)
	if err != nil {
		if err == unix.ENOTSUP || err == unix.EOPNOTSUPP || err == unix.ENODATA {
			if err == unix.ENODATA {
				return nil, nil
			}
			return nil, []string{"xattr"}
		}
		return nil, []string{"xattr"}
	}
	if sz <= 0 {
		return nil, nil
	}
	buf := make([]byte, sz)
	n, err := unix.Llistxattr(path, buf)
	if err != nil {
		return nil, []string{"xattr"}
	}
	names := splitXattrNames(buf[:n])
	out := map[string]string{}
	for _, name := range names {
		val, err := readOneXattr(path, name)
		if err != nil {
			return out, []string{"xattr"}
		}
		if val != "" {
			out[name] = val
		}
	}
	if len(out) == 0 {
		return nil, nil
	}
	return out, nil
}

func readOneXattr(path, name string) (string, error) {
	sz, err := unix.Lgetxattr(path, name, nil)
	if err != nil {
		if err == unix.ENODATA {
			return "", nil
		}
		return "", err
	}
	if sz <= 0 {
		return "", nil
	}
	buf := make([]byte, sz)
	n, err := unix.Lgetxattr(path, name, buf)
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(buf[:n]), nil
}

func applyXattrs(path string, attrs map[string]string) []string {
	if len(attrs) == 0 {
		return nil
	}
	var degraded []string
	for name, enc := range attrs {
		raw, err := base64.StdEncoding.DecodeString(enc)
		if err != nil {
			degraded = append(degraded, "xattr")
			continue
		}
		if err := unix.Lsetxattr(path, name, raw, 0); err != nil {
			degraded = append(degraded, "xattr")
		}
	}
	return uniqueStrings(degraded)
}

func splitXattrNames(buf []byte) []string {
	var names []string
	for _, part := range strings.Split(string(buf), "\x00") {
		if part != "" {
			names = append(names, part)
		}
	}
	sort.Strings(names)
	return names
}

func detectHoles(f *os.File, size int64) ([]Hole, []string) {
	if size <= 0 {
		return nil, nil
	}
	var holes []Hole
	off := int64(0)
	for off < size {
		data, err := unix.Seek(int(f.Fd()), off, unix.SEEK_DATA)
		if err != nil {
			if err == unix.ENXIO {
				if off < size {
					holes = append(holes, Hole{Offset: off, Length: size - off})
				}
				return holes, nil
			}
			return nil, []string{"sparse"}
		}
		if data > off {
			holes = append(holes, Hole{Offset: off, Length: data - off})
		}
		hole, err := unix.Seek(int(f.Fd()), data, unix.SEEK_HOLE)
		if err != nil {
			return holes, []string{"sparse"}
		}
		off = hole
		if hole <= data {
			break
		}
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return holes, []string{"sparse"}
	}
	return holes, nil
}

func punchHoles(f *os.File, holes []Hole) {
	for _, h := range holes {
		if h.Length <= 0 {
			continue
		}
		_ = unix.Fallocate(int(f.Fd()), unix.FALLOC_FL_PUNCH_HOLE|unix.FALLOC_FL_KEEP_SIZE, h.Offset, h.Length)
	}
}

func makeSpecial(path string, typ EntryType, mode uint32, rdev uint64) error {
	perm := os.FileMode(mode).Perm()
	switch typ {
	case EntryFIFO:
		return unix.Mkfifo(path, uint32(perm))
	case EntrySocket:
		return unix.Mknod(path, uint32(perm)|unix.S_IFSOCK, 0)
	case EntryBlock:
		return unix.Mknod(path, uint32(perm)|unix.S_IFBLK, int(rdev))
	case EntryChar:
		return unix.Mknod(path, uint32(perm)|unix.S_IFCHR, int(rdev))
	default:
		return nil
	}
}
