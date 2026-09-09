package ndnet

import (
	"fmt"
	"net"
	"strings"
)

// ValidateSpecLocators refuses request locators BuildPlan would reject
// without host routing or inventory. fakeNet DryRun does not run BuildPlan.
func ValidateSpecLocators(spec Spec) error {
	if strings.TrimSpace(spec.Kind) != "" && !ValidKind(spec.Kind) {
		return fmt.Errorf("unsupported network kind")
	}
	if Isolated(spec.Kind) {
		cidr := strings.TrimSpace(spec.IPv4CIDR)
		if cidr == "" {
			cidr = DefaultIPv4
		}
		if _, _, err := parseIPv4Net(cidr); err != nil {
			return err
		}
	}
	if spec.Kind == KindLANBridge {
		uplink := strings.TrimSpace(spec.UplinkIfName)
		if !ValidIfName(uplink) {
			return fmt.Errorf("LAN-bridge requires a valid uplink_ifname")
		}
		if id := strings.TrimSpace(spec.NetworkID); id != "" {
			bridge, err := BridgeName(id)
			if err != nil {
				return err
			}
			if sameIface(uplink, bridge) {
				return fmt.Errorf("uplink cannot be the No-dal bridge")
			}
		}
	}
	return nil
}

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
		if err := ValidateReservations(spec.Reservations, spec.IPv4CIDR); err != nil {
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
	if gw := strings.TrimSpace(host.DefaultGateway); gw != "" && lanBridgeCarriesDefaultRoute(host, bridge, plan.UplinkIfName) {
		plan.Gateway = gw
	}
	plan.Files = lanBridgeFiles(id, bridge, plan.UplinkIfName, host)
	plan.NFT = renderNFT(plan)
	plan.Warnings = append(plan.Warnings, class.Reason)
	if class.Danger == DangerDangerous {
		plan.Warnings = append(plan.Warnings, "typed interface confirmation and the rollback watchdog are required")
	}
	return plan, nil
}

func ValidateReservations(items []Reservation, cidr string) error {
	cidr = strings.TrimSpace(cidr)
	if cidr == "" {
		cidr = DefaultIPv4
	}
	_, n, err := parseIPv4Net(cidr)
	if err != nil {
		return err
	}
	return validateReservations(items, n, gatewayOf(n))
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

func lanBridgeFiles(id, bridge, uplink string, host HostView) []File {
	netdev := "[NetDev]\nName=" + bridge + "\nKind=bridge\n"
	if mac := lanBridgeMAC(host, uplink); mac != "" {
		netdev += "MACAddress=" + mac + "\n"
	}
	addrs, gw, dns := lanBridgeHostIP(host, bridge, uplink)
	var b strings.Builder
	b.WriteString("[Match]\nName=" + bridge + "\n\n[Network]\n")
	if len(addrs) > 0 {
		// Keep the observed host address. DHCP=yes on a new bridge MAC/DUID
		// obtains a different lease than the physical uplink reservation.
		b.WriteString("DHCP=no\nKeepConfiguration=static\n")
		for _, addr := range addrs {
			b.WriteString("Address=" + addr + "\n")
		}
		if gw != "" {
			b.WriteString("Gateway=" + gw + "\n")
		}
		for _, ns := range dns {
			b.WriteString("DNS=" + ns + "\n")
		}
	} else {
		b.WriteString("DHCP=yes\nKeepConfiguration=yes\n\n[DHCP]\nClientIdentifier=mac\n")
	}
	up := "[Match]\nName=" + uplink + "\n\n[Network]\nBridge=" + bridge + "\n"
	return []File{
		{RelPath: persistName(id, ".netdev"), Body: netdev},
		{RelPath: persistName(id, ".network"), Body: b.String()},
		{RelPath: persistName(id, "-uplink.network"), Body: up},
	}
}

func lanBridgeMAC(host HostView, uplink string) string {
	iface, ok := lookup(host, uplink)
	if !ok {
		return ""
	}
	return normalizeMAC(iface.HardwareAddr)
}

func lanBridgeCarriesDefaultRoute(host HostView, bridge, uplink string) bool {
	return sameIface(host.DefaultRouteIf, uplink) || sameIface(host.DefaultRouteIf, bridge)
}

func lanBridgeHostIP(host HostView, bridge, uplink string) (addrs []string, gw string, dns []string) {
	if up, ok := lookup(host, uplink); ok {
		addrs = ipv4CIDRs(up.Addresses)
	}
	if len(addrs) == 0 {
		if br, ok := lookup(host, bridge); ok {
			addrs = ipv4CIDRs(br.Addresses)
		}
	}
	if lanBridgeCarriesDefaultRoute(host, bridge, uplink) {
		if mgmt := ipv4CIDRs(host.ManagementAddresses); len(mgmt) > 0 {
			addrs = mgmt
		}
		gw = strings.TrimSpace(host.DefaultGateway)
		dns = append([]string{}, host.Nameservers...)
	}
	return addrs, gw, dns
}

func ipv4CIDRs(addrs []string) []string {
	var out []string
	seen := map[string]struct{}{}
	for _, raw := range addrs {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		ip, n, err := net.ParseCIDR(raw)
		if err != nil {
			ip = net.ParseIP(raw)
			if ip == nil || ip.To4() == nil || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsUnspecified() {
				continue
			}
			raw = ip.To4().String() + "/32"
			ip, n, err = net.ParseCIDR(raw)
			if err != nil {
				continue
			}
		}
		ip4 := ip.To4()
		if ip4 == nil || ip4.IsLoopback() || ip4.IsLinkLocalUnicast() || ip4.IsUnspecified() {
			continue
		}
		ones, bits := n.Mask.Size()
		if bits != 32 || ones <= 0 || ones > 32 {
			continue
		}
		s := fmt.Sprintf("%s/%d", ip4.String(), ones)
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	return out
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
