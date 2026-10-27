// Package shield is No-dal's DDoS protection. It filters traffic for the
// services ndl-ce runs (game servers, workloads, host ports and rules people
// define) in a No-dal-owned nftables netdev table on the node's uplink, so
// floods are dropped on ingress before conntrack, Docker NAT or the bridge
// see them. Per-source connection limits need conntrack and live in a small
// inet table at prerouting. Both tables are replaced as a whole on apply and
// never touch the host's own ruleset.
//
// On-host filtering cannot help when an attack fills the uplink itself; that
// needs filtering upstream (the provider, or a scrubbing service).
package shield

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/netip"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

const (
	// TableName is the No-dal-owned table in the netdev and inet families.
	TableName = "ndl_shield"

	ModeEnforce = "enforce"
	ModeMonitor = "monitor"

	ActionBan  = "ban"
	ActionDrop = "drop"

	ProtoUDP  = "udp"
	ProtoTCP  = "tcp"
	ProtoBoth = "both"
	ProtoAny  = "any"

	DefaultBanSeconds = 300
	maxBanSeconds     = 7 * 24 * 3600
	maxRules          = 256
	maxCIDRs          = 4096
	maxPortsPerRule   = 64
)

// Limits are one rule's thresholds. Zero turns a numeric limit off.
type Limits struct {
	// PerSourcePPS caps packets per second from one address (an IPv6 /64).
	PerSourcePPS int `json:"per_source_pps,omitempty"`
	// PerSourceBurst is how far a source may go over PerSourcePPS briefly.
	// Zero means twice PerSourcePPS.
	PerSourceBurst int `json:"per_source_burst,omitempty"`
	// TotalPPS caps packets per second to the target from everyone. It is a
	// last resort that keeps the host responsive; legitimate players over the
	// cap are dropped too.
	TotalPPS int `json:"total_pps,omitempty"`
	// SYNPerSource caps new TCP connections per second from one source.
	SYNPerSource int `json:"syn_per_source,omitempty"`
	// ConnsPerSource caps open TCP connections from one source.
	ConnsPerSource int `json:"conns_per_source,omitempty"`
	// QueryPerSource caps Steam server queries (A2S, also used by the server
	// browser) per second from one source. Query floods are the usual attack
	// on Source engine servers and a reflection source when left open.
	QueryPerSource int `json:"query_per_source,omitempty"`
	// MinUDPPayload drops UDP packets with fewer payload bytes.
	MinUDPPayload int `json:"min_udp_payload,omitempty"`
	// MaxPacket drops IP packets longer than this many bytes.
	MaxPacket int `json:"max_packet,omitempty"`
	// BlockAmplification drops UDP from ports used by reflection attacks
	// (DNS, NTP, SSDP, memcached and similar). Game traffic never comes from
	// them.
	BlockAmplification bool `json:"block_amplification,omitempty"`
	// DropFragments drops IP fragments. Source engine and most games split
	// packets themselves, so fragments to them are attack traffic.
	DropFragments bool `json:"drop_fragments,omitempty"`
}

// Rule protects one target.
type Rule struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	// Host matches traffic to this node's own addresses (host services and
	// game servers, which publish their ports on the node).
	Host bool `json:"host,omitempty"`
	// Dest matches traffic to these addresses or CIDRs (workloads, custom).
	Dest     []string `json:"dest,omitempty"`
	Protocol string   `json:"protocol"`
	// Ports are single ports or ranges ("27015", "27015-27020"). Empty
	// matches every port.
	Ports  []string `json:"ports,omitempty"`
	Limits Limits   `json:"limits"`
	// Action is what happens to a source over a per-source limit: drop the
	// excess, or ban the source from this target for BanSeconds.
	Action     string `json:"action"`
	BanSeconds int    `json:"ban_seconds,omitempty"`
	// Allow is always let through to this target; Block never is.
	Allow []string `json:"allow,omitempty"`
	Block []string `json:"block,omitempty"`
}

// Spec is the whole protection for one node.
type Spec struct {
	Mode string `json:"mode"`
	// Devices are the interfaces to filter on. Empty finds the uplink.
	Devices []string `json:"devices,omitempty"`
	// Trusted is always let through and never banned. Blocked never is.
	Trusted []string `json:"trusted,omitempty"`
	Blocked []string `json:"blocked,omitempty"`
	// TrustLocal also trusts the networks this node is directly on (its
	// LAN), so local players and admins are never limited.
	TrustLocal bool `json:"trust_local,omitempty"`
	// DropInvalidTCP drops TCP packets with impossible flag combinations to
	// protected targets.
	DropInvalidTCP bool `json:"drop_invalid_tcp,omitempty"`
	// Hardening raises kernel limits against SYN and conntrack floods.
	Hardening bool   `json:"hardening,omitempty"`
	Rules     []Rule `json:"rules"`
	// Bans are added by hand: address -> when the ban ends (Unix seconds).
	// They block the address from every target.
	Bans map[string]int64 `json:"bans,omitempty"`
}

// Hash identifies a spec, so callers can skip applying an unchanged one.
// Bans are left out: they are added and removed without a re-apply.
func (s Spec) Hash() string {
	s.Bans = nil
	raw, _ := json.Marshal(s)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:8])
}

// Profile is a starting set of limits for one kind of service.
type Profile struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	Summary  string `json:"summary"`
	Protocol string `json:"protocol"`
	Limits   Limits `json:"limits"`
	Action   string `json:"action"`
}

// Profile IDs.
const (
	ProfileSource    = "source"
	ProfileMinecraft = "minecraft"
	ProfileGameUDP   = "game-udp"
	ProfileGameTCP   = "game-tcp"
	ProfileWeb       = "web"
	ProfileStrict    = "strict"
	ProfileCustom    = "custom"
)

// Profiles lists the built-in profiles. Limits are generous for real
// players: a Source client sends 66-130 packets per second, so 400 leaves room
// for joins and downloads.
func Profiles() []Profile {
	return []Profile{
		{
			ID: ProfileSource, Title: "Source engine",
			Summary:  "Garry's Mod, Counter-Strike, Team Fortress 2, Left 4 Dead and other Source servers. Limits server queries, drops reflection traffic, fragments and empty packets.",
			Protocol: ProtoBoth, Action: ActionBan,
			Limits: Limits{
				PerSourcePPS: 400, PerSourceBurst: 1200, QueryPerSource: 5, SYNPerSource: 5, ConnsPerSource: 4,
				MinUDPPayload: 4, BlockAmplification: true, DropFragments: true,
			},
		},
		{
			ID: ProfileMinecraft, Title: "Minecraft",
			Summary:  "Java and Bedrock servers. Limits new and open connections per address, the usual bot-join flood.",
			Protocol: ProtoBoth, Action: ActionBan,
			Limits: Limits{
				PerSourcePPS: 1500, SYNPerSource: 6, ConnsPerSource: 6, BlockAmplification: true,
			},
		},
		{
			ID: ProfileGameUDP, Title: "UDP game",
			Summary:  "Steam and other UDP games (Rust, ARK, Valheim, FiveM and similar). Limits packets and server queries per address and drops reflection traffic.",
			Protocol: ProtoBoth, Action: ActionBan,
			Limits: Limits{
				PerSourcePPS: 600, PerSourceBurst: 1800, QueryPerSource: 10, SYNPerSource: 10, ConnsPerSource: 8,
				BlockAmplification: true,
			},
		},
		{
			ID: ProfileGameTCP, Title: "TCP game",
			Summary:  "Games that talk over TCP. Limits new and open connections per address.",
			Protocol: ProtoTCP, Action: ActionBan,
			Limits: Limits{PerSourcePPS: 2000, SYNPerSource: 10, ConnsPerSource: 10},
		},
		{
			ID: ProfileWeb, Title: "Web service",
			Summary:  "HTTP and HTTPS. Allows many connections per address (browsers, shared offices) but limits SYN floods.",
			Protocol: ProtoTCP, Action: ActionDrop,
			Limits: Limits{PerSourcePPS: 5000, SYNPerSource: 50, ConnsPerSource: 128},
		},
		{
			ID: ProfileStrict, Title: "Under attack",
			Summary:  "Tight limits while an attack is on. Real players may notice; switch back once it ends.",
			Protocol: ProtoBoth, Action: ActionBan,
			Limits: Limits{
				PerSourcePPS: 200, PerSourceBurst: 400, QueryPerSource: 2, SYNPerSource: 3, ConnsPerSource: 3,
				MinUDPPayload: 4, BlockAmplification: true, DropFragments: true, MaxPacket: 1400,
			},
		},
		{
			ID: ProfileCustom, Title: "Custom",
			Summary:  "Start from nothing and set every limit yourself.",
			Protocol: ProtoBoth, Action: ActionDrop,
		},
	}
}

// LookupProfile returns a built-in profile.
func LookupProfile(id string) (Profile, bool) {
	for _, p := range Profiles() {
		if p.ID == id {
			return p, true
		}
	}
	return Profile{}, false
}

// ProfileForGame picks the profile for a game server template family.
func ProfileForGame(family string, tcpOnly bool) string {
	switch strings.ToLower(strings.TrimSpace(family)) {
	case "source":
		return ProfileSource
	case "minecraft", "minecraft-bedrock":
		return ProfileMinecraft
	}
	if tcpOnly {
		return ProfileGameTCP
	}
	return ProfileGameUDP
}

// AmplificationPorts are UDP source ports of common reflection attacks.
var AmplificationPorts = []int{
	17, 19, 53, 69, 111, 123, 137, 161, 389, 427, 520, 1434, 1900, 3283, 3702, 5353, 5683, 10001, 11211,
}

var portRangeRE = regexp.MustCompile(`^([0-9]{1,5})(?:-([0-9]{1,5}))?$`)

// ParsePort validates "27015" or "27015-27020" and returns it normalized.
func ParsePort(v string) (string, error) {
	m := portRangeRE.FindStringSubmatch(strings.TrimSpace(v))
	if m == nil {
		return "", fmt.Errorf("port %q must be a number or a range like 27015-27020", v)
	}
	lo, _ := strconv.Atoi(m[1])
	hi := lo
	if m[2] != "" {
		hi, _ = strconv.Atoi(m[2])
	}
	if lo < 1 || hi > 65535 || hi < lo {
		return "", fmt.Errorf("port %q must be between 1 and 65535", v)
	}
	if lo == hi {
		return strconv.Itoa(lo), nil
	}
	return fmt.Sprintf("%d-%d", lo, hi), nil
}

// ParseCIDR accepts an address or a CIDR and returns the masked prefix.
func ParseCIDR(v string) (netip.Prefix, error) {
	v = strings.TrimSpace(v)
	if strings.Contains(v, "/") {
		p, err := netip.ParsePrefix(v)
		if err != nil {
			return netip.Prefix{}, fmt.Errorf("%q is not an address or CIDR", v)
		}
		return p.Masked(), nil
	}
	a, err := netip.ParseAddr(v)
	if err != nil {
		return netip.Prefix{}, fmt.Errorf("%q is not an address or CIDR", v)
	}
	a = a.Unmap()
	return netip.PrefixFrom(a, a.BitLen()), nil
}

// ParseAddr accepts a single address (for bans).
func ParseAddr(v string) (netip.Addr, error) {
	a, err := netip.ParseAddr(strings.TrimSpace(v))
	if err != nil {
		return netip.Addr{}, fmt.Errorf("%q is not an IP address", v)
	}
	return a.Unmap(), nil
}

func cleanCIDRs(field string, in []string) ([]string, error) {
	if len(in) > maxCIDRs {
		return nil, fmt.Errorf("%s has more than %d entries", field, maxCIDRs)
	}
	ps := make([]netip.Prefix, 0, len(in))
	for _, v := range in {
		if strings.TrimSpace(v) == "" {
			continue
		}
		p, err := ParseCIDR(v)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", field, err)
		}
		ps = append(ps, p)
	}
	// Wider prefixes first, so a prefix inside one already kept is dropped:
	// nft refuses overlapping elements in an interval set.
	sort.Slice(ps, func(i, j int) bool {
		if ps[i].Bits() != ps[j].Bits() {
			return ps[i].Bits() < ps[j].Bits()
		}
		return ps[i].Addr().Less(ps[j].Addr())
	})
	var kept []netip.Prefix
	for _, p := range ps {
		inside := false
		for _, k := range kept {
			if k.Addr().BitLen() == p.Addr().BitLen() && k.Contains(p.Addr()) {
				inside = true
				break
			}
		}
		if !inside {
			kept = append(kept, p)
		}
	}
	out := make([]string, 0, len(kept))
	for _, p := range kept {
		out = append(out, p.String())
	}
	sort.Strings(out)
	return out, nil
}

var ruleIDRE = regexp.MustCompile(`^[A-Za-z0-9:._-]{1,80}$`)
var deviceRE = regexp.MustCompile(`^[A-Za-z0-9._-]{1,15}$`)

func validProtocol(p string) bool {
	switch p {
	case ProtoUDP, ProtoTCP, ProtoBoth, ProtoAny:
		return true
	}
	return false
}

func nonNegative(name string, v, max int) error {
	if v < 0 || v > max {
		return fmt.Errorf("%s must be between 0 and %d", name, max)
	}
	return nil
}

// Validate checks limits and normalizes nothing.
func (l Limits) Validate() error {
	checks := []struct {
		name string
		v    int
		max  int
	}{
		{"per-source packets per second", l.PerSourcePPS, 10_000_000},
		{"per-source burst", l.PerSourceBurst, 100_000_000},
		{"total packets per second", l.TotalPPS, 100_000_000},
		{"new connections per second per source", l.SYNPerSource, 1_000_000},
		{"open connections per source", l.ConnsPerSource, 1_000_000},
		{"server queries per second per source", l.QueryPerSource, 1_000_000},
		{"minimum UDP payload", l.MinUDPPayload, 1400},
		{"maximum packet size", l.MaxPacket, 65535},
	}
	for _, c := range checks {
		if err := nonNegative(c.name, c.v, c.max); err != nil {
			return err
		}
	}
	if l.MaxPacket > 0 && l.MaxPacket < 64 {
		return fmt.Errorf("maximum packet size must be at least 64 bytes")
	}
	return nil
}

// Normalize validates a rule and cleans its lists.
func (r *Rule) Normalize() error {
	r.ID = strings.TrimSpace(r.ID)
	if !ruleIDRE.MatchString(r.ID) {
		return fmt.Errorf("rule id %q is invalid", r.ID)
	}
	r.Name = strings.TrimSpace(r.Name)
	if r.Name == "" {
		r.Name = r.ID
	}
	r.Protocol = strings.ToLower(strings.TrimSpace(r.Protocol))
	if r.Protocol == "" {
		r.Protocol = ProtoBoth
	}
	if !validProtocol(r.Protocol) {
		return fmt.Errorf("%s: protocol must be udp, tcp, both or any", r.Name)
	}
	r.Action = strings.ToLower(strings.TrimSpace(r.Action))
	if r.Action == "" {
		r.Action = ActionDrop
	}
	if r.Action != ActionBan && r.Action != ActionDrop {
		return fmt.Errorf("%s: action must be ban or drop", r.Name)
	}
	if err := nonNegative(r.Name+": ban length", r.BanSeconds, maxBanSeconds); err != nil {
		return err
	}
	if err := r.Limits.Validate(); err != nil {
		return fmt.Errorf("%s: %w", r.Name, err)
	}
	if len(r.Ports) > maxPortsPerRule {
		return fmt.Errorf("%s: more than %d ports", r.Name, maxPortsPerRule)
	}
	ports := make([]string, 0, len(r.Ports))
	seen := map[string]bool{}
	for _, p := range r.Ports {
		if strings.TrimSpace(p) == "" {
			continue
		}
		v, err := ParsePort(p)
		if err != nil {
			return fmt.Errorf("%s: %w", r.Name, err)
		}
		if !seen[v] {
			seen[v] = true
			ports = append(ports, v)
		}
	}
	sort.Slice(ports, func(i, j int) bool { return portLow(ports[i]) < portLow(ports[j]) })
	r.Ports = ports
	if len(r.Ports) > 0 && r.Protocol == ProtoAny {
		return fmt.Errorf("%s: ports need protocol udp, tcp or both", r.Name)
	}
	var err error
	if r.Dest, err = cleanCIDRs(r.Name+": destination", r.Dest); err != nil {
		return err
	}
	if !r.Host && len(r.Dest) == 0 {
		return fmt.Errorf("%s: protect this node or at least one destination address", r.Name)
	}
	if r.Allow, err = cleanCIDRs(r.Name+": allow list", r.Allow); err != nil {
		return err
	}
	if r.Block, err = cleanCIDRs(r.Name+": block list", r.Block); err != nil {
		return err
	}
	return nil
}

func portLow(p string) int {
	lo, _, _ := strings.Cut(p, "-")
	n, _ := strconv.Atoi(lo)
	return n
}

// Normalize validates the spec and cleans its lists.
func (s *Spec) Normalize() error {
	s.Mode = strings.ToLower(strings.TrimSpace(s.Mode))
	if s.Mode == "" {
		s.Mode = ModeEnforce
	}
	if s.Mode != ModeEnforce && s.Mode != ModeMonitor {
		return fmt.Errorf("mode must be enforce or monitor")
	}
	devs := make([]string, 0, len(s.Devices))
	for _, d := range s.Devices {
		d = strings.TrimSpace(d)
		if d == "" {
			continue
		}
		if !deviceRE.MatchString(d) {
			return fmt.Errorf("interface %q is not a valid name", d)
		}
		devs = append(devs, d)
	}
	sort.Strings(devs)
	s.Devices = devs
	var err error
	if s.Trusted, err = cleanCIDRs("trusted networks", s.Trusted); err != nil {
		return err
	}
	if s.Blocked, err = cleanCIDRs("blocked networks", s.Blocked); err != nil {
		return err
	}
	if len(s.Rules) > maxRules {
		return fmt.Errorf("more than %d protected targets", maxRules)
	}
	ids := map[string]bool{}
	for i := range s.Rules {
		if err := s.Rules[i].Normalize(); err != nil {
			return err
		}
		if ids[s.Rules[i].ID] {
			return fmt.Errorf("rule id %q is used twice", s.Rules[i].ID)
		}
		ids[s.Rules[i].ID] = true
	}
	bans := map[string]int64{}
	for a, until := range s.Bans {
		addr, err := ParseAddr(a)
		if err != nil {
			return err
		}
		bans[addr.String()] = until
	}
	if len(bans) == 0 {
		bans = nil
	}
	s.Bans = bans
	return nil
}
