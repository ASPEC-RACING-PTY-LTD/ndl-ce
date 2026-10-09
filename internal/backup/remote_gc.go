package backup

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// ObjectInfo describes one object in a remote target.
type ObjectInfo struct {
	Key  string `json:"key"`
	Size int64  `json:"size"`
}

// RemoteCleaner is a Target that can also list and delete objects. Retention
// needs it to remove expired restore points from the remote copy; targets that
// cannot list or delete keep every uploaded object.
type RemoteCleaner interface {
	Target
	Delete(ctx context.Context, key string) error
	List(ctx context.Context, prefix string) ([]ObjectInfo, error)
}

// RemoteExpireResult reports what expiring one restore point removed remotely.
type RemoteExpireResult struct {
	ManifestDeleted bool   `json:"manifest_deleted,omitempty"`
	CandidatePacks  int    `json:"candidate_packs,omitempty"`
	Unsupported     string `json:"unsupported,omitempty"`
}

// RemoteSweepResult reports a remote pack sweep.
type RemoteSweepResult struct {
	Candidates   int   `json:"candidates"`
	PacksDeleted int   `json:"packs_deleted"`
	BytesFreed   int64 `json:"bytes_freed"`
	StillInUse   int   `json:"still_in_use"`
	// ForeignPoints are remote restore points whose location map this host
	// cannot authenticate, usually because they were written with another
	// repository key. They cannot reference this host's packs, so they do not
	// block the sweep, but they are never deleted automatically.
	ForeignPoints int    `json:"foreign_points,omitempty"`
	Unsupported   string `json:"unsupported,omitempty"`
}

func (r *Repository) remoteGCPath(targetKey string) string {
	return filepath.Join(r.root, "state", "remote-gc", sanitize(targetKey)+".json")
}

func (r *Repository) loadSweepCandidates(targetKey string) map[string]struct{} {
	out := map[string]struct{}{}
	raw, err := os.ReadFile(r.remoteGCPath(targetKey))
	if err != nil {
		return out
	}
	var ids []string
	if json.Unmarshal(raw, &ids) == nil {
		for _, id := range ids {
			out[id] = struct{}{}
		}
	}
	return out
}

func (r *Repository) saveSweepCandidates(targetKey string, set map[string]struct{}) error {
	path := r.remoteGCPath(targetKey)
	if len(set) == 0 {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	ids := make([]string, 0, len(set))
	for id := range set {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	raw, err := json.Marshal(ids)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return err
	}
	return writeFileAtomic(path, raw)
}

// PendingSweep reports how many remote packs await a sweep for targetKey.
func (r *Repository) PendingSweep(targetKey string) int {
	return len(r.loadSweepCandidates(targetKey))
}

// ExpireRemote removes an expired restore point's manifest and location map
// from target. The packs it referenced are recorded as sweep candidates and
// removed later by SweepRemote only if nothing else still references them.
// Candidates are recorded before anything is deleted, so an interruption
// cannot strand packs.
func (r *Repository) ExpireRemote(ctx context.Context, t Target, targetKey, namespace, backupID string) (RemoteExpireResult, error) {
	var res RemoteExpireResult
	rc, ok := t.(RemoteCleaner)
	if !ok {
		res.Unsupported = "this backup target cannot delete objects, so expired restore points stay in it"
		return res, nil
	}
	if raw, err := t.Get(ctx, objectKeyLocmap(namespace, backupID)); err == nil {
		if packs, err := r.keys.locmapPacks(namespace, raw); err == nil && len(packs) > 0 {
			cands := r.loadSweepCandidates(targetKey)
			for p := range packs {
				cands[p] = struct{}{}
			}
			if err := r.saveSweepCandidates(targetKey, cands); err != nil {
				return res, err
			}
			res.CandidatePacks = len(packs)
		}
	}
	if err := rc.Delete(ctx, objectKeyManifest(namespace, backupID)); err != nil {
		return res, fmt.Errorf("delete remote manifest: %w", err)
	}
	if err := rc.Delete(ctx, objectKeyLocmap(namespace, backupID)); err != nil {
		return res, fmt.Errorf("delete remote location map: %w", err)
	}
	res.ManifestDeleted = true
	return res, nil
}

// SweepRemote deletes candidate packs that no remaining restore point needs.
// A pack is kept when any remote location map this host can read references
// it, or when any local restore point (including ones still uploading) has
// chunks in it. Only packs recorded as candidates by ExpireRemote are ever
// considered, so objects this host did not expire are never touched.
func (r *Repository) SweepRemote(ctx context.Context, t Target, targetKey string) (RemoteSweepResult, error) {
	var res RemoteSweepResult
	cands := r.loadSweepCandidates(targetKey)
	res.Candidates = len(cands)
	if len(cands) == 0 {
		return res, nil
	}
	rc, ok := t.(RemoteCleaner)
	if !ok {
		res.Unsupported = "this backup target cannot list or delete objects"
		return res, nil
	}
	objs, err := rc.List(ctx, "backups/")
	if err != nil {
		return res, fmt.Errorf("list remote objects: %w", err)
	}
	referenced, foreign, err := r.remoteReferences(ctx, t, objs)
	if err != nil {
		return res, err
	}
	res.ForeignPoints = foreign
	sizes := map[string]int64{}
	for _, o := range objs {
		if id, ok := packIDFromKey(o.Key); ok {
			sizes[id] = o.Size
		}
	}

	// Local references are taken under the capture lock: every finished
	// capture has written its manifest and none is half way, so a pack a
	// new capture deduplicates against is either protected here or already
	// gone locally and cannot be referenced again.
	r.capture.Lock()
	defer r.capture.Unlock()
	local, err := r.referencedPacks()
	if err != nil {
		return res, err
	}
	remaining := map[string]struct{}{}
	for id := range cands {
		if _, ok := referenced[id]; ok {
			res.StillInUse++
			continue
		}
		if _, ok := local[id]; ok {
			res.StillInUse++
			continue
		}
		size, present := sizes[id]
		if !present {
			continue
		}
		if err := rc.Delete(ctx, objectKeyPack(id)); err != nil {
			remaining[id] = struct{}{}
			continue
		}
		res.PacksDeleted++
		res.BytesFreed += size
	}
	if err := r.saveSweepCandidates(targetKey, remaining); err != nil {
		return res, err
	}
	if len(remaining) > 0 {
		return res, fmt.Errorf("%d remote pack(s) could not be deleted and will be retried", len(remaining))
	}
	return res, nil
}

// remoteReferences unions the packs referenced by every remote location map
// this host can authenticate and counts the ones it cannot.
func (r *Repository) remoteReferences(ctx context.Context, t Target, objs []ObjectInfo) (map[string]struct{}, int, error) {
	referenced := map[string]struct{}{}
	foreign := 0
	for _, o := range objs {
		ns, ok := locmapNamespace(o.Key)
		if !ok {
			continue
		}
		raw, err := t.Get(ctx, o.Key)
		if err != nil {
			return nil, 0, fmt.Errorf("read remote location map %s: %w", o.Key, err)
		}
		packs, err := r.keys.locmapPacks(ns, raw)
		if err != nil {
			foreign++
			continue
		}
		for p := range packs {
			referenced[p] = struct{}{}
		}
	}
	return referenced, foreign, nil
}

// referencedPacks returns every local pack holding a chunk of a local
// restore point. The caller holds the capture lock.
func (r *Repository) referencedPacks() (map[string]struct{}, error) {
	chunks, err := r.referencedChunks()
	if err != nil {
		return nil, err
	}
	out := map[string]struct{}{}
	r.mu.RLock()
	defer r.mu.RUnlock()
	for id := range chunks {
		if loc, ok := r.index[id]; ok {
			out[loc.pack] = struct{}{}
		}
	}
	return out, nil
}

// RemoteUsage breaks a remote target down so its size can be explained.
type RemoteUsage struct {
	TotalBytes    int64 `json:"total_bytes"`
	Objects       int   `json:"objects"`
	PackBytes     int64 `json:"pack_bytes"`
	PackCount     int   `json:"pack_count"`
	ManifestBytes int64 `json:"manifest_bytes"`
	RestorePoints int   `json:"restore_points"`
	// ForeignPoints cannot be read with this host's repository key.
	ForeignPoints int `json:"foreign_points,omitempty"`
	// UnreferencedPackBytes are packs no readable restore point needs. When
	// ForeignPoints is non-zero some of them may belong to those points.
	UnreferencedPackBytes int64 `json:"unreferenced_pack_bytes"`
	UnreferencedPacks     int   `json:"unreferenced_packs"`
	// OtherBytes are objects outside the Backup Engine V2 layout, such as
	// older whole-disk and archive backups.
	OtherBytes    int64            `json:"other_bytes"`
	OtherObjects  int              `json:"other_objects"`
	OtherPrefixes map[string]int64 `json:"other_prefixes,omitempty"`
	PendingSweep  int              `json:"pending_sweep,omitempty"`
}

// MeasureRemote lists a target and classifies what it holds. It reads every
// location map but never deletes anything.
func (r *Repository) MeasureRemote(ctx context.Context, t Target, targetKey string) (RemoteUsage, error) {
	var u RemoteUsage
	rc, ok := t.(RemoteCleaner)
	if !ok {
		return u, fmt.Errorf("this backup target cannot list objects")
	}
	objs, err := rc.List(ctx, "")
	if err != nil {
		return u, err
	}
	referenced, foreign, err := r.remoteReferences(ctx, t, objs)
	if err != nil {
		return u, err
	}
	u.ForeignPoints = foreign
	u.OtherPrefixes = map[string]int64{}
	for _, o := range objs {
		u.Objects++
		u.TotalBytes += o.Size
		if id, ok := packIDFromKey(o.Key); ok {
			u.PackBytes += o.Size
			u.PackCount++
			if _, live := referenced[id]; !live {
				u.UnreferencedPacks++
				u.UnreferencedPackBytes += o.Size
			}
			continue
		}
		if isManifestKey(o.Key) {
			u.ManifestBytes += o.Size
			if strings.HasSuffix(o.Key, ".locmap") {
				u.RestorePoints++
			}
			continue
		}
		u.OtherBytes += o.Size
		u.OtherObjects++
		u.OtherPrefixes[otherPrefix(o.Key)] += o.Size
	}
	u.PendingSweep = r.PendingSweep(targetKey)
	return u, nil
}

func packIDFromKey(key string) (string, bool) {
	const prefix = "backups/packs/"
	if !strings.HasPrefix(key, prefix) || !strings.HasSuffix(key, ".pack") {
		return "", false
	}
	id := strings.TrimSuffix(strings.TrimPrefix(key, prefix), ".pack")
	if id == "" || strings.Contains(id, "/") {
		return "", false
	}
	return id, true
}

// locmapNamespace parses backups/<namespace>/manifests/<id>.locmap.
func locmapNamespace(key string) (string, bool) {
	if !strings.HasSuffix(key, ".locmap") {
		return "", false
	}
	parts := strings.Split(key, "/")
	if len(parts) != 4 || parts[0] != "backups" || parts[2] != "manifests" || parts[1] == "packs" {
		return "", false
	}
	return parts[1], true
}

func isManifestKey(key string) bool {
	parts := strings.Split(key, "/")
	return len(parts) == 4 && parts[0] == "backups" && parts[2] == "manifests" &&
		(strings.HasSuffix(key, ".snap") || strings.HasSuffix(key, ".locmap"))
}

// otherPrefix groups a non-V2 object by its first two path segments.
func otherPrefix(key string) string {
	parts := strings.SplitN(key, "/", 3)
	if len(parts) >= 2 {
		return parts[0] + "/" + parts[1]
	}
	return parts[0]
}

// LocalTarget, MemTarget and TransportTarget can all clean up.
var (
	_ RemoteCleaner = LocalTarget{}
	_ RemoteCleaner = (*MemTarget)(nil)
	_ RemoteCleaner = TransportTarget{}
)

func (t LocalTarget) Delete(_ context.Context, key string) error {
	p, err := t.path(key)
	if err != nil {
		return err
	}
	if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func (t LocalTarget) List(_ context.Context, prefix string) ([]ObjectInfo, error) {
	var out []ObjectInfo
	root := filepath.Clean(t.Root)
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		if d.IsDir() || filepath.Ext(path) == ".tmp" {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		key := filepath.ToSlash(rel)
		if !strings.HasPrefix(key, prefix) {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		out = append(out, ObjectInfo{Key: key, Size: info.Size()})
		return nil
	})
	return out, err
}

func (m *MemTarget) Delete(_ context.Context, key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.objects, key)
	return nil
}

func (m *MemTarget) List(_ context.Context, prefix string) ([]ObjectInfo, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []ObjectInfo
	for k, v := range m.objects {
		if strings.HasPrefix(k, prefix) {
			out = append(out, ObjectInfo{Key: k, Size: int64(len(v))})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out, nil
}

// ClearSweep forgets pending sweep candidates, for example after the target
// was wiped.
func (r *Repository) ClearSweep(targetKey string) {
	_ = r.saveSweepCandidates(targetKey, nil)
}
