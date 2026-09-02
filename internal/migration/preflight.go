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
}

func Preflight(item ItemPlan, caps Caps, env PreflightEnv) error {
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
	if !env.NameAvailable {
		return fmt.Errorf("destination workload name is already in use")
	}
	if !env.ToolsOK {
		return fmt.Errorf("required conversion tools are unavailable")
	}
	if !env.StagingOK {
		return fmt.Errorf("migration staging capacity is insufficient")
	}
	if item.Compatibility == CompatBlocked || item.Compatibility == CompatUnsupported {
		return fmt.Errorf("compatibility is %s", item.Compatibility)
	}
	if item.Compatibility == CompatRequiresMapping {
		return fmt.Errorf("required mappings are incomplete")
	}
	if item.StartAfter && env.SourceRunning && !item.IdentityConflictAck {
		return fmt.Errorf("NETWORK IDENTITY CONFLICT. The source workload appears to remain online and the destination may retain the same MAC address. Starting both may cause network conflicts")
	}
	return nil
}

func NoSilentFallback(requested, available string) error {
	if requested != available {
		return fmt.Errorf("selected mode %s cannot be performed; No-dal will not silently fall back to %s", requested, available)
	}
	return nil
}
