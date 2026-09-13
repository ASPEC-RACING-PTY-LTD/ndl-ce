package backup

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// locEntry is one chunk's physical location, used to make a restore point's
// remote data self-contained without a repository-wide listing.
type locEntry struct {
	ID   KeyID  `json:"id"`
	Pack string `json:"pack"`
	Off  int    `json:"off"`
	Len  int    `json:"len"`
}

// buildLocmap produces a sealed location map for every chunk referenced by a
// restore point, resolved against the current in-memory index.
func (r *Repository) buildLocmap(namespace, backupID string) ([]byte, error) {
	m, err := r.LoadManifest(namespace, backupID)
	if err != nil {
		return nil, err
	}
	seen := map[KeyID]struct{}{}
	var entries []locEntry
	r.mu.RLock()
	for _, f := range m.Files {
		for _, id := range f.Chunks {
			if _, ok := seen[id]; ok {
				continue
			}
			loc, ok := r.index[id]
			if !ok {
				r.mu.RUnlock()
				return nil, fmt.Errorf("chunk %s missing while building location map", id)
			}
			seen[id] = struct{}{}
			entries = append(entries, locEntry{ID: id, Pack: loc.pack, Off: loc.off, Len: loc.size})
		}
	}
	r.mu.RUnlock()
	raw, err := json.Marshal(entries)
	if err != nil {
		return nil, err
	}
	return r.keys.SealBytes("locmap:"+namespace, raw)
}

// FetchRemote reconstructs a local repository containing exactly the data needed
// to restore one remote-protected restore point. It downloads the manifest and
// location map, then fetches only the packs that hold referenced chunks, and
// synthesizes local pack indexes. This supports disaster recovery on a host
// that has lost its local repository, and correctly handles chunks that were
// deduplicated into packs written by other workloads.
func FetchRemote(ctx context.Context, target Target, keys *Keys, namespace, backupID, destRoot string) (*Repository, *Manifest, error) {
	repo, err := OpenRepository(destRoot, keys)
	if err != nil {
		return nil, nil, err
	}
	manRaw, err := target.Get(ctx, objectKeyManifest(namespace, backupID))
	if err != nil {
		return nil, nil, fmt.Errorf("fetch manifest: %w", err)
	}
	snapDir := filepath.Join(destRoot, "snapshots", namespace)
	if err := os.MkdirAll(snapDir, 0o750); err != nil {
		return nil, nil, err
	}
	if err := writeFileAtomic(filepath.Join(snapDir, backupID+".snap"), manRaw); err != nil {
		return nil, nil, err
	}
	man, err := repo.LoadManifest(namespace, backupID)
	if err != nil {
		return nil, nil, err
	}

	locRaw, err := target.Get(ctx, objectKeyLocmap(namespace, backupID))
	if err != nil {
		return nil, nil, fmt.Errorf("fetch location map: %w", err)
	}
	locPlain, err := keys.OpenBytes("locmap:"+namespace, locRaw)
	if err != nil {
		return nil, nil, fmt.Errorf("location map failed authentication: %w", err)
	}
	var entries []locEntry
	if err := json.Unmarshal(locPlain, &entries); err != nil {
		return nil, nil, err
	}

	byPack := map[string][]packEntry{}
	for _, e := range entries {
		byPack[e.Pack] = append(byPack[e.Pack], packEntry{ID: e.ID, Off: e.Off, Len: e.Len})
	}
	for packID, ents := range byPack {
		data, err := target.Get(ctx, objectKeyPack(packID))
		if err != nil {
			return nil, nil, fmt.Errorf("fetch pack %s: %w", packID, err)
		}
		if err := writeFileAtomic(filepath.Join(repo.packDir(), packID+".pack"), data); err != nil {
			return nil, nil, err
		}
		pi := packIndex{Pack: packID, Size: int64(len(data)), Entries: ents}
		raw, err := json.Marshal(pi)
		if err != nil {
			return nil, nil, err
		}
		if err := writeFileAtomic(filepath.Join(repo.packDir(), packID+".idx"), raw); err != nil {
			return nil, nil, err
		}
	}
	if err := repo.rebuildIndex(); err != nil {
		return nil, nil, err
	}
	if err := repo.Verify(man); err != nil {
		return nil, nil, err
	}
	return repo, man, nil
}
