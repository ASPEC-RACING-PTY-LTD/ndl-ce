package backup

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

// DefaultPackTarget is the size at which the active pack buffer is flushed to a
// pack object. Small immutable chunks are aggregated into packs so the remote
// object store is not hammered with millions of tiny requests and so multipart
// upload becomes useful for large packs.
const DefaultPackTarget = 16 << 20

type location struct {
	pack string
	off  int
	size int
}

type packEntry struct {
	ID  KeyID `json:"id"`
	Off int   `json:"off"`
	Len int   `json:"len"`
}

type packIndex struct {
	Pack    string      `json:"pack"`
	Size    int64       `json:"size"`
	Entries []packEntry `json:"entries"`
}

// Repository is the local content-addressed store. The in-memory chunk index is
// rebuilt from pack index sidecars on Open, so losing the process memory never
// loses data: the repository is authoritative and self-describing.
type Repository struct {
	root string
	keys *Keys

	mu    sync.RWMutex
	index map[KeyID]location

	manifestMu    sync.Mutex
	manifestCache map[string]manifestSummaryCacheEntry
	manifestRead  func(string) ([]byte, error)

	capture sync.RWMutex

	recovery RecoveryReport
}

// RecoveryReport describes what OpenRepository cleaned up after an earlier
// crash, interrupted capture or interrupted upload. Nothing in it is a valid
// restore point: temporary files are half-written copies, and orphan packs
// have no committed index so no manifest can reference them.
type RecoveryReport struct {
	TempFilesRemoved   int   `json:"temp_files_removed,omitempty"`
	TempBytesRemoved   int64 `json:"temp_bytes_removed,omitempty"`
	OrphanPacksRemoved int   `json:"orphan_packs_removed,omitempty"`
	OrphanBytesRemoved int64 `json:"orphan_bytes_removed,omitempty"`
	// StatesRebuilt counts complete manifests whose state sidecar was lost
	// in a crash. They are kept and marked Recovered, never deleted.
	StatesRebuilt int `json:"states_rebuilt,omitempty"`
}

// Recovery returns what the last Open cleaned up.
func (r *Repository) Recovery() RecoveryReport { return r.recovery }

// Root is the repository directory.
func (r *Repository) Root() string { return r.root }

// OpenRepository opens or creates a repository rooted at root.
func OpenRepository(root string, keys *Keys) (*Repository, error) {
	if !filepath.IsAbs(root) {
		abs, err := filepath.Abs(root)
		if err != nil {
			return nil, err
		}
		root = abs
	}
	for _, d := range []string{"packs", "snapshots", "cache", "state"} {
		if err := os.MkdirAll(filepath.Join(root, d), 0o750); err != nil {
			return nil, err
		}
	}
	r := &Repository{
		root: root, keys: keys, index: map[KeyID]location{},
		manifestCache: map[string]manifestSummaryCacheEntry{},
	}
	r.removeTempFiles()
	if err := r.rebuildIndex(); err != nil {
		return nil, err
	}
	r.rebuildMissingStates()
	return r, nil
}

// removeTempFiles deletes half-written ".tmp" files left by a crash during
// writeFileAtomic. They are never renamed into place, so nothing refers to
// them.
func (r *Repository) removeTempFiles() {
	_ = filepath.WalkDir(r.root, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || filepath.Ext(d.Name()) != ".tmp" {
			return nil
		}
		info, ierr := d.Info()
		if os.Remove(path) == nil {
			r.recovery.TempFilesRemoved++
			if ierr == nil {
				r.recovery.TempBytesRemoved += info.Size()
			}
		}
		return nil
	})
}

// rebuildMissingStates recreates the state sidecar of a committed manifest
// whose capture crashed before the sidecar was written. Without a sidecar the
// restore point is invisible to status and retention yet still pins its
// chunks forever. The rebuilt point is marked Recovered so operators see it.
func (r *Repository) rebuildMissingStates() {
	base := filepath.Join(r.root, "snapshots")
	nsDirs, err := os.ReadDir(base)
	if err != nil {
		return
	}
	for _, ns := range nsDirs {
		if !ns.IsDir() {
			continue
		}
		files, err := os.ReadDir(filepath.Join(base, ns.Name()))
		if err != nil {
			continue
		}
		for _, f := range files {
			if filepath.Ext(f.Name()) != ".snap" {
				continue
			}
			id := strings.TrimSuffix(f.Name(), ".snap")
			if _, err := os.Stat(filepath.Join(base, ns.Name(), id+".state")); err == nil {
				continue
			}
			m, err := r.LoadManifest(ns.Name(), id)
			if err != nil {
				continue
			}
			st := &PointState{
				BackupID: m.BackupID, Namespace: m.Namespace, WorkloadID: m.WorkloadID,
				WorkloadName: m.WorkloadName, CreatedAtNS: m.CreatedAtNS,
				LocalComplete: true, Remote: RemoteNone, Recovered: true,
			}
			if r.writeState(st) == nil {
				r.recovery.StatesRebuilt++
			}
		}
	}
}

func (r *Repository) packDir() string { return filepath.Join(r.root, "packs") }

// Keys returns the repository keyring. Callers must not log or serialize it.
func (r *Repository) Keys() *Keys { return r.keys }

func (r *Repository) invalidateManifest(namespace, backupID string) {
	path := filepath.Join(r.root, "snapshots", namespace, backupID+".snap")
	r.manifestMu.Lock()
	delete(r.manifestCache, path)
	r.manifestMu.Unlock()
}

// rebuildIndex scans pack sidecars. A .pack without a committed .idx is an
// interrupted write and is removed so it cannot corrupt the repository.
func (r *Repository) rebuildIndex() error {
	entries, err := os.ReadDir(r.packDir())
	if err != nil {
		return err
	}
	idxByPack := map[string]bool{}
	for _, e := range entries {
		if filepath.Ext(e.Name()) == ".idx" {
			idxByPack[e.Name()[:len(e.Name())-4]] = true
		}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.index = map[KeyID]location{}
	for _, e := range entries {
		name := e.Name()
		if filepath.Ext(name) != ".pack" {
			continue
		}
		packID := name[:len(name)-5]
		if !idxByPack[packID] {
			// Interrupted pack write: discard.
			info, ierr := e.Info()
			if os.Remove(filepath.Join(r.packDir(), name)) == nil {
				r.recovery.OrphanPacksRemoved++
				if ierr == nil {
					r.recovery.OrphanBytesRemoved += info.Size()
				}
			}
			continue
		}
		raw, err := os.ReadFile(filepath.Join(r.packDir(), packID+".idx"))
		if err != nil {
			return err
		}
		var pi packIndex
		if err := json.Unmarshal(raw, &pi); err != nil {
			return fmt.Errorf("pack index %s: %w", packID, err)
		}
		for _, ent := range pi.Entries {
			r.index[ent.ID] = location{pack: packID, off: ent.Off, size: ent.Len}
		}
	}
	return nil
}

func (r *Repository) beginCapture() { r.capture.RLock() }
func (r *Repository) endCapture()   { r.capture.RUnlock() }

// Has reports whether a chunk id is already stored.
func (r *Repository) Has(id KeyID) bool {
	r.mu.RLock()
	_, ok := r.index[id]
	r.mu.RUnlock()
	return ok
}

// GetSealed returns the sealed bytes for a chunk id.
func (r *Repository) GetSealed(id KeyID) ([]byte, error) {
	r.mu.RLock()
	loc, ok := r.index[id]
	r.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("chunk %s not found", id)
	}
	f, err := os.Open(filepath.Join(r.packDir(), loc.pack+".pack"))
	if err != nil {
		return nil, err
	}
	defer f.Close()
	buf := make([]byte, loc.size)
	if _, err := f.ReadAt(buf, int64(loc.off)); err != nil {
		return nil, err
	}
	return buf, nil
}

// GetChunk returns the decrypted plaintext for a chunk id.
func (r *Repository) GetChunk(id KeyID) ([]byte, error) {
	sealed, err := r.GetSealed(id)
	if err != nil {
		return nil, err
	}
	plain, err := r.keys.Open(id, sealed)
	if err != nil {
		return nil, fmt.Errorf("chunk %s failed authentication: %w", id, err)
	}
	return plain, nil
}

func (r *Repository) setLocation(id KeyID, loc location) {
	r.mu.Lock()
	r.index[id] = loc
	r.mu.Unlock()
}

// packWriter aggregates sealed chunks into a pack buffer and flushes immutable
// pack objects. It is used for the duration of a single capture.
type packWriter struct {
	repo   *Repository
	target int
	buf    []byte
	ents   []packEntry
	staged map[KeyID]struct{}
	packs  []string // pack ids committed during this capture
	// guard is called before committing a pack so the workspace ceiling and
	// host free-space reserve can stop accepting new data safely.
	guard func() error
	// rewrite stores chunks even when the index already has them. Compaction
	// uses it to copy live chunks out of a mostly-dead pack.
	rewrite bool
}

func (r *Repository) newPackWriter(target int) *packWriter {
	if target <= 0 {
		target = DefaultPackTarget
	}
	return &packWriter{repo: r, target: target, staged: map[KeyID]struct{}{}}
}

// add stores one sealed chunk unless it is already present in the repository or
// already staged in the current pack. It returns whether the chunk was new.
func (w *packWriter) add(id KeyID, sealed []byte) (bool, error) {
	if !w.rewrite && w.repo.Has(id) {
		return false, nil
	}
	if _, ok := w.staged[id]; ok {
		return false, nil
	}
	w.ents = append(w.ents, packEntry{ID: id, Off: len(w.buf), Len: len(sealed)})
	w.buf = append(w.buf, sealed...)
	w.staged[id] = struct{}{}
	if len(w.buf) >= w.target {
		if err := w.flush(); err != nil {
			return false, err
		}
	}
	return true, nil
}

// flush commits the current buffer as an immutable pack. The pack is named by
// the hash of its contents. The .pack file is written first, then the .idx; a
// crash between the two leaves an orphan .pack that Open discards.
func (w *packWriter) flush() error {
	if len(w.ents) == 0 {
		return nil
	}
	if w.guard != nil {
		if err := w.guard(); err != nil {
			return err
		}
	}
	sum := sha256.Sum256(w.buf)
	packID := "pack-" + hex.EncodeToString(sum[:16])
	dir := w.repo.packDir()
	packPath := filepath.Join(dir, packID+".pack")
	if err := writeFileAtomic(packPath, w.buf); err != nil {
		return err
	}
	pi := packIndex{Pack: packID, Size: int64(len(w.buf)), Entries: w.ents}
	raw, err := json.Marshal(pi)
	if err != nil {
		return err
	}
	if err := writeFileAtomic(filepath.Join(dir, packID+".idx"), raw); err != nil {
		return err
	}
	for _, ent := range w.ents {
		w.repo.setLocation(ent.ID, location{pack: packID, off: ent.Off, size: ent.Len})
	}
	w.packs = append(w.packs, packID)
	w.buf = nil
	w.ents = nil
	return nil
}

// packList returns the ids of all committed packs.
func (r *Repository) packList() ([]string, error) {
	entries, err := os.ReadDir(r.packDir())
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range entries {
		if filepath.Ext(e.Name()) == ".idx" {
			out = append(out, e.Name()[:len(e.Name())-4])
		}
	}
	sort.Strings(out)
	return out, nil
}

func (r *Repository) readPackIndex(packID string) (packIndex, error) {
	var pi packIndex
	raw, err := os.ReadFile(filepath.Join(r.packDir(), packID+".idx"))
	if err != nil {
		return pi, err
	}
	err = json.Unmarshal(raw, &pi)
	return pi, err
}

func (r *Repository) deletePack(packID string) error {
	r.mu.Lock()
	for id, loc := range r.index {
		if loc.pack == packID {
			delete(r.index, id)
		}
	}
	r.mu.Unlock()
	_ = os.Remove(filepath.Join(r.packDir(), packID+".pack"))
	return os.Remove(filepath.Join(r.packDir(), packID+".idx"))
}

// SizeOnDisk returns the total bytes the repository occupies: packs,
// manifests, caches, queue state and any leftover temporary files. The
// workspace ceiling is enforced against this, not only against packs.
func (r *Repository) SizeOnDisk() (int64, error) {
	u, err := r.Usage()
	if err != nil {
		return 0, err
	}
	return u.TotalBytes, nil
}

// RepoUsage breaks the repository footprint down by area.
type RepoUsage struct {
	TotalBytes     int64 `json:"total_bytes"`
	PackBytes      int64 `json:"pack_bytes"`
	PackCount      int   `json:"pack_count"`
	SnapshotBytes  int64 `json:"snapshot_bytes"`
	CacheBytes     int64 `json:"cache_bytes"`
	StateBytes     int64 `json:"state_bytes"`
	TempBytes      int64 `json:"temp_bytes"`
	OtherBytes     int64 `json:"other_bytes"`
	RestorePoints  int   `json:"restore_points"`
	RecoveredCount int   `json:"recovered_points,omitempty"`
}

// Usage walks the repository and sizes each area.
func (r *Repository) Usage() (RepoUsage, error) {
	var u RepoUsage
	if _, err := os.Stat(r.root); err != nil {
		return u, err
	}
	err := filepath.WalkDir(r.root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		n := info.Size()
		u.TotalBytes += n
		rel, _ := filepath.Rel(r.root, path)
		top := strings.SplitN(filepath.ToSlash(rel), "/", 2)[0]
		switch {
		case filepath.Ext(path) == ".tmp" || strings.HasPrefix(top, "remote-restore-"):
			u.TempBytes += n
		case top == "packs":
			u.PackBytes += n
			if filepath.Ext(path) == ".pack" {
				u.PackCount++
			}
		case top == "snapshots":
			u.SnapshotBytes += n
			if filepath.Ext(path) == ".snap" {
				u.RestorePoints++
			}
		case top == "cache":
			u.CacheBytes += n
		case top == "state":
			u.StateBytes += n
		default:
			u.OtherBytes += n
		}
		return nil
	})
	return u, err
}

func writeFileAtomic(path string, data []byte) error {
	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o640)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, path)
}
