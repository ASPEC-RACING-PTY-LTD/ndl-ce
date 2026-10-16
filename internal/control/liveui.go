package control

import (
	"io/fs"
	"os"
	"path/filepath"
	"sort"
)

// liveUI serves the web UI of the active live update when there is one and
// the packaged UI otherwise. The live release is followed through a symlink
// on every request, so switching it takes effect without a restart. A file
// the live release lacks (a page loaded before the switch asking for its
// old script chunks) is looked up in the other kept releases, then in the
// packaged UI.
type liveUI struct {
	link     string // <live root>/ui -> releases/<id>/files/ui
	releases string // <live root>/releases
	packaged fs.FS
}

func newLiveUI(liveRoot string, packaged fs.FS) fs.FS {
	return liveUI{
		link:     filepath.Join(liveRoot, "ui"),
		releases: filepath.Join(liveRoot, "releases"),
		packaged: packaged,
	}
}

func (l liveUI) Open(name string) (fs.File, error) {
	if !fs.ValidPath(name) {
		return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrInvalid}
	}
	if target, err := os.Readlink(l.link); err == nil {
		if f, err := os.DirFS(target).Open(name); err == nil {
			return f, nil
		}
		if name == "." || name == "index.html" {
			// The live release is incomplete; fall through to packaged.
		} else if f := l.fromOtherReleases(target, name); f != nil {
			return f, nil
		}
	}
	if l.packaged == nil {
		return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrNotExist}
	}
	return l.packaged.Open(name)
}

func (l liveUI) fromOtherReleases(active, name string) fs.File {
	entries, err := os.ReadDir(l.releases)
	if err != nil {
		return nil
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() > entries[j].Name() })
	for _, e := range entries {
		dir := filepath.Join(l.releases, e.Name(), "files", "ui")
		if !e.IsDir() || dir == active {
			continue
		}
		if f, err := os.DirFS(dir).Open(name); err == nil {
			return f
		}
	}
	return nil
}
