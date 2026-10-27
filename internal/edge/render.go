package edge

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/netip"
	"sort"
	"strings"
)

// TableName is the nftables table (family inet) No-dal owns for edge
// forwarding. Nothing else is touched.
const TableName = "ndl_edge"

// Private are destinations an origin never sends through the edge: its
// own networks, Docker, CGNAT, link-local, loopback and multicast.
var Private = []string{
	"0.0.0.0/8", "10.0.0.0/8", "100.64.0.0/10", "127.0.0.0/8",
	"169.254.0.0/16", "172.16.0.0/12", "192.168.0.0/16", "224.0.0.0/3",
}

// Env is what the node found when rendering.
type Env struct {
	// Egress maps a tunnel ID to the IPv4 addresses its Egress entries
	// resolved to (container names are looked up in Docker).
	Egress map[string][]string
}

// CounterName is the named counter of a forward.
func CounterName(tunnelID, forwardID string) string {
	sum := sha256.Sum256([]byte(tunnelID + "\x00" + forwardID))
	return "f_" + hex.EncodeToString(sum[:])[:12]
}

// Render returns the nft script that replaces the table. An empty spec
// renders only the delete.
func Render(spec Spec, env Env) string {
	var b strings.Builder
	b.WriteString("table inet " + TableName + "\ndelete table inet " + TableName + "\n")
	if len(spec.Tunnels) == 0 {
		return b.String()
	}
	b.WriteString("table inet " + TableName + " {\n")
	b.WriteString("\tset private4 {\n\t\ttype ipv4_addr\n\t\tflags interval\n\t\telements = { " + strings.Join(Private, ", ") + " }\n\t}\n")
	for _, t := range spec.Tunnels {
		for _, f := range t.Forwards {
			fmt.Fprintf(&b, "\tcounter %s { }\n", CounterName(t.ID, f.ID))
		}
	}

	var preNAT, postNAT, mark, raw, out, mss []string
	for _, t := range spec.Tunnels {
		ifn := quote(IfName(t.ID))
		self := addrOf(t.Address)
		m := fmt.Sprintf("0x%08x", Mark(t.Index))
		mss = append(mss,
			fmt.Sprintf("oifname %s tcp flags syn / syn,rst tcp option maxseg size set rt mtu", ifn),
			fmt.Sprintf("iifname %s tcp flags syn / syn,rst tcp option maxseg size set rt mtu", ifn))
		switch t.Role {
		case RoleEdge:
			for _, f := range t.Forwards {
				preNAT = append(preNAT, fmt.Sprintf("iifname != %s fib daddr type local %s dport %s counter name %s dnat ip to %s",
					ifn, f.Protocol, portSpan(f.Port, f.last()), quote(CounterName(t.ID, f.ID)), edgeTo(t.Peer, f)))
			}
			// The origin's own outbound traffic leaves with the edge's address.
			postNAT = append(postNAT, fmt.Sprintf("iifname %s ip saddr %s oifname != %s masquerade", ifn, t.Peer, ifn))
		case RoleOrigin:
			var udpPorts []string
			for _, f := range t.Forwards {
				span := portSpan(f.ToPort, f.ToPort+f.last()-f.Port)
				if f.Target == "" {
					preNAT = append(preNAT, fmt.Sprintf("iifname %s ip daddr %s %s dport %s counter name %s",
						ifn, self, f.Protocol, span, quote(CounterName(t.ID, f.ID))))
					if f.Protocol == "udp" {
						udpPorts = append(udpPorts, span)
					}
					continue
				}
				preNAT = append(preNAT, fmt.Sprintf("iifname %s ip daddr %s %s dport %s counter name %s dnat ip to %s",
					ifn, self, f.Protocol, span, quote(CounterName(t.ID, f.ID)), f.Target))
				if f.Masquerade {
					postNAT = append(postNAT, fmt.Sprintf("iifname %s ip daddr %s %s dport %s masquerade", ifn, f.Target, f.Protocol, span))
				}
			}
			// Anything leaving through the tunnel carries the tunnel address,
			// so the edge can match it to the connection it forwarded.
			postNAT = append(postNAT, fmt.Sprintf("oifname %s ip saddr != %s masquerade", ifn, self))
			// Connections that came in through the tunnel go back through it.
			mark = append(mark, fmt.Sprintf("iifname %s ct state new ct mark set %s", ifn, m))
			if eg := env.Egress[t.ID]; len(eg) > 0 {
				mark = append(mark, fmt.Sprintf("iifname != %s ip saddr { %s } ip daddr != @private4 ct state new ct mark set %s",
					ifn, strings.Join(eg, ", "), m))
			}
			mark = append(mark, fmt.Sprintf("iifname != %s ct mark %s meta mark set %s", ifn, m, m))
			out = append(out, fmt.Sprintf("ct mark %s meta mark set %s", m, m))
			// A UDP server on the host network answers from an unconnected
			// socket: the kernel picks the uplink's address, and a new NAT
			// entry for it would clash with the one the request made. So
			// these replies skip conntrack and get the tunnel address
			// directly, which is what the edge's DNAT entry expects.
			if len(udpPorts) > 0 {
				ports := strings.Join(udpPorts, ", ")
				raw = append(raw, fmt.Sprintf("ip daddr != @private4 udp sport { %s } notrack", ports))
				out = append(out, fmt.Sprintf("ip daddr != @private4 udp sport { %s } ip saddr set %s meta mark set %s", ports, self, m))
			}
		}
	}
	chain(&b, "prerouting_nat", "type nat hook prerouting priority dstnat - 5; policy accept;", preNAT)
	chain(&b, "postrouting_nat", "type nat hook postrouting priority srcnat - 5; policy accept;", postNAT)
	chain(&b, "prerouting_mark", "type filter hook prerouting priority mangle; policy accept;", mark)
	chain(&b, "output_raw", "type filter hook output priority raw; policy accept;", raw)
	chain(&b, "output_mark", "type route hook output priority mangle; policy accept;", out)
	chain(&b, "forward_mss", "type filter hook forward priority mangle; policy accept;", mss)
	b.WriteString("}\n")
	return b.String()
}

func chain(b *strings.Builder, name, hook string, rules []string) {
	if len(rules) == 0 {
		return
	}
	b.WriteString("\tchain " + name + " {\n\t\t" + hook + "\n")
	for _, r := range rules {
		b.WriteString("\t\t" + r + "\n")
	}
	b.WriteString("\t}\n")
}

// edgeTo is the DNAT destination. A range keeps each port's offset only
// when it maps to the same ports, so ranges always keep their ports.
func edgeTo(peer string, f Forward) string {
	if f.ToPort == f.Port {
		return peer
	}
	return fmt.Sprintf("%s:%d", peer, f.ToPort)
}

func portSpan(lo, hi int) string {
	if hi > lo {
		return fmt.Sprintf("%d-%d", lo, hi)
	}
	return fmt.Sprint(lo)
}

func addrOf(cidr string) string {
	p, err := netip.ParsePrefix(cidr)
	if err != nil {
		return cidr
	}
	return p.Addr().String()
}

func quote(s string) string { return `"` + s + `"` }

// sortedUnique returns the IPv4 addresses in in, sorted, without repeats.
func sortedUnique(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		a, err := netip.ParseAddr(strings.TrimSpace(s))
		if err != nil || !a.Is4() || seen[a.String()] {
			continue
		}
		seen[a.String()] = true
		out = append(out, a.String())
	}
	sort.Strings(out)
	return out
}
