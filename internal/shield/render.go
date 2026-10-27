package shield

import (
	"fmt"
	"net/netip"
	"sort"
	"strconv"
	"strings"
)

// Env is what the node knows that the control plane does not.
type Env struct {
	// Devices are the interfaces the ingress chain attaches to.
	Devices []string
	// Host are this node's own addresses.
	Host []netip.Addr
	// Local are the networks this node is directly on.
	Local []netip.Prefix
	// Now is Unix seconds, for the time left on manual bans.
	Now int64
	// Carry are automatic bans to keep across a re-apply, by rule ID.
	Carry map[string][]Ban
}

const (
	v6Mask      = "ffff:ffff:ffff:ffff::"
	meterSize   = 65536
	meterTTL    = "30s"
	banSetSize  = 65536
	ingressPrio = -500
	// conntrackPrio runs after conntrack (-200) and before Docker DNAT (-100).
	conntrackPrio = -150
)

// a2sHeaders are the first payload byte after 0xFFFFFFFF of Steam server
// queries and connection challenges: A2S_INFO, A2S_PLAYER, A2S_RULES,
// A2S_SERVERQUERY_GETCHALLENGE, A2A_PING and the Source connect challenge.
const a2sHeaders = "{ 0x54, 0x55, 0x56, 0x57, 0x69, 0x71 }"

// chainName is the nft name of rule i. Names come from the index, never from
// user input.
func chainName(i int) string { return "r" + strconv.Itoa(i) }

// CounterReasons are the drop reasons a rule counts, in display order.
var CounterReasons = []string{"block", "ban", "bad", "amp", "query", "syn", "flood", "cap", "conns"}

type renderer struct {
	b       strings.Builder
	monitor bool
}

func (r *renderer) line(indent int, format string, args ...any) {
	r.b.WriteString(strings.Repeat("  ", indent))
	fmt.Fprintf(&r.b, format, args...)
	r.b.WriteString("\n")
}

// drop is the verdict for a matched packet: drop, or count and let it
// through in monitor mode.
func (r *renderer) drop() string {
	if r.monitor {
		return "accept"
	}
	return "drop"
}

func split(cidrs []string) (v4, v6 []string) {
	for _, c := range cidrs {
		p, err := ParseCIDR(c)
		if err != nil {
			continue
		}
		if p.Addr().Is4() {
			v4 = append(v4, p.String())
		} else {
			v6 = append(v6, p.String())
		}
	}
	return v4, v6
}

func (r *renderer) intervalSet(name, typ string, elems []string) {
	if len(elems) == 0 {
		r.line(1, "set %s { type %s; flags interval; }", name, typ)
		return
	}
	r.line(1, "set %s { type %s; flags interval; auto-merge; elements = { %s } }", name, typ, strings.Join(elems, ", "))
}

func (r *renderer) pairSet(name string, cidrs []string) {
	v4, v6 := split(cidrs)
	r.intervalSet(name+"4", "ipv4_addr", v4)
	r.intervalSet(name+"6", "ipv6_addr", v6)
}

func (r *renderer) dynSet(name, typ string, size int, timeout string) {
	if timeout == "" {
		r.line(1, "set %s { type %s; flags dynamic; size %d; }", name, typ, size)
		return
	}
	r.line(1, "set %s { type %s; flags dynamic,timeout; timeout %s; size %d; }", name, typ, timeout, size)
}

func (r *renderer) dynPair(name string, size int, timeout string) {
	r.dynSet(name+"4", "ipv4_addr", size, timeout)
	r.dynSet(name+"6", "ipv6_addr", size, timeout)
}

func hostAddrs(env Env) []string {
	out := make([]string, 0, len(env.Host))
	for _, a := range env.Host {
		a = a.Unmap()
		out = append(out, netip.PrefixFrom(a, a.BitLen()).String())
	}
	sort.Strings(out)
	return out
}

func usesUDP(p string) bool { return p == ProtoUDP || p == ProtoBoth || p == ProtoAny }
func usesTCP(p string) bool { return p == ProtoTCP || p == ProtoBoth || p == ProtoAny }

// l4Match is the protocol and port part of a dispatch rule.
func l4Match(rule Rule) string {
	ports := ""
	if len(rule.Ports) > 0 {
		ports = "{ " + strings.Join(rule.Ports, ", ") + " }"
	}
	switch rule.Protocol {
	case ProtoUDP:
		if ports == "" {
			return "meta l4proto udp"
		}
		return "udp dport " + ports
	case ProtoTCP:
		if ports == "" {
			return "meta l4proto tcp"
		}
		return "tcp dport " + ports
	case ProtoBoth:
		if ports == "" {
			return "meta l4proto { tcp, udp }"
		}
		return "meta l4proto { tcp, udp } th dport " + ports
	}
	return ""
}

func join(parts ...string) string {
	out := parts[:0]
	for _, p := range parts {
		if p != "" {
			out = append(out, p)
		}
	}
	return strings.Join(out, " ")
}

func banSeconds(rule Rule) int {
	if rule.BanSeconds > 0 {
		return rule.BanSeconds
	}
	return DefaultBanSeconds
}

func burst(rate, b int) int {
	if b > 0 {
		return b
	}
	return rate * 2
}

func needsConntrack(rule Rule) bool {
	return rule.Limits.ConnsPerSource > 0 && usesTCP(rule.Protocol)
}

// Render returns the nft script that replaces both No-dal shield tables.
// It is applied with nft -f as one transaction.
func Render(spec Spec, env Env) (string, error) {
	if err := spec.Normalize(); err != nil {
		return "", err
	}
	if len(env.Devices) == 0 {
		return "", fmt.Errorf("no interface to filter on")
	}
	for _, d := range env.Devices {
		if !deviceRE.MatchString(d) {
			return "", fmt.Errorf("interface %q is not a valid name", d)
		}
	}
	r := &renderer{monitor: spec.Mode == ModeMonitor}
	r.line(0, "destroy table netdev %s", TableName)
	r.line(0, "destroy table inet %s", TableName)
	r.renderNetdev(spec, env)
	conntrack := false
	for _, rule := range spec.Rules {
		if needsConntrack(rule) {
			conntrack = true
		}
	}
	if conntrack {
		r.renderInet(spec, env)
	}
	r.renderBans(spec, env)
	return r.b.String(), nil
}

func (r *renderer) commonSets(spec Spec, env Env) {
	r.pairSet("host", hostAddrs(env))
	r.pairSet("trusted", trusted(spec, env))
}

// trusted is the spec's trusted networks, plus loopback and, with
// TrustLocal, the node's own networks.
func trusted(spec Spec, env Env) []string {
	out := append([]string{"127.0.0.0/8", "::1/128"}, spec.Trusted...)
	if spec.TrustLocal {
		for _, p := range env.Local {
			out = append(out, p.Masked().String())
		}
	}
	clean, _ := cleanCIDRs("trusted", out)
	return clean
}

// dests returns the dispatch prefixes (v4 then v6) for a rule.
func dests(i int, rule Rule) []string {
	var out []string
	name := chainName(i)
	if rule.Host {
		out = append(out, "ip daddr @host4", "ip6 daddr @host6")
	}
	if len(rule.Dest) > 0 {
		out = append(out, "ip daddr @"+name+"_d4", "ip6 daddr @"+name+"_d6")
	}
	return out
}

func (r *renderer) renderNetdev(spec Spec, env Env) {
	devs := make([]string, 0, len(env.Devices))
	for _, d := range env.Devices {
		devs = append(devs, `"`+d+`"`)
	}
	r.line(0, "table netdev %s {", TableName)
	r.commonSets(spec, env)
	r.pairSet("blocked", spec.Blocked)
	r.line(1, "set ban4 { type ipv4_addr; flags timeout; size %d; }", banSetSize)
	r.line(1, "set ban6 { type ipv6_addr; flags timeout; size %d; }", banSetSize)
	amp := make([]string, 0, len(AmplificationPorts))
	for _, p := range AmplificationPorts {
		amp = append(amp, strconv.Itoa(p))
	}
	r.line(1, "set amp_ports { type inet_service; elements = { %s } }", strings.Join(amp, ", "))
	r.line(1, "counter blocked {}")
	r.line(1, "counter banned {}")
	for i, rule := range spec.Rules {
		name := chainName(i)
		if len(rule.Dest) > 0 {
			r.pairSet(name+"_d", rule.Dest)
		}
		r.pairSet(name+"_allow", rule.Allow)
		r.pairSet(name+"_block", rule.Block)
		if rule.Action == ActionBan && rule.Limits.PerSourcePPS > 0 {
			r.dynPair(name+"_ban", banSetSize, strconv.Itoa(banSeconds(rule))+"s")
		}
		if rule.Limits.PerSourcePPS > 0 {
			r.dynPair(name+"_pps", meterSize, meterTTL)
		}
		if rule.Limits.QueryPerSource > 0 && usesUDP(rule.Protocol) {
			r.dynPair(name+"_q", meterSize, meterTTL)
		}
		if rule.Limits.SYNPerSource > 0 && usesTCP(rule.Protocol) {
			r.dynPair(name+"_syn", meterSize, meterTTL)
		}
		r.line(1, "counter %s_in {}", name)
		for _, reason := range CounterReasons {
			if reason != "conns" {
				r.line(1, "counter %s_%s {}", name, reason)
			}
		}
	}
	r.line(1, "chain ingress {")
	r.line(2, "type filter hook ingress devices = { %s } priority %d; policy accept;", strings.Join(devs, ", "), ingressPrio)
	r.line(2, "ip saddr @trusted4 accept")
	r.line(2, "ip6 saddr @trusted6 accept")
	r.line(2, `ip saddr @blocked4 counter name "blocked" %s`, r.drop())
	r.line(2, `ip6 saddr @blocked6 counter name "blocked" %s`, r.drop())
	r.line(2, `ip saddr @ban4 counter name "banned" %s`, r.drop())
	r.line(2, `ip6 saddr @ban6 counter name "banned" %s`, r.drop())
	for i, rule := range spec.Rules {
		for _, d := range dests(i, rule) {
			r.line(2, `%s jump %s comment "%s"`, join(d, l4Match(rule)), chainName(i), commentSafe(rule.ID))
		}
	}
	r.line(1, "}")
	for i, rule := range spec.Rules {
		r.ruleChain(i, rule, spec)
	}
	r.line(0, "}")
}

func commentSafe(v string) string {
	return strings.Map(func(c rune) rune {
		if c == '"' || c == '\\' || c < 0x20 {
			return -1
		}
		return c
	}, v)
}

func (r *renderer) ruleChain(i int, rule Rule, spec Spec) {
	n := chainName(i)
	l := rule.Limits
	drop := r.drop()
	c := func(reason string) string { return fmt.Sprintf(`counter name "%s_%s" %s`, n, reason, drop) }
	r.line(1, "chain %s {", n)
	r.line(2, `counter name "%s_in"`, n)
	r.line(2, "ip saddr @%s_allow4 accept", n)
	r.line(2, "ip6 saddr @%s_allow6 accept", n)
	r.line(2, "ip saddr @%s_block4 %s", n, c("block"))
	r.line(2, "ip6 saddr @%s_block6 %s", n, c("block"))
	ban := rule.Action == ActionBan && l.PerSourcePPS > 0
	if ban {
		r.line(2, "ip saddr @%s_ban4 %s", n, c("ban"))
		r.line(2, "ip6 saddr and %s @%s_ban6 %s", v6Mask, n, c("ban"))
	}
	if l.DropFragments {
		r.line(2, "ip frag-off & 0x2000 != 0 %s", c("bad"))
		r.line(2, "exthdr frag exists %s", c("bad"))
	}
	if l.MaxPacket > 0 {
		r.line(2, "meta length > %d %s", l.MaxPacket, c("bad"))
	}
	if spec.DropInvalidTCP && usesTCP(rule.Protocol) {
		r.line(2, "tcp flags & (fin|syn|rst|psh|ack|urg) == 0x0 %s", c("bad"))
		r.line(2, "tcp flags & (fin|syn) == (fin|syn) %s", c("bad"))
		r.line(2, "tcp flags & (syn|rst) == (syn|rst) %s", c("bad"))
		r.line(2, "tcp flags & (fin|psh|urg) == (fin|psh|urg) %s", c("bad"))
	}
	if usesUDP(rule.Protocol) {
		if l.BlockAmplification {
			r.line(2, "udp sport @amp_ports %s", c("amp"))
		}
		if l.MinUDPPayload > 0 {
			r.line(2, "udp length < %d %s", 8+l.MinUDPPayload, c("bad"))
		}
		if l.QueryPerSource > 0 {
			q := l.QueryPerSource
			r.line(2, "meta l4proto udp @th,64,32 0xffffffff @th,96,8 %s update @%s_q4 { ip saddr limit rate over %d/second burst %d packets } %s",
				a2sHeaders, n, q, burst(q, 0), c("query"))
			r.line(2, "meta l4proto udp @th,64,32 0xffffffff @th,96,8 %s update @%s_q6 { ip6 saddr and %s limit rate over %d/second burst %d packets } %s",
				a2sHeaders, n, v6Mask, q, burst(q, 0), c("query"))
		}
	}
	if usesTCP(rule.Protocol) && l.SYNPerSource > 0 {
		s := l.SYNPerSource
		r.line(2, "tcp flags & (syn|ack) == syn update @%s_syn4 { ip saddr limit rate over %d/second burst %d packets } %s", n, s, burst(s, 0), c("syn"))
		r.line(2, "tcp flags & (syn|ack) == syn update @%s_syn6 { ip6 saddr and %s limit rate over %d/second burst %d packets } %s", n, v6Mask, s, burst(s, 0), c("syn"))
	}
	if l.PerSourcePPS > 0 {
		p, b := l.PerSourcePPS, burst(l.PerSourcePPS, l.PerSourceBurst)
		ban4, ban6 := "", ""
		if ban && !r.monitor {
			ban4 = fmt.Sprintf("update @%s_ban4 { ip saddr } ", n)
			ban6 = fmt.Sprintf("update @%s_ban6 { ip6 saddr and %s } ", n, v6Mask)
		}
		r.line(2, "update @%s_pps4 { ip saddr limit rate over %d/second burst %d packets } %s%s", n, p, b, ban4, c("flood"))
		r.line(2, "update @%s_pps6 { ip6 saddr and %s limit rate over %d/second burst %d packets } %s%s", n, v6Mask, p, b, ban6, c("flood"))
	}
	if l.TotalPPS > 0 {
		b := l.TotalPPS / 5
		if b < 100 {
			b = 100
		}
		r.line(2, "limit rate over %d/second burst %d packets %s", l.TotalPPS, b, c("cap"))
	}
	r.line(2, "accept")
	r.line(1, "}")
}

func (r *renderer) renderInet(spec Spec, env Env) {
	r.line(0, "table inet %s {", TableName)
	r.commonSets(spec, env)
	for i, rule := range spec.Rules {
		if !needsConntrack(rule) {
			continue
		}
		n := chainName(i)
		if len(rule.Dest) > 0 {
			r.pairSet(n+"_d", rule.Dest)
		}
		r.pairSet(n+"_allow", rule.Allow)
		r.dynPair(n+"_ct", meterSize, "")
		r.line(1, "counter %s_conns {}", n)
	}
	r.line(1, "chain prerouting {")
	r.line(2, "type filter hook prerouting priority %d; policy accept;", conntrackPrio)
	r.line(2, "ip saddr @trusted4 accept")
	r.line(2, "ip6 saddr @trusted6 accept")
	for i, rule := range spec.Rules {
		if !needsConntrack(rule) {
			continue
		}
		tcp := rule
		tcp.Protocol = ProtoTCP
		for _, d := range dests(i, rule) {
			r.line(2, `%s ct state new jump %s comment "%s"`, join(d, l4Match(tcp)), chainName(i), commentSafe(rule.ID))
		}
	}
	r.line(1, "}")
	for i, rule := range spec.Rules {
		if !needsConntrack(rule) {
			continue
		}
		n := chainName(i)
		max := rule.Limits.ConnsPerSource
		r.line(1, "chain %s {", n)
		r.line(2, "ip saddr @%s_allow4 accept", n)
		r.line(2, "ip6 saddr @%s_allow6 accept", n)
		r.line(2, `add @%s_ct4 { ip saddr ct count over %d } counter name "%s_conns" %s`, n, max, n, r.drop())
		r.line(2, `add @%s_ct6 { ip6 saddr and %s ct count over %d } counter name "%s_conns" %s`, n, v6Mask, max, n, r.drop())
		r.line(2, "accept")
		r.line(1, "}")
	}
	r.line(0, "}")
}

// Ban is one banned address.
type Ban struct {
	Addr string `json:"addr"`
	// RuleID is the target the address is banned from; empty for a ban
	// added by hand, which covers every target.
	RuleID string `json:"rule_id,omitempty"`
	// Seconds is the time left.
	Seconds int `json:"seconds"`
}

func (r *renderer) renderBans(spec Spec, env Env) {
	keys := make([]string, 0, len(spec.Bans))
	for a := range spec.Bans {
		keys = append(keys, a)
	}
	sort.Strings(keys)
	for _, a := range keys {
		left := spec.Bans[a] - env.Now
		if left < 1 {
			continue
		}
		addr, err := ParseAddr(a)
		if err != nil {
			continue
		}
		r.line(0, "add element netdev %s ban%s { %s timeout %ds }", TableName, family(addr), addr, left)
	}
	for i, rule := range spec.Rules {
		if rule.Action != ActionBan || rule.Limits.PerSourcePPS <= 0 || r.monitor {
			continue
		}
		for _, b := range env.Carry[rule.ID] {
			addr, err := ParseAddr(b.Addr)
			if err != nil || b.Seconds < 1 {
				continue
			}
			secs := b.Seconds
			if max := banSeconds(rule); secs > max {
				secs = max
			}
			r.line(0, "add element netdev %s %s_ban%s { %s timeout %ds }", TableName, chainName(i), family(addr), addr, secs)
		}
	}
}

func family(a netip.Addr) string {
	if a.Is4() {
		return "4"
	}
	return "6"
}
