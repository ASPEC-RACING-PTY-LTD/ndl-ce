package migration

import (
	"fmt"
	"strings"
)

type PreflightEnv struct {
	SourceExists   bool
	SourceRunning  bool
	CredentialsOK  bool
	DestPoolExists bool
	DestCapacityOK bool
	DestNetExists  bool
	NameAvailable  bool
	ToolsOK        bool
	StagingOK      bool
	DestPoolBytes  int64
	EstimatedBytes int64
	// Negative gates default to false so existing callers stay valid.
	ArchiveUnreadable  bool
	ArchiveUnsupported bool
	PartialDestUnsafe  bool
	DuplicateName      bool
	DuplicateDestID    bool
	ArchiveReason      string
}

func Preflight(item ItemPlan, caps Caps, env PreflightEnv) error {
	if item.Compatibility == CompatBlocked || item.Compatibility == CompatUnsupported {
		for _, f := range item.Findings {
			if f.Level == CompatBlocked || f.Level == CompatUnsupported {
				return fmt.Errorf("%s", f.Message)
			}
		}
		return fmt.Errorf("compatibility is %s", item.Compatibility)
	}
	if strings.TrimSpace(item.Mode) == "" {
		return fmt.Errorf("migration mode must be selected by the operator")
	}
	ok, reason := caps.ModeAvailable(item.Mode)
	if !ok {
		return fmt.Errorf("%s", reason)
	}
	if !env.SourceExists {
		return fmt.Errorf("source no longer exists")
	}
	if !env.CredentialsOK {
		return fmt.Errorf("source credentials are no longer valid")
	}
	if item.Mode == ModeOffline && env.SourceRunning {
		return fmt.Errorf("Offline migration requires a stopped source. Stop the workload on the source, then retry. No-dal will not stop it for you")
	}
	if item.Mode == ModeLocal && env.SourceRunning {
		return fmt.Errorf("Local Host Migration requires a stopped LXC. Stop it on Proxmox, then retry. No-dal will not stop it")
	}
	if item.Mode == ModeLive && !item.LiveAck {
		return fmt.Errorf("Live migration requires explicit acknowledgement of risk")
	}
	if item.Mode == ModeLive && !caps.Live {
		return fmt.Errorf("Live is unavailable. Source storage does not expose the capabilities required for live transfer")
	}
	if item.Mode == ModeSnapshot && !caps.Snapshot {
		return fmt.Errorf("%s", firstNonEmpty(caps.SnapshotNote, "Snapshot-assisted migration requires a source snapshot capability"))
	}
	if !env.DestPoolExists {
		return fmt.Errorf("destination storage does not exist")
	}
	if !env.DestCapacityOK || (env.DestPoolBytes > 0 && env.EstimatedBytes > env.DestPoolBytes) {
		return fmt.Errorf("destination storage has insufficient capacity")
	}
	if !env.DestNetExists {
		return fmt.Errorf("destination network does not exist")
	}
	if env.ArchiveUnsupported {
		return fmt.Errorf("%s", firstNonEmpty(env.ArchiveReason, "archive type is not supported"))
	}
	if env.ArchiveUnreadable {
		return fmt.Errorf("%s", firstNonEmpty(env.ArchiveReason, "backup or archive path is not readable on this host"))
	}
	if env.PartialDestUnsafe {
		return fmt.Errorf("a partial or failed destination already exists for %s. Remove that workload and its volumes; No-dal will not reuse a corrupt import", item.Name)
	}
	if env.DuplicateName {
		return fmt.Errorf("duplicate destination name %s in this bulk plan", item.Name)
	}
	if env.DuplicateDestID {
		return fmt.Errorf("duplicate destination identity in this bulk plan")
	}
	if !env.NameAvailable {
		return fmt.Errorf("destination workload name is already in use")
	}
	if !env.ToolsOK {
		return fmt.Errorf("required conversion tools are unavailable")
	}
	if !env.StagingOK {
		return fmt.Errorf("migration staging capacity is insufficient")
	}
	if item.Compatibility == CompatRequiresMapping {
		return fmt.Errorf("required mappings are incomplete")
	}
	if item.StartAfter && env.SourceRunning && !item.IdentityConflictAck {
		return fmt.Errorf("NETWORK IDENTITY CONFLICT. The source workload appears to remain online and the destination may retain the same MAC address. Starting both may cause network conflicts")
	}
	return nil
}

// CheckDuplicateDests reports colliding destination names or source IDs in a bulk plan.
func CheckDuplicateDests(items []ItemPlan) error {
	names := map[string]string{}
	ids := map[string]struct{}{}
	for _, item := range items {
		name := strings.ToLower(strings.TrimSpace(item.Name))
		if name != "" {
			if prev, ok := names[name]; ok {
				return fmt.Errorf("duplicate destination name %s (sources %s and %s)", item.Name, prev, item.SourceID)
			}
			names[name] = item.SourceID
		}
		if item.SourceID == "" {
			continue
		}
		if _, ok := ids[item.SourceID]; ok {
			return fmt.Errorf("duplicate destination identity %s in this bulk plan", item.SourceID)
		}
		ids[item.SourceID] = struct{}{}
	}
	return nil
}

func ArchiveSupported(path, format string) (ok bool, reason string) {
	low := strings.ToLower(path + " " + format)
	if IsVMA(path) || strings.Contains(low, ".vma") || strings.EqualFold(strings.TrimSpace(format), "vma") {
		return false, "VM vma vzdump is not supported"
	}
	if strings.Contains(low, "pbs:") || strings.EqualFold(strings.TrimSpace(format), "pbs") {
		return false, "Proxmox Backup Server archives cannot be imported over the content API"
	}
	return true, ""
}

func DestLooksPartial(status, imagePin string, verified bool) bool {
	st := strings.ToLower(strings.TrimSpace(status))
	pin := strings.ToLower(strings.TrimSpace(imagePin))
	if pin != "imported" {
		return false
	}
	if st == "failed" || st == "unavailable" || st == "error" || strings.Contains(st, "fail") {
		return true
	}
	return !verified && (st == "" || st == "creating" || st == "pending")
}

func CompletedReport(reports []Report, sourceID, name string) (Report, bool) {
	for _, r := range reports {
		if sourceID != "" && r.SourceID == sourceID && r.WorkloadID != "" {
			return r, true
		}
		if name != "" && strings.EqualFold(r.Name, name) && r.WorkloadID != "" && r.SourceID == sourceID {
			return r, true
		}
	}
	return Report{}, false
}

func NoSilentFallback(requested, available string) error {
	if requested != available {
		return fmt.Errorf("selected mode %s cannot be performed; No-dal will not silently fall back to %s", requested, available)
	}
	return nil
}
