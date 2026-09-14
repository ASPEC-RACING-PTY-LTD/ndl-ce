package appdb

import (
	"strings"
	"time"

	"github.com/no-dal/ndl-ce/internal/storage"
)

const (
	// OrphanAllocMax is the largest allocated size we treat as an empty
	// leftover dest (live orphans were about 1 KiB).
	OrphanAllocMax int64 = 64 << 10
)

// OrphanVolume is a migration-created dest that is safe to remove.
type OrphanVolume struct {
	Volume Volume
	Reason string
	JobID  string
}

// VolumeFacts is the catalog picture used to classify leftover dest volumes.
type VolumeFacts struct {
	Volumes  []Volume
	Attached map[string]bool
	Jobs     []MigrationJob
	Now      time.Time
}

// ClassifyOrphanVolumes returns No-dal-owned leftover dests only.
// User-owned and attached volumes are never returned.
func ClassifyOrphanVolumes(facts VolumeFacts) []OrphanVolume {
	attached := facts.Attached
	if attached == nil {
		attached = map[string]bool{}
	}
	var out []OrphanVolume
	for _, v := range facts.Volumes {
		if attached[v.ID] {
			continue
		}
		if strings.EqualFold(v.OwnerKind, storage.VolumeKindOperator) {
			continue
		}
		if v.Class != storage.ClassContainerRoot && v.Class != storage.ClassVMDisk {
			continue
		}
		if strings.EqualFold(v.OwnerKind, storage.VolumeKindMigration) && strings.EqualFold(v.Owner, storage.VolumeOwnerName) {
			if jobProtectsVolume(facts.Jobs, v) {
				continue
			}
			out = append(out, OrphanVolume{Volume: v, Reason: "unattached migration-owned volume", JobID: v.OwnerJobID})
			continue
		}
		if v.OwnerKind != "" {
			continue
		}
		if !legacyEmptyDest(v) {
			continue
		}
		jobID, ok := legacyMigrationWindow(v, facts.Jobs)
		if !ok {
			continue
		}
		out = append(out, OrphanVolume{
			Volume: v,
			Reason: "unattached empty dest created during a failed or canceled migration",
			JobID:  jobID,
		})
	}
	return out
}

func legacyEmptyDest(v Volume) bool {
	if v.Class != storage.ClassContainerRoot {
		return false
	}
	if v.AllocatedBytes == nil {
		return false
	}
	return *v.AllocatedBytes <= OrphanAllocMax
}

func jobProtectsVolume(jobs []MigrationJob, v Volume) bool {
	for _, j := range jobs {
		if !strings.EqualFold(j.State, OpStateSucceeded) {
			continue
		}
		if v.OwnerJobID != "" && v.OwnerJobID == j.ID {
			return true
		}
	}
	return false
}

func legacyMigrationWindow(v Volume, jobs []MigrationJob) (string, bool) {
	for _, j := range jobs {
		if jobTerminalState(j.State) == OpStateSucceeded {
			continue
		}
		if j.Direction != "" && !strings.EqualFold(j.Direction, "import") {
			continue
		}
		start := j.CreatedAt.Add(-2 * time.Minute)
		end := j.UpdatedAt.Add(45 * time.Minute)
		if end.Before(j.CreatedAt) {
			end = j.CreatedAt.Add(45 * time.Minute)
		}
		if v.CreatedAt.Before(start) || v.CreatedAt.After(end) {
			continue
		}
		return j.ID, true
	}
	return "", false
}
