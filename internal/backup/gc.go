package backup

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// RetentionPolicy keeps a number of daily, weekly, and monthly restore points.
// Retention operates on restore points (manifests), never directly on physical
// chunks. Expiring a restore point removes its manifest; shared chunks are only
// reclaimed later by garbage collection once proven unreferenced.
type RetentionPolicy struct {
	Daily   int
	Weekly  int
	Monthly int
}

// SelectExpired returns the restore points to expire for one namespace. It
// follows the usual daily/weekly/monthly rotation: rules are applied in that
// order, each keeping the newest point of its most recent buckets, and a
// bucket already covered by a point kept under an earlier rule is skipped
// rather than kept twice. Keep 1 daily, 1 weekly and 1 monthly therefore keeps
// today's point, the newest point of an earlier week and the newest point of
// an earlier month, not three points from the same week. The newest point is
// always kept, so retention can never remove the last restore point.
func (p RetentionPolicy) SelectExpired(states []PointState) []PointState {
	pts := append([]PointState(nil), states...)
	sort.Slice(pts, func(i, j int) bool { return pts[i].CreatedAtNS > pts[j].CreatedAtNS })
	times := make([]time.Time, len(pts))
	for i, s := range pts {
		times[i] = time.Unix(0, s.CreatedAtNS).UTC()
	}
	keepIdx := RetainNewest(times, p.Daily, p.Weekly, p.Monthly)
	var expired []PointState
	for i, s := range pts {
		if !keepIdx[i] {
			expired = append(expired, s)
		}
	}
	return expired
}

// RetainNewest applies daily, weekly and monthly rotation to times, which must
// be sorted newest first, and returns the indexes to keep. It is shared by the
// engine and the control plane so both enforce the same policy. The newest
// entry is always kept.
func RetainNewest(times []time.Time, daily, weekly, monthly int) map[int]bool {
	keep := map[int]bool{}
	if len(times) == 0 {
		return keep
	}
	rules := []struct {
		count  int
		bucket func(time.Time) string
	}{
		{daily, func(t time.Time) string { return t.Format("2006-01-02") }},
		{weekly, func(t time.Time) string {
			y, w := t.ISOWeek()
			return isoWeekKey(y, w)
		}},
		{monthly, func(t time.Time) string { return t.Format("2006-01") }},
	}
	for _, rule := range rules {
		if rule.count <= 0 {
			continue
		}
		covered := map[string]bool{}
		for i := range times {
			if keep[i] {
				covered[rule.bucket(times[i])] = true
			}
		}
		kept := 0
		for i, t := range times {
			if kept >= rule.count {
				break
			}
			if keep[i] {
				continue
			}
			b := rule.bucket(t)
			if covered[b] {
				continue
			}
			covered[b] = true
			keep[i] = true
			kept++
		}
	}
	keep[0] = true
	return keep
}

func isoWeekKey(year, week int) string {
	return time.Date(year, 1, 1, 0, 0, 0, 0, time.UTC).Format("2006") + "-W" + pad2(week)
}

func pad2(n int) string {
	if n < 10 {
		return "0" + itoa(n)
	}
	return itoa(n)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

// DeleteRestorePoint removes a restore point's manifest and state sidecar. It
// does not touch chunks; run CollectGarbage afterwards to reclaim space.
func (r *Repository) DeleteRestorePoint(namespace, backupID string) error {
	dir := filepath.Join(r.root, "snapshots", namespace)
	_ = os.Remove(filepath.Join(dir, backupID+".snap"))
	r.invalidateManifest(namespace, backupID)
	return os.Remove(filepath.Join(dir, backupID+".state"))
}

// GCStats reports the outcome of a collection.
type GCStats struct {
	ReferencedChunks int   `json:"referenced_chunks"`
	PacksScanned     int   `json:"packs_scanned"`
	PacksDeleted     int   `json:"packs_deleted"`
	BytesFreed       int64 `json:"bytes_freed"`
	PacksRepacked    int   `json:"packs_repacked,omitempty"`
	BytesRepacked    int64 `json:"bytes_repacked,omitempty"`
	// DeadBytes is unreferenced data still held inside packs that also hold
	// live chunks (not repacked this time).
	DeadBytes     int64  `json:"dead_bytes,omitempty"`
	CachesRemoved int    `json:"caches_removed,omitempty"`
	RepackSkipped string `json:"repack_skipped,omitempty"`
}

// GCOptions tunes a collection.
type GCOptions struct {
	// RepackLiveRatio rewrites a pack whose live bytes are below this
	// fraction of its size, copying the live chunks into a new pack and
	// deleting the old one. Zero only deletes fully dead packs.
	RepackLiveRatio float64
	// ReserveBytes is the free space repacking must leave on the
	// repository filesystem. Repacking is skipped rather than allowed to
	// cross it.
	ReserveBytes int64
}

// DefaultRepackLiveRatio repacks packs that are more than half dead.
const DefaultRepackLiveRatio = 0.5

// CollectGarbage removes packs whose chunks are entirely unreferenced by any
// remaining manifest. A pack is deleted only when every chunk it contains is
// proven to have no live reference, so a chunk shared by another restore point
// always survives. The operation is idempotent and safe to re-run after an
// interruption.
func (r *Repository) CollectGarbage() (GCStats, error) {
	return r.CollectGarbageWith(GCOptions{})
}

// CollectGarbageWith collects garbage and, when opts.RepackLiveRatio is set,
// compacts mostly-dead packs. Without compaction a pack keeps every byte on
// disk as long as a single chunk in it is still referenced, so expiring
// restore points frees far less space than it should.
//
// Compaction is crash safe: the new pack and its index are committed before
// the old pack is removed, and a chunk present in two packs is harmless.
func (r *Repository) CollectGarbageWith(opts GCOptions) (GCStats, error) {
	r.capture.Lock()
	defer r.capture.Unlock()
	var stats GCStats
	referenced, err := r.referencedChunks()
	if err != nil {
		return stats, err
	}
	stats.ReferencedChunks = len(referenced)
	packs, err := r.packList()
	if err != nil {
		return stats, err
	}
	type candidate struct {
		pi   packIndex
		live int64
	}
	var repack []candidate
	var repackLive int64
	for _, packID := range packs {
		stats.PacksScanned++
		pi, err := r.readPackIndex(packID)
		if err != nil {
			return stats, err
		}
		var live int64
		seen := map[KeyID]bool{}
		for _, ent := range pi.Entries {
			if _, ok := referenced[ent.ID]; ok && !seen[ent.ID] {
				seen[ent.ID] = true
				live += int64(ent.Len)
			}
		}
		if live == 0 {
			if err := r.deletePack(packID); err != nil {
				return stats, err
			}
			stats.PacksDeleted++
			stats.BytesFreed += pi.Size
			continue
		}
		if opts.RepackLiveRatio > 0 && pi.Size > 0 && float64(live) < opts.RepackLiveRatio*float64(pi.Size) {
			repack = append(repack, candidate{pi: pi, live: live})
			repackLive += live
			continue
		}
		stats.DeadBytes += pi.Size - live
	}
	if len(repack) > 0 {
		free, ferr := hostFreeBytes(r.root)
		switch {
		case ferr != nil:
			stats.RepackSkipped = "free space is unknown: " + ferr.Error()
		case free-repackLive < opts.ReserveBytes:
			stats.RepackSkipped = fmt.Sprintf("repacking needs %d bytes but only %d are free above the %d byte reserve", repackLive, free-opts.ReserveBytes, opts.ReserveBytes)
		}
		if stats.RepackSkipped != "" {
			for _, c := range repack {
				stats.DeadBytes += c.pi.Size - c.live
			}
		} else {
			pw := r.newPackWriter(DefaultPackTarget)
			pw.rewrite = true
			for _, c := range repack {
				if err := r.copyLive(pw, c.pi, referenced); err != nil {
					return stats, err
				}
			}
			if err := pw.flush(); err != nil {
				return stats, err
			}
			for _, c := range repack {
				if err := r.deletePack(c.pi.Pack); err != nil {
					return stats, err
				}
				stats.PacksRepacked++
				stats.BytesRepacked += c.live
				stats.BytesFreed += c.pi.Size - c.live
			}
		}
	}
	stats.CachesRemoved = r.removeOrphanCaches()
	return stats, nil
}

// copyLive copies the referenced chunks of one pack into pw.
func (r *Repository) copyLive(pw *packWriter, pi packIndex, referenced map[KeyID]struct{}) error {
	f, err := os.Open(filepath.Join(r.packDir(), pi.Pack+".pack"))
	if err != nil {
		return err
	}
	defer f.Close()
	for _, ent := range pi.Entries {
		if _, ok := referenced[ent.ID]; !ok {
			continue
		}
		buf := make([]byte, ent.Len)
		if _, err := f.ReadAt(buf, int64(ent.Off)); err != nil {
			return fmt.Errorf("pack %s: %w", pi.Pack, err)
		}
		if _, err := pw.add(ent.ID, buf); err != nil {
			return err
		}
	}
	return nil
}

// removeOrphanCaches deletes metadata caches of namespaces that no longer
// have any restore point. Caches only speed up the next capture.
func (r *Repository) removeOrphanCaches() int {
	entries, err := os.ReadDir(filepath.Join(r.root, "cache"))
	if err != nil {
		return 0
	}
	n := 0
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".cache" {
			continue
		}
		ns := strings.TrimSuffix(e.Name(), ".cache")
		snaps, _ := filepath.Glob(filepath.Join(r.root, "snapshots", ns, "*.snap"))
		if len(snaps) > 0 {
			continue
		}
		if os.Remove(filepath.Join(r.root, "cache", e.Name())) == nil {
			n++
		}
	}
	return n
}

// referencedChunks unions the chunk ids of every remaining manifest across all
// namespaces, including restore points that are only local and those pending or
// completed upload.
func (r *Repository) referencedChunks() (map[KeyID]struct{}, error) {
	ref := map[KeyID]struct{}{}
	base := filepath.Join(r.root, "snapshots")
	nsDirs, err := os.ReadDir(base)
	if err != nil {
		return nil, err
	}
	for _, ns := range nsDirs {
		if !ns.IsDir() {
			continue
		}
		files, err := os.ReadDir(filepath.Join(base, ns.Name()))
		if err != nil {
			return nil, err
		}
		for _, f := range files {
			if filepath.Ext(f.Name()) != ".snap" {
				continue
			}
			backupID := f.Name()[:len(f.Name())-5]
			m, err := r.LoadManifest(ns.Name(), backupID)
			if err != nil {
				// Never guess: a manifest that cannot be read may reference
				// any chunk, so collection stops instead of deleting data.
				return nil, fmt.Errorf("restore point %s/%s cannot be read, so no backup data was removed: %w", ns.Name(), backupID, err)
			}
			for _, fe := range m.Files {
				for _, id := range fe.Chunks {
					ref[id] = struct{}{}
				}
			}
		}
	}
	return ref, nil
}
