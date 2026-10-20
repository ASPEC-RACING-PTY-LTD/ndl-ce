package backup

import (
	"os"
	"path/filepath"
	"strings"
)

// Reset empties the repository for a clean start: every pack, restore
// point, cache entry, upload job and remote bookkeeping file goes. The
// repository key (master.key) and the remembered targets (state/targets)
// stay, so an exported key still opens new backups and cloud targets keep
// working. The caller must make sure no capture or upload is running.
func (r *Repository) Reset() (removedBytes int64, err error) {
	r.capture.Lock()
	defer r.capture.Unlock()
	r.mu.Lock()
	defer r.mu.Unlock()
	clear := func(dir string, keep func(name string) bool) error {
		entries, err := os.ReadDir(dir)
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		for _, e := range entries {
			if keep != nil && keep(e.Name()) {
				continue
			}
			p := filepath.Join(dir, e.Name())
			removedBytes += treeSize(p)
			if err := os.RemoveAll(p); err != nil {
				return err
			}
		}
		return nil
	}
	for _, d := range []string{"packs", "snapshots", "cache"} {
		if err := clear(filepath.Join(r.root, d), nil); err != nil {
			return removedBytes, err
		}
	}
	if err := clear(filepath.Join(r.root, "state"), func(name string) bool {
		return name == "targets" || strings.HasPrefix(name, ".")
	}); err != nil {
		return removedBytes, err
	}
	r.index = map[KeyID]location{}
	r.manifestMu.Lock()
	r.manifestCache = map[string]manifestSummaryCacheEntry{}
	r.manifestMu.Unlock()
	return removedBytes, nil
}

func treeSize(p string) int64 {
	var n int64
	_ = filepath.WalkDir(p, func(_ string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			if info, ierr := d.Info(); ierr == nil {
				n += info.Size()
			}
		}
		return nil
	})
	return n
}
