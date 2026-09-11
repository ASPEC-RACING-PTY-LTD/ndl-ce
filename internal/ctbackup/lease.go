package ctbackup

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	FreezeReasonBackup = "directory-backup"
	// MaxFreeze is the longest a No-DAL backup may hold a guest freezer.
	// A crash watchdog and agent startup recovery thaw owned leases past this.
	DefaultMaxFreeze = 30 * time.Minute
)

var (
	freezeLeaseDir = "/run/ndl/backup-freeze"
	maxFreeze      = DefaultMaxFreeze
)

type freezeLease struct {
	Unit       string    `json:"unit"`
	WorkloadID string    `json:"workload_id"`
	FrozenAt   time.Time `json:"frozen_at"`
	Reason     string    `json:"reason"`
}

func leasePath(unit string) string {
	id := freezeUnitID(unit)
	return filepath.Join(freezeLeaseDir, id+".json")
}

func writeFreezeLease(unit string) error {
	if err := ValidateFreezeUnit(unit); err != nil {
		return err
	}
	if err := os.MkdirAll(freezeLeaseDir, 0o750); err != nil {
		return err
	}
	body, err := json.Marshal(freezeLease{
		Unit:       unit,
		WorkloadID: freezeUnitID(unit),
		FrozenAt:   time.Now().UTC(),
		Reason:     FreezeReasonBackup,
	})
	if err != nil {
		return err
	}
	return os.WriteFile(leasePath(unit), body, 0o600)
}

func removeFreezeLease(unit string) {
	_ = os.Remove(leasePath(unit))
}

func readFreezeLease(path string) (freezeLease, error) {
	var lease freezeLease
	body, err := os.ReadFile(path)
	if err != nil {
		return lease, err
	}
	if err := json.Unmarshal(body, &lease); err != nil {
		return lease, err
	}
	return lease, nil
}

func thawOwnedLease(lease freezeLease) {
	if lease.Reason != FreezeReasonBackup {
		return
	}
	if ValidateFreezeUnit(lease.Unit) != nil {
		return
	}
	for _, path := range existingFreezePaths(lease.Unit) {
		_ = os.WriteFile(path, []byte("0\n"), 0o644)
	}
	removeFreezeLease(lease.Unit)
}

// RecoverOwnedFreezes thaws cgroups that still have a No-DAL backup lease.
// Freezes without a lease are left alone.
func RecoverOwnedFreezes() int {
	entries, err := os.ReadDir(freezeLeaseDir)
	if err != nil {
		if os.IsNotExist(err) {
			return 0
		}
		return 0
	}
	n := 0
	for _, ent := range entries {
		if ent.IsDir() || !strings.HasSuffix(ent.Name(), ".json") {
			continue
		}
		lease, err := readFreezeLease(filepath.Join(freezeLeaseDir, ent.Name()))
		if err != nil {
			continue
		}
		thawOwnedLease(lease)
		n++
	}
	return n
}

func recoverExpiredFreezes(now time.Time) int {
	entries, err := os.ReadDir(freezeLeaseDir)
	if err != nil {
		return 0
	}
	n := 0
	for _, ent := range entries {
		if ent.IsDir() || !strings.HasSuffix(ent.Name(), ".json") {
			continue
		}
		lease, err := readFreezeLease(filepath.Join(freezeLeaseDir, ent.Name()))
		if err != nil {
			continue
		}
		if lease.FrozenAt.IsZero() || now.Sub(lease.FrozenAt) < maxFreeze {
			continue
		}
		thawOwnedLease(lease)
		n++
	}
	return n
}

// WatchOwnedFreezes thaws backup leases that outlive maxFreeze.
func WatchOwnedFreezes(stop <-chan struct{}) {
	t := time.NewTicker(5 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-stop:
			return
		case now := <-t.C:
			_ = recoverExpiredFreezes(now.UTC())
		}
	}
}
