package ndnet

import (
	"strings"
)

// NATTableName is the per-network inet table. It is No-dal-owned.
func NATTableName(bridge string) string {
	hex := strings.TrimPrefix(bridge, "ndl")
	if hex == "" || hex == bridge {
		hex = "net"
	}
	return "ndl_nat_" + hex
}

func renderNFT(plan Plan) string {
	if !plan.NAT || plan.IPv4CIDR == "" || plan.BridgeName == "" || plan.EgressIfName == "" {
		return ""
	}
	table := NATTableName(plan.BridgeName)
	br := quoteNFT(plan.BridgeName)
	oif := quoteNFT(plan.EgressIfName)
	cidr := plan.IPv4CIDR
	var b strings.Builder
	b.WriteString("destroy table inet " + table + "\n")
	b.WriteString("table inet " + table + " {\n")
	// An isolated-nat network reaches the internet only. New connections
	// from it to the host's own services or to private ranges (the LAN,
	// other No-dal networks, edge tunnels) are dropped, so a compromised
	// container cannot reach the rest of the home network. DHCP and DNS
	// from the gateway still work, and so do replies to connections that
	// came in (port forwards, edge forwards).
	b.WriteString("  chain input {\n")
	b.WriteString("    type filter hook input priority -10; policy accept;\n")
	b.WriteString("    iifname " + br + " ct state established,related accept\n")
	b.WriteString("    iifname " + br + " udp dport { 53, 67 } accept\n")
	b.WriteString("    iifname " + br + " tcp dport 53 accept\n")
	b.WriteString("    iifname " + br + " icmp type echo-request accept\n")
	b.WriteString("    iifname " + br + " counter drop\n")
	b.WriteString("  }\n")
	b.WriteString("  chain forward {\n")
	b.WriteString("    type filter hook forward priority -10; policy accept;\n")
	b.WriteString("    iifname " + br + " ip daddr != " + cidr + " ip daddr { " + strings.Join(natBlocked, ", ") + " } ct state new counter drop\n")
	b.WriteString("    iifname " + br + " oifname " + oif + " ip saddr " + cidr + " accept\n")
	b.WriteString("    iifname " + oif + " oifname " + br + " ip daddr " + cidr + " ct state established,related accept\n")
	b.WriteString("  }\n")
	b.WriteString("  chain postrouting {\n")
	b.WriteString("    type nat hook postrouting priority srcnat; policy accept;\n")
	b.WriteString("    ip saddr " + cidr + " oifname " + oif + " masquerade\n")
	b.WriteString("  }\n")
	b.WriteString("}\n")
	return b.String()
}

// natBlocked are the destinations an isolated-nat network may not open
// connections to: private, CGNAT and link-local ranges.
var natBlocked = []string{"10.0.0.0/8", "100.64.0.0/10", "169.254.0.0/16", "172.16.0.0/12", "192.168.0.0/16"}

func renderNFTDestroy(plan Plan) string {
	if plan.BridgeName == "" {
		return ""
	}
	return "destroy table inet " + NATTableName(plan.BridgeName) + "\n"
}

func quoteNFT(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, "") + `"`
}
