package backup

import (
	"os"
	"path/filepath"
	"sort"
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

// SelectExpired returns the backup ids to expire for one namespace given the
// policy. The newest point in each active bucket is kept.
func (p RetentionPolicy) SelectExpired(states []PointState) []PointState {
	pts := append([]PointState(nil), states...)
	sort.Slice(pts, func(i, j int) bool { return pts[i].CreatedAtNS > pts[j].CreatedAtNS })

	keep := map[string]bool{}
	markNewestPerBucket := func(count int, bucket func(time.Time) string) {
		if count <= 0 {
			return
		}
		seen := map[string]bool{}
		kept := 0
		for _, s := range pts {
			b := bucket(time.Unix(0, s.CreatedAtNS).UTC())
			if seen[b] {
				continue
			}
			seen[b] = true
			keep[s.BackupID] = true
			kept++
			if kept >= count {
				break
			}
		}
	}
	markNewestPerBucket(p.Daily, func(t time.Time) string { return t.Format("2006-01-02") })
	markNewestPerBucket(p.Weekly, func(t time.Time) string {
		y, w := t.ISOWeek()
		return isoWeekKey(y, w)
	})
	markNewestPerBucket(p.Monthly, func(t time.Time) string { return t.Format("2006-01") })

	var expired []PointState
	for _, s := range pts {
		if !keep[s.BackupID] {
			expired = append(expired, s)
		}
	}
	return expired
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
	return os.Remove(filepath.Join(dir, backupID+".state"))
}

// GCStats reports the outcome of a collection.
type GCStats struct {
	ReferencedChunks int
	PacksScanned     int
	PacksDeleted     int
	BytesFreed       int64
}

// CollectGarbage removes packs whose chunks are entirely unreferenced by any
// remaining manifest. A pack is deleted only when every chunk it contains is
// proven to have no live reference, so a chunk shared by another restore point
// always survives. The operation is idempotent and safe to re-run after an
// interruption.
func (r *Repository) CollectGarbage() (GCStats, error) {
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
	for _, packID := range packs {
		stats.PacksScanned++
		pi, err := r.readPackIndex(packID)
		if err != nil {
			return stats, err
		}
		used := false
		for _, ent := range pi.Entries {
			if _, ok := referenced[ent.ID]; ok {
				used = true
				break
			}
		}
		if used {
			continue
		}
		if err := r.deletePack(packID); err != nil {
			return stats, err
		}
		stats.PacksDeleted++
		stats.BytesFreed += pi.Size
	}
	return stats, nil
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
				return nil, err
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
