package appdb

import (
	"context"
	"encoding/json"
	"time"

	"github.com/no-dal/ndl-ce/internal/lxc"
)

// Workload is desired existence plus observed availability.
// Paths, lxc names, and volume backend_ref are locators. ID is the UUID.
type Workload struct {
	ID              string
	ClusterID       string
	NodeID          string
	OwnerNodeID     string
	DesiredNodeID   string
	Name            string
	Kind            string
	Status          string
	Reason          string
	DesiredPower    string
	ImagePin        string
	ImageVerified   bool
	CPUs            int
	MemoryBytes     int64
	Privileged      bool
	UIDMap          string
	GIDMap          string
	PID             *int
	UnitActive      bool
	OwnershipEpoch  int
	MigrateReady    bool
	MigrateBlockers json.RawMessage
	Devices         json.RawMessage
	Warnings        []string
	IdempotencyKey  string
	SpecJSON        json.RawMessage
	AppliedJSON     json.RawMessage
	Autostart       bool
	PendingRestart  bool
	Firmware        string
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

// WorkloadDisk attaches a volume. VolumeID is the identity of the volume.
type WorkloadDisk struct {
	ID         string
	ClusterID  string
	WorkloadID string
	VolumeID   string
	Role       string
	Slot       int
	BusAddr    string
	ReadOnly   bool
	Format     string
	CreatedAt  time.Time
}

// WorkloadNIC attaches a network. MAC is allocated from the workload UUID.
type WorkloadNIC struct {
	ID         string
	ClusterID  string
	WorkloadID string
	NetworkID  string
	MAC        string
	IPv4       string
	PCIAddr    string
	Model      string
	CreatedAt  time.Time
}

// VMCidata is managed NoCloud seed metadata. Secrets are never stored here.
type VMCidata struct {
	WorkloadID  string
	ClusterID   string
	UserDataSHA string
	HasPassword bool
	UpdatedAt   time.Time
}

// VMFirmware records per-VM firmware vars locators, not product identity.
type VMFirmware struct {
	WorkloadID string
	ClusterID  string
	Mode       string
	VarsRef    string
	UpdatedAt  time.Time
}

func WorkloadHints(items []Workload) []lxc.Hint {
	out := make([]lxc.Hint, 0, len(items))
	for _, w := range items {
		out = append(out, lxc.Hint{WorkloadID: w.ID, Kind: w.Kind})
	}
	return out
}

// ReconcileWorkloads updates availability. It never deletes desired rows.
func ReconcileWorkloads(ctx context.Context, st Store, clusterID string, desired []Workload, obs lxc.Observation) (unavailable, recovered []string, err error) {
	seen := map[string]lxc.Observed{}
	for _, item := range obs.Workloads {
		seen[item.WorkloadID] = item
	}
	for _, w := range desired {
		cur, ok := seen[w.ID]
		next := w
		if !ok {
			next.Status = lxc.StatusUnavailable
			next.Reason = "workload was not observed"
			next.UnitActive = false
			next.PID = nil
		} else {
			next.Status = cur.Status
			next.Reason = cur.Reason
			next.UnitActive = cur.UnitActive
			next.ImageVerified = cur.ImageVerified || w.ImageVerified
			next.Warnings = cur.Warnings
			if cur.PID > 0 {
				pid := cur.PID
				next.PID = &pid
			} else {
				next.PID = nil
			}
			blockers, _ := json.Marshal(cur.MigrateBlockers)
			if len(blockers) > 0 {
				next.MigrateBlockers = blockers
			}
			next.MigrateReady = cur.MigrateReady
		}
		if w.Status != lxc.StatusUnavailable && next.Status == lxc.StatusUnavailable {
			unavailable = append(unavailable, w.ID)
		}
		if w.Status == lxc.StatusUnavailable && next.Status != lxc.StatusUnavailable && next.Status != "" {
			recovered = append(recovered, w.ID)
		}
		if err := st.UpdateWorkloadObserved(ctx, next); err != nil {
			return unavailable, recovered, err
		}
		if ok && cur.IPv4 != "" {
			nics, nerr := st.ListWorkloadNICs(ctx, clusterID, w.ID)
			if nerr != nil {
				return unavailable, recovered, nerr
			}
			for _, nic := range nics {
				if nic.IPv4 != cur.IPv4 {
					nic.IPv4 = cur.IPv4
					if err := st.UpdateWorkloadNIC(ctx, nic); err != nil {
						return unavailable, recovered, err
					}
				}
			}
		}
	}
	return unavailable, recovered, nil
}
