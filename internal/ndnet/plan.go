package ndnet

import (
	"fmt"
	"net"
	"strings"
)

// BuildPlan validates spec against host and generates persistence artifacts.
func BuildPlan(spec Spec, host HostView) (Plan, error) {
	if !ValidKind(spec.Kind) {
		return Plan{}, fmt.Errorf("unsupported network kind")
	}
	id := strings.TrimSpace(spec.NetworkID)
	bridge, err := BridgeName(id)
	if err != nil {
		return Plan{}, err
	}
	class := Classify(spec, host)
	plan := Plan{
		NetworkID:         id,
		Name:              strings.TrimSpace(spec.Name),
		Kind:              spec.Kind,
		BridgeName:        bridge,
		UplinkIfName:      strings.TrimSpace(spec.UplinkIfName),
		Class:             class,
		ManagementIfIndex: host.ManagementIfIndex,
		ManagementIfName:  managementName(host),
	}
	if Isolated(spec.Kind) {
		cidr := strings.TrimSpace(spec.IPv4CIDR)
		if cidr == "" {
			cidr = DefaultIPv4
		}
		_, n, err := parseIPv4Net(cidr)
		if err != nil {
			return Plan{}, err
		}
		if overlaps(n, host.ManagementAddresses) {
			return Plan{}, fmt.Errorf("isolated subnet overlaps a management address")
		}
		gw := gatewayOf(n)
		start, end, err := dhcpRange(n)
		if err != nil {
			return Plan{}, err
		}
		plan.IPv4CIDR = n.String()
		plan.Gateway = gw.String()
		plan.DHCPStart = start.String()
		plan.DHCPEnd = end.String()
		plan.DHCP = spec.DHCP || spec.Kind != KindLANBridge
		plan.DNS = spec.DNS || spec.Kind != KindLANBridge
		if spec.Kind == KindIsolated && !spec.DHCP && spec.IPv4CIDR != "" && !spec.DNS {
			// Explicit create still enables isolated DHCP/DNS by default.
			plan.DHCP = true
			plan.DNS = true
		}
		plan.DHCP = true
		plan.DNS = true
		plan.NAT = spec.Kind == KindIsolatedNAT
		if plan.NAT {
			egress := strings.TrimSpace(host.DefaultRouteIf)
			if !ValidIfName(egress) {
				return Plan{}, fmt.Errorf("isolated-nat requires a default IPv4 route to determine egress")
			}
			if sameIface(egress, bridge) {
				return Plan{}, fmt.Errorf("isolated-nat egress cannot be the isolated bridge")
			}
			if isLoopback(egress) {
				return Plan{}, fmt.Errorf("isolated-nat egress cannot be loopback")
			}
			plan.EgressIfName = egress
		}
		if err := validateReservations(spec.Reservations, n, gw); err != nil {
			return Plan{}, err
		}
		plan.Files = isolatedFiles(id, bridge, gw, n, plan.NAT)
		plan.Dnsmasq = renderDnsmasq(bridge, gw, start, end, spec.Reservations)
		plan.NFT = renderNFT(plan)
		return plan, nil
	}

	// LAN-bridge: no DHCP, no NAT, no second server on the uplink LAN.
	if !ValidIfName(plan.UplinkIfName) {
		return Plan{}, fmt.Errorf("LAN-bridge requires a valid uplink_ifname")
	}
	if plan.UplinkIfName == plan.BridgeName {
		return Plan{}, fmt.Errorf("uplink cannot be the No-dal bridge")
	}
	plan.DHCP = false
	plan.DNS = false
	plan.NAT = false
	plan.Files = lanBridgeFiles(id, bridge, plan.UplinkIfName)
	plan.NFT = renderNFT(plan)
	plan.Warnings = append(plan.Warnings, class.Reason)
	if class.Danger == DangerDangerous {
		plan.Warnings = append(plan.Warnings, "typed interface confirmation and the rollback watchdog are required")
	}
	return plan, nil
}

func validateReservations(items []Reservation, n *net.IPNet, gw net.IP) error {
	seenMAC := map[string]struct{}{}
	seenIP := map[string]struct{}{}
	for _, item := range items {
		mac, err := net.ParseMAC(strings.TrimSpace(item.MAC))
		if err != nil {
			return fmt.Errorf("reservation mac is invalid")
		}
		ip := net.ParseIP(strings.TrimSpace(item.IPv4))
		if ip == nil || ip.To4() == nil {
			return fmt.Errorf("reservation ipv4 is invalid")
		}
		ip = ip.To4()
		if !n.Contains(ip) {
			return fmt.Errorf("reservation ipv4 is outside the isolated subnet")
		}
		if ip.Equal(gw) || ip.Equal(n.IP) {
			return fmt.Errorf("reservation ipv4 is reserved")
		}
		macKey := strings.ToLower(mac.String())
		ipKey := ip.String()
		if _, ok := seenMAC[macKey]; ok {
			return fmt.Errorf("duplicate reservation mac")
		}
		if _, ok := seenIP[ipKey]; ok {
			return fmt.Errorf("duplicate reservation ipv4")
		}
		seenMAC[macKey] = struct{}{}
		seenIP[ipKey] = struct{}{}
	}
	return nil
}

func isolatedFiles(id, bridge string, gw net.IP, n *net.IPNet, nat bool) []File {
	ones, _ := n.Mask.Size()
	netdev := "[NetDev]\nName=" + bridge + "\nKind=bridge\n"
	network := "[Match]\nName=" + bridge + "\n\n[Link]\nRequiredForOnline=no\nActivationPolicy=always-up\n\n[Network]\nAddress=" + fmt.Sprintf("%s/%d", gw.String(), ones) + "\nDHCP=no\nLinkLocalAddressing=no\nConfigureWithoutCarrier=yes\nIgnoreCarrierLoss=yes\n"
	if nat {
		network += "IPForward=yes\nIPv4Forwarding=yes\n"
	}
	return []File{
		{RelPath: persistName(id, ".netdev"), Body: netdev},
		{RelPath: persistName(id, ".network"), Body: network},
	}
}

func lanBridgeFiles(id, bridge, uplink string) []File {
	netdev := "[NetDev]\nName=" + bridge + "\nKind=bridge\n"
	br := "[Match]\nName=" + bridge + "\n\n[Network]\nDHCP=yes\nKeepConfiguration=yes\n"
	up := "[Match]\nName=" + uplink + "\n\n[Network]\nBridge=" + bridge + "\n"
	return []File{
		{RelPath: persistName(id, ".netdev"), Body: netdev},
		{RelPath: persistName(id, ".network"), Body: br},
		{RelPath: persistName(id, "-uplink.network"), Body: up},
	}
}

func persistName(id, suffix string) string {
	return "50-ndl-" + strings.ToLower(id) + suffix
}

// PreviewOf converts a plan to the dry-run result.
func PreviewOf(plan Plan) Preview {
	return Preview{
		NetworkID:         plan.NetworkID,
		Name:              plan.Name,
		Kind:              plan.Kind,
		BridgeName:        plan.BridgeName,
		UplinkIfName:      plan.UplinkIfName,
		EgressIfName:      plan.EgressIfName,
		IPv4CIDR:          plan.IPv4CIDR,
		Gateway:           plan.Gateway,
		Danger:            plan.Class.Danger,
		DangerReason:      plan.Class.Reason,
		RequiresConfirm:   plan.Class.RequiresConfirm,
		TypedIfName:       plan.Class.TypedIfName,
		DHCP:              plan.DHCP,
		DNS:               plan.DNS,
		NAT:               plan.NAT,
		Files:             plan.Files,
		ManagementIfIndex: plan.ManagementIfIndex,
		ManagementIfName:  plan.ManagementIfName,
		Warnings:          plan.Warnings,
		DryRun:            true,
	}
}
