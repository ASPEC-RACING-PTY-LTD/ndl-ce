package iojail

import (
	"os"
	"time"
)

// FileMeta is jail-scoped metadata for one path. Owner and group are
// best-effort names from the host or guest passwd database.
type FileMeta struct {
	Name    string
	Type    string
	Size    int64
	Mode    uint32
	UID     uint32
	GID     uint32
	Owner   string
	Group   string
	ModTime string
	Path    string
}

// MetaFromInfo fills FileMeta from lstat/stat results without following
// the caller's path further.
func MetaFromInfo(info os.FileInfo, name, rel string) FileMeta {
	kind := "file"
	if info.IsDir() {
		kind = "dir"
	} else if info.Mode()&os.ModeSymlink != 0 {
		kind = "symlink"
	}
	meta := FileMeta{
		Name:    name,
		Type:    kind,
		Size:    info.Size(),
		Mode:    uint32(info.Mode().Perm()),
		ModTime: info.ModTime().UTC().Format(time.RFC3339),
		Path:    rel,
	}
	fillOwner(&meta, info)
	return meta
}
