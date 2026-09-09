package ndnet

import (
	"os"
	"path/filepath"
	"strings"
)

func persistIDFromName(name string) (id, kind string) {
	base := strings.ToLower(filepath.Base(name))
	if !strings.HasPrefix(base, "50-ndl-") {
		return "", ""
	}
	rest := strings.TrimPrefix(base, "50-ndl-")
	switch {
	case strings.HasSuffix(rest, "-uplink.network"):
		return strings.TrimSuffix(rest, "-uplink.network"), "uplink"
	case strings.HasSuffix(rest, ".netdev"):
		return strings.TrimSuffix(rest, ".netdev"), "netdev"
	case strings.HasSuffix(rest, ".network"):
		return strings.TrimSuffix(rest, ".network"), "network"
	default:
		return "", ""
	}
}

func networkdMatchName(body string) string {
	inMatch := false
	for _, line := range strings.Split(body, "\n") {
		trim := strings.TrimSpace(line)
		if trim == "" || strings.HasPrefix(trim, "#") {
			continue
		}
		if strings.HasPrefix(trim, "[") {
			inMatch = strings.EqualFold(trim, "[Match]")
			continue
		}
		if !inMatch {
			continue
		}
		key, val, ok := strings.Cut(trim, "=")
		if ok && strings.EqualFold(strings.TrimSpace(key), "Name") {
			return strings.TrimSpace(val)
		}
	}
	return ""
}

func networkdBridgeName(body string) string {
	inNet := false
	for _, line := range strings.Split(body, "\n") {
		trim := strings.TrimSpace(line)
		if trim == "" || strings.HasPrefix(trim, "#") {
			continue
		}
		if strings.HasPrefix(trim, "[") {
			inNet = strings.EqualFold(trim, "[Network]")
			continue
		}
		if !inNet {
			continue
		}
		key, val, ok := strings.Cut(trim, "=")
		if ok && strings.EqualFold(strings.TrimSpace(key), "Bridge") {
			return strings.TrimSpace(val)
		}
	}
	return ""
}

func (e *Engine) listStaleUplinkClaims(uplink, keepID string) []StaleUplinkClaim {
	uplink = strings.TrimSpace(uplink)
	keepID = strings.ToLower(strings.TrimSpace(keepID))
	entries, err := os.ReadDir(e.networkDir())
	if err != nil {
		return nil
	}
	var out []StaleUplinkClaim
	for _, ent := range entries {
		if ent.IsDir() || !ownedPersistName(ent.Name()) {
			continue
		}
		id, kind := persistIDFromName(ent.Name())
		if kind != "uplink" || id == "" || id == keepID {
			continue
		}
		b, err := os.ReadFile(filepath.Join(e.networkDir(), ent.Name()))
		if err != nil {
			continue
		}
		if !sameIface(networkdMatchName(string(b)), uplink) {
			continue
		}
		out = append(out, StaleUplinkClaim{
			NetworkID: id,
			Path:      ent.Name(),
			Bridge:    networkdBridgeName(string(b)),
		})
	}
	return out
}

func (e *Engine) removeOwnedNetworkSet(id string) {
	id = strings.ToLower(strings.TrimSpace(id))
	if id == "" {
		return
	}
	for _, suffix := range []string{".netdev", ".network", "-uplink.network"} {
		name := persistName(id, suffix)
		if !ownedPersistName(name) {
			continue
		}
		_ = os.Remove(filepath.Join(e.networkDir(), name))
	}
}

func (e *Engine) releaseStaleUplinkClaims(uplink, keepID string) []StaleUplinkClaim {
	stale := e.listStaleUplinkClaims(uplink, keepID)
	for _, claim := range stale {
		e.removeOwnedNetworkSet(claim.NetworkID)
	}
	return stale
}

func (e *Engine) sweepOrphanUplinkFiles(keep map[string]bool) []StaleUplinkClaim {
	entries, err := os.ReadDir(e.networkDir())
	if err != nil {
		return nil
	}
	var out []StaleUplinkClaim
	seen := map[string]bool{}
	for _, ent := range entries {
		if ent.IsDir() || !ownedPersistName(ent.Name()) {
			continue
		}
		id, kind := persistIDFromName(ent.Name())
		if kind != "uplink" || id == "" || keep[id] || seen[id] {
			continue
		}
		b, err := os.ReadFile(filepath.Join(e.networkDir(), ent.Name()))
		if err != nil {
			continue
		}
		seen[id] = true
		out = append(out, StaleUplinkClaim{
			NetworkID: id,
			Path:      ent.Name(),
			Bridge:    networkdBridgeName(string(b)),
		})
		e.removeOwnedNetworkSet(id)
	}
	return out
}

func (e *Engine) adminNetworkdConflicts(uplink string) []HostManagerConflict {
	uplink = strings.TrimSpace(uplink)
	entries, err := os.ReadDir(e.networkDir())
	if err != nil {
		return nil
	}
	var out []HostManagerConflict
	for _, ent := range entries {
		if ent.IsDir() || ownedPersistName(ent.Name()) {
			continue
		}
		if !strings.HasSuffix(strings.ToLower(ent.Name()), ".network") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(e.networkDir(), ent.Name()))
		if err != nil {
			continue
		}
		if !sameIface(networkdMatchName(string(b)), uplink) {
			continue
		}
		out = append(out, HostManagerConflict{
			Manager: "systemd-networkd",
			Path:    ent.Name(),
			Detail:  "administrator networkd unit already matches this uplink",
			Action:  "conflict",
		})
	}
	return out
}
