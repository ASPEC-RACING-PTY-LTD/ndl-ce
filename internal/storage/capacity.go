package storage

import "fmt"

// PhysicalReserveBytes is the free-space warning threshold. Sparse logical
// size is not reserved; this only reflects real filesystem headroom.
const PhysicalReserveBytes = 1 << 30

const (
	WarnPhysicalLow    = "physical_low"
	PhysicalLowMessage = "Physical free space is low. Sparse volume limits are not a reservation; writes that need new blocks can fail when the filesystem fills."
)

// AdmitPhysicalFree allows sparse/thin volume create and grow when the
// backing filesystem still has the minimum free blocks. Logical size is not
// compared to free space.
func AdmitPhysicalFree(usable *int64) error {
	if usable != nil && *usable < MinPoolFreeBytes {
		return ErrCapacity
	}
	return nil
}

// PhysicalUsedBytes is StatFS used space (total minus available). Nil when
// either side is missing so unavailable pools do not invent zero.
func PhysicalUsedBytes(c Capacity) *int64 {
	if c.TotalBytes == nil || c.UsableBytes == nil {
		return nil
	}
	used := *c.TotalBytes - *c.UsableBytes
	if used < 0 {
		used = 0
	}
	return &used
}

func thinOvercommitMessage(provisioned, total int64) string {
	return fmt.Sprintf("Logical provisioned %s / Physical capacity %s. Sparse volumes share physical space and are not a reservation.", formatCap(provisioned), formatCap(total))
}

func formatCap(n int64) string {
	const gi = 1 << 30
	if n >= gi {
		return fmt.Sprintf("%.0f GiB", float64(n)/float64(gi))
	}
	const mi = 1 << 20
	if n >= mi {
		return fmt.Sprintf("%.0f MiB", float64(n)/float64(mi))
	}
	return fmt.Sprintf("%d B", n)
}

func applyCapacityWarnings(obs *ObservedPool, prov int64) {
	if obs == nil || obs.Status == StatusUnavailable {
		return
	}
	if obs.Capacity.TotalBytes != nil && prov > *obs.Capacity.TotalBytes && *obs.Capacity.TotalBytes > 0 {
		if !containsWarning(obs.Warnings, WarnThinOvercommit) {
			obs.Warnings = append(obs.Warnings, WarnThinOvercommit)
			obs.WarningText = append(obs.WarningText, thinOvercommitMessage(prov, *obs.Capacity.TotalBytes))
		}
		if obs.Status == StatusAvailable {
			obs.Status = StatusWarning
		}
	}
	if obs.Capacity.UsableBytes != nil && *obs.Capacity.UsableBytes < PhysicalReserveBytes {
		if !containsWarning(obs.Warnings, WarnPhysicalLow) {
			obs.Warnings = append(obs.Warnings, WarnPhysicalLow)
			obs.WarningText = append(obs.WarningText, PhysicalLowMessage)
		}
		if obs.Status == StatusAvailable {
			obs.Status = StatusWarning
		}
	}
}

func containsWarning(list []string, code string) bool {
	for _, w := range list {
		if w == code {
			return true
		}
	}
	return false
}
