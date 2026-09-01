package inventory

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// FS reads a host or fixture tree. Paths are POSIX-style from the host root.
type FS struct {
	Root string
}

// Live returns the real host root.
func Live() FS {
	return FS{Root: "/"}
}

func (f FS) fixture() bool {
	return f.Root != "" && f.Root != "/"
}

func (f FS) join(p string) string {
	p = strings.TrimPrefix(p, "/")
	if f.Root == "" || f.Root == "/" {
		return "/" + p
	}
	return filepath.Join(f.Root, filepath.FromSlash(encodeFixturePath(p)))
}

func (f FS) read(p string) (string, error) {
	b, err := os.ReadFile(f.join(p))
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(b)), nil
}

func (f FS) readOK(p string) string {
	s, err := f.read(p)
	if err != nil {
		return ""
	}
	return s
}

func (f FS) exists(p string) bool {
	_, err := os.Stat(f.join(p))
	return err == nil
}

func (f FS) list(p string) []string {
	ents, err := os.ReadDir(f.join(p))
	if err != nil {
		return nil
	}
	out := make([]string, 0, len(ents))
	for _, e := range ents {
		name := e.Name()
		if f.fixture() {
			name = decodeFixtureName(name)
		}
		out = append(out, name)
	}
	return out
}

// fixtureColon stands in for ':' in Windows fixture trees (PCI/USB names).
const fixtureColon = "@"

func encodeFixturePath(p string) string {
	return strings.ReplaceAll(p, ":", fixtureColon)
}

func decodeFixtureName(name string) string {
	return strings.ReplaceAll(name, fixtureColon, ":")
}

func (f FS) readlink(p string) string {
	target, err := os.Readlink(f.join(p))
	if err != nil {
		return ""
	}
	return filepath.ToSlash(target)
}

func (f FS) readUint(p string) (uint64, bool) {
	s := f.readOK(p)
	if s == "" {
		return 0, false
	}
	n, err := strconv.ParseUint(strings.TrimSpace(s), 10, 64)
	return n, err == nil
}

func (f FS) readInt(p string) (int, bool) {
	s := f.readOK(p)
	if s == "" {
		return 0, false
	}
	n, err := strconv.Atoi(strings.TrimSpace(s))
	return n, err == nil
}

func basename(p string) string {
	p = strings.TrimRight(filepath.ToSlash(p), "/")
	i := strings.LastIndex(p, "/")
	if i < 0 {
		return p
	}
	return p[i+1:]
}
