package ndnet

import (
	"fmt"
	"strings"
)

// Classify decides whether applying spec would touch the management path.
func Classify(spec Spec, host HostView) Classification {
	out := Classification{
		Danger:           DangerSafe,
		ManagementIfName: host.ManagementIfName,
		ManagementIndex:  host.ManagementIfIndex,
		SingleNIC:        physicalCount(host) <= 1,
	}
	if host.ManagementIfName == "" && host.DefaultRouteIf != "" {
		out.ManagementIfName = host.DefaultRouteIf
	}
	if Isolated(spec.Kind) {
		out.Reason = "isolated networks do not enslave a host NIC"
		return out
	}
	if spec.Kind != KindLANBridge {
		out.Danger = DangerDangerous
		out.Reason = "unknown network kind"
		out.RequiresConfirm = true
		return out
	}
	uplink := strings.TrimSpace(spec.UplinkIfName)
	out.TypedIfName = uplink
	if uplink == "" {
		out.Danger = DangerDangerous
		out.Reason = "LAN-bridge requires an uplink interface name"
		out.RequiresConfirm = true
		return out
	}
	if !ValidIfName(uplink) {
		out.Danger = DangerDangerous
		out.Reason = "uplink interface name is not valid"
		out.RequiresConfirm = true
		return out
	}
	if isLoopback(uplink) {
		out.Danger = DangerDangerous
		out.Reason = "loopback cannot be enslaved"
		out.RequiresConfirm = true
		return out
	}
	mgmt := managementName(host)
	if sameIface(uplink, mgmt) || sameIface(uplink, host.DefaultRouteIf) || matchesManagementIndex(host, uplink) {
		out.Danger = DangerDangerous
		out.RequiresConfirm = true
		out.Reason = fmt.Sprintf("enslaving %s would move the management NIC onto a bridge", uplink)
		return out
	}
	if out.SingleNIC {
		out.Danger = DangerDangerous
		out.RequiresConfirm = true
		out.Reason = "single-NIC host: LAN-bridge enslaves the only physical interface"
		return out
	}
	if iface, ok := lookup(host, uplink); ok && containsAny(iface.Addresses, host.ManagementAddresses) {
		out.Danger = DangerDangerous
		out.RequiresConfirm = true
		out.Reason = fmt.Sprintf("enslaving %s would move a management address", uplink)
		return out
	}
	out.Danger = DangerWarning
	out.Reason = fmt.Sprintf("LAN-bridge enslaves %s; guests share that L2 domain and No-dal will not run DHCP there", uplink)
	return out
}

func managementName(host HostView) string {
	if host.ManagementIfName != "" {
		return host.ManagementIfName
	}
	return host.DefaultRouteIf
}

func physicalCount(host HostView) int {
	n := 0
	for _, iface := range host.Ifaces {
		if isPhysical(iface) {
			n++
		}
	}
	return n
}

func isPhysical(iface Iface) bool {
	name := strings.ToLower(iface.Name)
	if isLoopback(name) {
		return false
	}
	switch strings.ToLower(iface.Kind) {
	case "bridge", "veth", "tun", "tap", "bond", "vlan", "dummy":
		return false
	}
	if strings.HasPrefix(name, "ndl") || strings.HasPrefix(name, "veth") || strings.HasPrefix(name, "tap") || strings.HasPrefix(name, "br-") {
		return false
	}
	return true
}

func isLoopback(name string) bool {
	return name == "lo" || strings.HasPrefix(name, "lo:")
}

func lookup(host HostView, name string) (Iface, bool) {
	for _, iface := range host.Ifaces {
		if sameIface(iface.Name, name) {
			return iface, true
		}
	}
	return Iface{}, false
}

func matchesManagementIndex(host HostView, name string) bool {
	if host.ManagementIfIndex <= 0 {
		return false
	}
	iface, ok := lookup(host, name)
	return ok && iface.IfIndex == host.ManagementIfIndex
}

func sameIface(a, b string) bool {
	return a != "" && strings.EqualFold(a, b)
}

func containsAny(have, want []string) bool {
	set := map[string]struct{}{}
	for _, addr := range have {
		set[stripCIDR(addr)] = struct{}{}
	}
	for _, addr := range want {
		if _, ok := set[stripCIDR(addr)]; ok {
			return true
		}
	}
	return false
}

func stripCIDR(addr string) string {
	if i := strings.IndexByte(addr, '/'); i > 0 {
		return addr[:i]
	}
	return addr
}
