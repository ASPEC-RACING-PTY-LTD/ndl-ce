package backup

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
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
}

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
	r := &Repository{root: root, keys: keys, index: map[KeyID]location{}}
	if err := r.rebuildIndex(); err != nil {
		return nil, err
	}
	return r, nil
}

func (r *Repository) packDir() string { return filepath.Join(r.root, "packs") }

// Keys returns the repository keyring. Callers must not log or serialize it.
func (r *Repository) Keys() *Keys { return r.keys }

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
			_ = os.Remove(filepath.Join(r.packDir(), name))
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
	if w.repo.Has(id) {
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

// SizeOnDisk returns the total bytes used by pack objects.
func (r *Repository) SizeOnDisk() (int64, error) {
	entries, err := os.ReadDir(r.packDir())
	if err != nil {
		return 0, err
	}
	var total int64
	for _, e := range entries {
		info, err := e.Info()
		if err != nil {
			continue
		}
		total += info.Size()
	}
	return total, nil
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
