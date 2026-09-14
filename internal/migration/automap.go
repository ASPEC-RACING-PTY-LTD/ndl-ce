package migration

import (
	"fmt"
	"strconv"
	"strings"
)

// DestResource is a No-dal pool or network that a source identifier can bind to.
type DestResource struct {
	ID         string
	Name       string
	Kind       string
	BridgeName string
}

// AutoMap binds source storage and bridges onto destination resources.
// Existing mapping entries win. A single destination is unambiguous.
// Multiple destinations without a name or bridge match stay unmapped.
func AutoMap(sourceStorage, sourceNets []string, destPools, destNets []DestResource, existing Mapping) (Mapping, []Finding) {
	out := Mapping{
		Storage: copyMap(existing.Storage),
		Network: copyMap(existing.Network),
		VLAN:    copyMap(existing.VLAN),
	}
	var findings []Finding
	for _, src := range uniqueNonEmpty(sourceStorage) {
		if out.Storage[src] != "" {
			continue
		}
		if dest, ok := matchDest(src, destPools); ok {
			out.Storage[src] = dest
			continue
		}
		if len(destPools) == 1 {
			out.Storage[src] = destPools[0].ID
			continue
		}
		if len(destPools) == 0 {
			findings = append(findings, Finding{Level: CompatBlocked, Code: "storage", Message: "No destination storage pool exists. Create a pool before import."})
			continue
		}
		findings = append(findings, Finding{Level: CompatRequiresMapping, Code: "storage", Message: "Source storage " + src + " has no automatic destination. Choose a pool in Advanced."})
	}
	for _, src := range uniqueNonEmpty(sourceNets) {
		if mappedNetwork(out, src) {
			continue
		}
		if dest, ok := matchDest(src, destNets); ok {
			out.Network[src] = dest
			continue
		}
		if len(destNets) == 1 {
			out.Network[src] = destNets[0].ID
			continue
		}
		if len(destNets) == 0 {
			findings = append(findings, Finding{Level: CompatBlocked, Code: "network", Message: "No destination network exists. Create a network before import."})
			continue
		}
		findings = append(findings, Finding{Level: CompatRequiresMapping, Code: "network", Message: "Source network " + src + " has no automatic destination. Choose a network in Advanced."})
	}
	return out, findings
}

func mappedNetwork(m Mapping, src string) bool {
	if m.Network[src] != "" {
		return true
	}
	if i := strings.LastIndex(src, "/"); i > 0 {
		return m.Network[src[:i]] != ""
	}
	return false
}

func matchDest(src string, dests []DestResource) (string, bool) {
	want := normalizeIdent(src)
	if want == "" {
		return "", false
	}
	var hits []string
	for _, d := range dests {
		if normalizeIdent(d.ID) == want || normalizeIdent(d.Name) == want || normalizeIdent(d.BridgeName) == want {
			hits = append(hits, d.ID)
		}
	}
	if len(hits) == 1 {
		return hits[0], true
	}
	return "", false
}

func normalizeIdent(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}

func uniqueNonEmpty(in []string) []string {
	seen := map[string]struct{}{}
	var out []string
	for _, v := range in {
		v = strings.TrimSpace(v)
		if v == "" {
			continue
		}
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	return out
}

func copyMap(in map[string]string) map[string]string {
	out := map[string]string{}
	for k, v := range in {
		if strings.TrimSpace(k) != "" && strings.TrimSpace(v) != "" {
			out[k] = v
		}
	}
	return out
}

// SourceIdentifiers returns storage and network names from a manifest.
func SourceIdentifiers(m Manifest) (storage []string, nets []string) {
	if m.VM != nil {
		for _, d := range m.VM.Disks {
			if d.Storage != "" {
				storage = append(storage, d.Storage)
			}
		}
		for _, n := range m.VM.NICs {
			if n.Bridge == "" {
				continue
			}
			if n.VLAN > 0 {
				nets = append(nets, n.Bridge+"/"+strconv.Itoa(n.VLAN))
			}
			nets = append(nets, n.Bridge)
		}
	}
	if m.Container != nil {
		if m.Container.Rootfs != nil && m.Container.Rootfs.Path != "" {
			if st, _ := pveVolume(m.Container.Rootfs.Path); st != "" {
				storage = append(storage, st)
			}
		}
		for _, n := range m.Container.NICs {
			if n.Bridge != "" {
				nets = append(nets, n.Bridge)
			}
		}
	}
	return uniqueNonEmpty(storage), uniqueNonEmpty(nets)
}

// Strategies are operator intents. The engine maps each workload onto a method.
func Strategies() []StrategyInfo {
	return []StrategyInfo{
		{
			ID: StrategyConsistent, Label: "Consistent copy", Consistency: ConsistencySafe, SourceSafety: SourceProtected,
			Summary:     "Accept downtime. Prefer Local Host on this machine, then Offline when the guest can be copied stopped, otherwise an existing backup, then disk import.",
			Recommended: true, Available: true,
		},
		{
			ID: StrategyLeaveRunning, Label: "Leave sources running", Consistency: ConsistencyDepends, SourceSafety: SourceProtected,
			Summary:   "Prefer methods that do not require stopping the source. Uses existing backups or disk import. Local Host or Offline is used only when the guest is already stopped.",
			Available: true,
		},
		{
			ID: StrategyBackup, Label: "Existing backups", Consistency: ConsistencySafe, SourceSafety: SourceProtected,
			Summary:   "Import captured backups only. Running guests stay running. Workloads without a usable backup are blocked.",
			Available: true,
		},
		{
			ID: StrategyMinimalInterruption, Label: "Minimal interruption", Consistency: ConsistencyRisky, SourceSafety: SourceProtected,
			Summary:           "Would use snapshot-assisted or live transfer. Listed so the risk is visible.",
			Available:         false,
			UnavailableReason: "V1 does not perform live or snapshot-assisted transfer. Choose Consistent copy or Leave sources running.",
		},
	}
}

// NormalizeStrategy returns the default consistent strategy when empty.
func NormalizeStrategy(id string) (string, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return StrategyConsistent, nil
	}
	for _, s := range Strategies() {
		if s.ID != id {
			continue
		}
		if !s.Available {
			return "", fmt.Errorf("%s", firstNonEmpty(s.UnavailableReason, "migration strategy "+id+" is unavailable"))
		}
		return id, nil
	}
	return "", fmt.Errorf("unknown migration strategy")
}

// SuggestMode picks the safest available V1 mode for the default strategy.
func SuggestMode(w DiscoveredWorkload) (string, *Finding) {
	return SuggestModeForStrategy(w, StrategyConsistent)
}

// SuggestModeForStrategy picks a transfer method that fits the operator intent.
// It never selects live or snapshot-assisted.
func SuggestModeForStrategy(w DiscoveredWorkload, strategy string) (string, *Finding) {
	if strings.TrimSpace(w.BlockReason) != "" {
		f := Finding{Level: CompatBlocked, Code: "ct-rootfs", Message: w.BlockReason}
		return "", &f
	}
	has := func(id string) bool {
		for _, c := range w.Caps {
			if c == id {
				return true
			}
		}
		return false
	}
	switch strategy {
	case StrategyLeaveRunning:
		if has(ModeLocal) && !w.Running {
			return ModeLocal, nil
		}
		if has(ModeBackup) {
			return ModeBackup, nil
		}
		if has(ModeDisk) {
			f := Finding{Level: CompatWarning, Code: "disk-import", Message: "Leave sources running uses Disk / Archive for " + w.Name + " because no backup is available."}
			return ModeDisk, &f
		}
		if has(ModeOffline) && !w.Running {
			return ModeOffline, nil
		}
		if has(ModeOffline) && w.Running {
			f := Finding{Level: CompatWarning, Code: "source-must-stop", Message: "Leave sources running cannot keep " + w.Name + " online. Offline is the compatible method. Stop it on the source. No-dal will not stop it."}
			return ModeOffline, &f
		}
	case StrategyBackup:
		if has(ModeBackup) {
			return ModeBackup, nil
		}
		f := Finding{Level: CompatBlocked, Code: "mode", Message: "Existing backups strategy requires a usable backup for " + w.Name + "."}
		return "", &f
	default:
		if has(ModeLocal) && !w.Running {
			return ModeLocal, nil
		}
		if has(ModeOffline) && !w.Running {
			return ModeOffline, nil
		}
		if has(ModeBackup) {
			return ModeBackup, nil
		}
		if has(ModeOffline) && w.Running {
			f := Finding{Level: CompatWarning, Code: "source-must-stop", Message: "Offline is the compatible method. Stop " + w.Name + " on the source before import. No-dal will not stop it."}
			return ModeOffline, &f
		}
		if has(ModeDisk) {
			f := Finding{Level: CompatWarning, Code: "disk-import", Message: "Source volumes are not HTTP-downloadable. Copy a disk or archive and use Disk / Archive import, or map a downloadable volume."}
			return ModeDisk, &f
		}
	}
	f := Finding{Level: CompatBlocked, Code: "mode", Message: "No compatible migration method is available for " + w.Name + "."}
	return "", &f
}
