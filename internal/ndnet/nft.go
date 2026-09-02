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
	b.WriteString("  chain forward {\n")
	b.WriteString("    type filter hook forward priority -10; policy accept;\n")
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

func renderNFTDestroy(plan Plan) string {
	if plan.BridgeName == "" {
		return ""
	}
	return "destroy table inet " + NATTableName(plan.BridgeName) + "\n"
}

func quoteNFT(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, "") + `"`
}
