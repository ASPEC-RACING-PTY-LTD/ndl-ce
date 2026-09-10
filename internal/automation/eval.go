package automation

import (
	"github.com/no-dal/ndl-ce/internal/appdb"
)

// UsedPercent is physical filesystem used over physical total. Logical
// provisioned volume size is not a reservation and is not used here.
func UsedPercent(total, usable *int64) (int, bool) {
	if total == nil || usable == nil || *total <= 0 {
		return 0, false
	}
	used := *total - *usable
	if used < 0 {
		used = 0
	}
	pct := int((used * 100) / *total)
	if pct < 0 {
		pct = 0
	}
	if pct > 100 {
		pct = 100
	}
	return pct, true
}

// Candidate is a low-priority workload selected for a queued migrate.
type Candidate struct {
	WorkloadID string
	Name       string
	Priority   int
}

// SelectLowPriority returns VM workloads on pool, lowest placement priority first.
func SelectLowPriority(poolID string, workloads []appdb.Workload, disks []appdb.WorkloadDisk, volumes []appdb.Volume, placements map[string]appdb.WorkloadPlacement) []Candidate {
	onPool := map[string]struct{}{}
	for _, v := range volumes {
		if v.PoolID == poolID {
			onPool[v.ID] = struct{}{}
		}
	}
	wlOnPool := map[string]struct{}{}
	for _, d := range disks {
		if _, ok := onPool[d.VolumeID]; ok {
			wlOnPool[d.WorkloadID] = struct{}{}
		}
	}
	var out []Candidate
	for _, w := range workloads {
		if _, ok := wlOnPool[w.ID]; !ok {
			continue
		}
		if w.Kind != "vm" {
			continue
		}
		prio := 0
		if p, ok := placements[w.ID]; ok {
			prio = p.Priority
		}
		out = append(out, Candidate{WorkloadID: w.ID, Name: w.Name, Priority: prio})
	}
	for i := 0; i < len(out); i++ {
		for j := i + 1; j < len(out); j++ {
			if out[j].Priority < out[i].Priority {
				out[i], out[j] = out[j], out[i]
			}
		}
	}
	return out
}
