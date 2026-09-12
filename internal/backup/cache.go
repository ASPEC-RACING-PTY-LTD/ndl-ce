package backup

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// cacheEntry records what the engine last saw for a path. Reuse of prior chunk
// references requires every tracked attribute to match: the engine never trusts
// mtime alone. If anything differs, or the entry is missing, the file is reread
// and rechunked. Correctness beats speed.
type cacheEntry struct {
	Size    int64   `json:"size"`
	MTimeNS int64   `json:"mtime_ns"`
	CTimeNS int64   `json:"ctime_ns"`
	Inode   uint64  `json:"inode"`
	Mode    uint32  `json:"mode"`
	UID     int     `json:"uid"`
	GID     int     `json:"gid"`
	Chunks  []KeyID `json:"chunks"`
}

// metaCache is a persistent per-workload filesystem metadata cache. It avoids
// rereading and rechunking files that are provably unchanged. It can be rebuilt
// (approximately) from the previous manifest; losing it only makes the next
// backup slower, never makes restore impossible.
type metaCache struct {
	repo      *Repository
	namespace string
	entries   map[string]cacheEntry
}

func (r *Repository) cachePath(namespace string) string {
	return filepath.Join(r.root, "cache", namespace+".cache")
}

func (r *Repository) loadCache(namespace string) (*metaCache, error) {
	c := &metaCache{repo: r, namespace: namespace, entries: map[string]cacheEntry{}}
	raw, err := os.ReadFile(r.cachePath(namespace))
	if err != nil {
		if os.IsNotExist(err) {
			return c, nil
		}
		return nil, err
	}
	plain, err := r.keys.OpenBytes("cache:"+namespace, raw)
	if err != nil {
		// A corrupt or unauthenticated cache is discarded; the next backup
		// simply rereads everything.
		return c, nil
	}
	_ = json.Unmarshal(plain, &c.entries)
	return c, nil
}

func (c *metaCache) save() error {
	raw, err := json.Marshal(c.entries)
	if err != nil {
		return err
	}
	sealed, err := c.repo.keys.SealBytes("cache:"+c.namespace, raw)
	if err != nil {
		return err
	}
	return writeFileAtomic(c.repo.cachePath(c.namespace), sealed)
}

// lookup returns the cached chunk list when the observed metadata matches the
// cached metadata exactly.
func (c *metaCache) lookup(path string, meta cacheEntry) ([]KeyID, bool) {
	prev, ok := c.entries[path]
	if !ok {
		return nil, false
	}
	if prev.Size != meta.Size || prev.MTimeNS != meta.MTimeNS || prev.CTimeNS != meta.CTimeNS ||
		prev.Inode != meta.Inode || prev.Mode != meta.Mode || prev.UID != meta.UID || prev.GID != meta.GID {
		return nil, false
	}
	return prev.Chunks, true
}

func (c *metaCache) update(path string, meta cacheEntry, chunks []KeyID) {
	meta.Chunks = chunks
	c.entries[path] = meta
}

// prune drops cache entries for paths not present in the latest capture so the
// cache does not grow without bound as files are deleted.
func (c *metaCache) prune(seen map[string]struct{}) {
	for p := range c.entries {
		if _, ok := seen[p]; !ok {
			delete(c.entries, p)
		}
	}
}
