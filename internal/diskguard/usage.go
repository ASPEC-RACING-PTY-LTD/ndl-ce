package diskguard

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Category is one kind of No-dal data under the data directory.
type Category struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	// Dir is relative to the data directory.
	Dir string `json:"dir"`
	// Cleanup says what Clean removes; empty means it is never cleaned
	// here (user data such as pools, backups and game servers).
	Cleanup string `json:"cleanup,omitempty"`
	Bytes   int64  `json:"bytes"`
	// Partial is true when the size walk hit its time limit.
	Partial bool `json:"partial,omitempty"`
}

// Cleanable categories. Each removes only disposable copies: never pool
// volumes, workload disks, backups in the backup repository, or game data.
const (
	CleanCheckpoints    = "update-checkpoints"
	CleanBackupStaging  = "backup-staging"
	CleanMigration      = "migration-staging"
	CleanRestoreStaging = "restore-staging"
	CleanImageCache     = "image-cache"
)

// StaleAfter is how old a staging entry must be before cleanup removes it,
// so an operation that is still running keeps its files.
const StaleAfter = 24 * time.Hour

// KeepCheckpoints is how many update checkpoints cleanup keeps.
const KeepCheckpoints = 1

func categories() []Category {
	return []Category{
		{ID: "storage", Label: "Storage pools on this disk (workload disks)", Dir: "storage"},
		{ID: "backup-repo", Label: "Backup repository (local cache)", Dir: "backup-repo"},
		{ID: "backups", Label: "Local backup targets", Dir: "backups"},
		{ID: CleanBackupStaging, Label: "Backup staging", Dir: "backup-staging", Cleanup: "Removes staging files older than 24 hours."},
		{ID: CleanMigration, Label: "Migration staging", Dir: "migration", Cleanup: "Removes staging files older than 24 hours."},
		{ID: CleanRestoreStaging, Label: "File restore staging", Dir: "restore-files", Cleanup: "Removes staging files older than 24 hours."},
		{ID: CleanCheckpoints, Label: "Update checkpoints", Dir: "update-checkpoints", Cleanup: "Keeps the newest checkpoint and removes older ones."},
		{ID: CleanImageCache, Label: "Container image cache", Dir: "cache/lxc-images", Cleanup: "Removes cached images; they are downloaded again when needed."},
		{ID: "gameservers", Label: "Game servers", Dir: "gameservers"},
		{ID: "game-backups", Label: "Game server backups", Dir: "game-backups"},
	}
}

// Usage is what fills the data directory's filesystem.
type Usage struct {
	DataDir    string     `json:"data_dir"`
	Categories []Category `json:"categories"`
	// OtherBytes is the rest of the data directory.
	OtherBytes int64 `json:"other_bytes"`
	// OutsideBytes is space used on the same filesystem outside the data
	// directory (the OS, logs, home folders, test output).
	OutsideBytes int64     `json:"outside_bytes"`
	FS           *FS       `json:"filesystem,omitempty"`
	MeasuredAt   time.Time `json:"measured_at"`
}

// dirBytes sums allocated bytes under root on root's filesystem, stopping
// at the deadline. Mounted pools below root are not counted.
func dirBytes(ctx context.Context, root string) (int64, bool) {
	st, err := os.Lstat(root)
	if err != nil {
		return 0, false
	}
	dev := deviceOf(st)
	var total int64
	partial := false
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if ctx.Err() != nil {
			partial = true
			return filepath.SkipAll
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		if d.IsDir() && p != root && deviceOf(info) != dev {
			return filepath.SkipDir
		}
		total += allocatedBytes(info)
		return nil
	})
	return total, partial
}

// Measure sizes every category under dataDir. It takes at most budget.
func (g *Guard) Measure(ctx context.Context, dataDir string, budget time.Duration) Usage {
	ctx, cancel := context.WithTimeout(ctx, budget)
	defer cancel()
	out := Usage{DataDir: dataDir, MeasuredAt: g.clock()}
	var counted int64
	for _, c := range categories() {
		c.Bytes, c.Partial = dirBytes(ctx, filepath.Join(dataDir, c.Dir))
		counted += c.Bytes
		out.Categories = append(out.Categories, c)
	}
	all, _ := dirBytes(ctx, dataDir)
	if rest := all - counted; rest > 0 {
		out.OtherBytes = rest
	}
	st := g.Refresh()
	if info, err := g.statFn()(nearestExisting(dataDir)); err == nil {
		for i := range st.Filesystems {
			if st.Filesystems[i].Device == info.device {
				fs := st.Filesystems[i]
				out.FS = &fs
				if used := fs.TotalBytes - fs.FreeBytes - all; used > 0 {
					out.OutsideBytes = used
				}
			}
		}
	}
	sort.SliceStable(out.Categories, func(i, j int) bool { return out.Categories[i].Bytes > out.Categories[j].Bytes })
	return out
}

// CleanResult reports what a cleanup removed.
type CleanResult struct {
	Category     string   `json:"category"`
	RemovedBytes int64    `json:"removed_bytes"`
	Removed      []string `json:"removed"`
	Kept         int      `json:"kept"`
}

// Clean removes disposable files in one cleanable category. It refuses
// every other category.
func (g *Guard) Clean(ctx context.Context, dataDir, id string, protect []string) (CleanResult, error) {
	var cat *Category
	for _, c := range categories() {
		if c.ID == id && c.Cleanup != "" {
			c := c
			cat = &c
		}
	}
	if cat == nil {
		return CleanResult{}, fmt.Errorf("%q cannot be cleaned up here", id)
	}
	dir := filepath.Join(dataDir, cat.Dir)
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return CleanResult{Category: id}, nil
	}
	if err != nil {
		return CleanResult{}, err
	}
	keep := map[string]bool{}
	for _, p := range protect {
		keep[strings.TrimSpace(p)] = true
	}
	type item struct {
		name string
		mod  time.Time
	}
	var items []item
	for _, e := range entries {
		info, err := e.Info()
		if err != nil {
			continue
		}
		items = append(items, item{e.Name(), newestMod(filepath.Join(dir, e.Name()), info.ModTime())})
	}
	res := CleanResult{Category: id}
	remove := func(name string) {
		p := filepath.Join(dir, name)
		n, _ := dirBytes(ctx, p)
		if err := os.RemoveAll(p); err == nil {
			res.RemovedBytes += n
			res.Removed = append(res.Removed, name)
		}
	}
	switch id {
	case CleanCheckpoints:
		// A checkpoint is <id>.tar plus <id>.sql; keep the newest ids.
		newest := map[string]time.Time{}
		for _, it := range items {
			base := strings.TrimSuffix(strings.TrimSuffix(it.name, ".tar"), ".sql")
			if it.mod.After(newest[base]) {
				newest[base] = it.mod
			}
		}
		ids := make([]string, 0, len(newest))
		for k := range newest {
			ids = append(ids, k)
		}
		sort.Slice(ids, func(i, j int) bool { return newest[ids[i]].After(newest[ids[j]]) })
		keepIDs := map[string]bool{}
		for i, k := range ids {
			if i < KeepCheckpoints || keep[k] {
				keepIDs[k] = true
			}
		}
		res.Kept = len(keepIDs)
		for _, it := range items {
			base := strings.TrimSuffix(strings.TrimSuffix(it.name, ".tar"), ".sql")
			if !keepIDs[base] {
				remove(it.name)
			}
		}
	default:
		cutoff := g.clock().Add(-StaleAfter)
		for _, it := range items {
			if keep[it.name] || it.mod.After(cutoff) {
				res.Kept++
				continue
			}
			remove(it.name)
		}
	}
	g.Refresh()
	return res, nil
}

// newestMod is the latest modification time anywhere under p, so a
// staging folder with recently written files is never treated as stale.
// Folder times are ignored (they change when the folder is created); an
// empty folder falls back to its own time.
func newestMod(p string, start time.Time) time.Time {
	var newest time.Time
	_ = filepath.WalkDir(p, func(_ string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if info, err := d.Info(); err == nil && info.ModTime().After(newest) {
			newest = info.ModTime()
		}
		return nil
	})
	if newest.IsZero() {
		return start
	}
	return newest
}
