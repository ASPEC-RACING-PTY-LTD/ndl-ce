package ndnet

import "strings"

func renderNFT(plan Plan) string {
	if !plan.NAT || plan.IPv4CIDR == "" {
		return ""
	}
	var b strings.Builder
	b.WriteString("table inet ndl {\n")
	b.WriteString("  chain forward {\n")
	b.WriteString("    type filter hook forward priority 0; policy accept;\n")
	b.WriteString("    iifname " + quoteNFT(plan.BridgeName) + " accept\n")
	b.WriteString("    oifname " + quoteNFT(plan.BridgeName) + " ct state established,related accept\n")
	b.WriteString("  }\n")
	b.WriteString("  chain postrouting {\n")
	b.WriteString("    type nat hook postrouting priority 100; policy accept;\n")
	b.WriteString("    ip saddr " + plan.IPv4CIDR + " masquerade\n")
	b.WriteString("  }\n")
	b.WriteString("}\n")
	return b.String()
}

func quoteNFT(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, "") + `"`
}
