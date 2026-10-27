// Package edge forwards public traffic from an edge node (a VPS with a
// public address) through a WireGuard tunnel to an origin node that has no
// public address of its own, such as a home server behind a router.
//
// Players connect to the edge. The edge rewrites only the destination
// (DNAT) into the tunnel and leaves the source alone, so the origin sees
// every player's real address: bans, per-source limits and server logs keep
// working. The origin marks connections that arrive through the tunnel and
// routes their replies back through it (policy routing), and it can send a
// service's own outbound traffic (Steam master server heartbeats) out
// through the edge too, so the server is listed with the edge's address.
package edge

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

// Roles a node plays in a tunnel.
const (
	RoleEdge   = "edge"
	RoleOrigin = "origin"
)

const (
	// IfPrefix starts every tunnel interface No-dal owns.
	IfPrefix = "ndle"
	// MarkBase is the firewall mark of tunnel 1; tunnel n uses MarkBase+n.
	MarkBase = 0x6e640000
	// TableBase is the routing table of tunnel 1; tunnel n uses TableBase+n.
	TableBase = 52000
	// RulePriority is the ip rule priority of tunnel 1; tunnel n uses
	// RulePriority+n. It sits before the main table (32766).
	RulePriority = 5200
	// MaxIndex bounds tunnels per node.
	MaxIndex = 250
	// MTU leaves room for WireGuard over a 1500 byte uplink.
	MTU = 1420
	// Keepalive keeps the origin's router mapping open so the edge can
	// always reach it.
	Keepalive = 25
)

// Spec is everything a node does for edge forwarding.
type Spec struct {
	Tunnels []Tunnel `json:"tunnels"`
}

// Tunnel is one side of a WireGuard link between an edge and an origin.
type Tunnel struct {
	// ID names the tunnel (the same on both sides). The interface is
	// IfPrefix plus the first 8 hex digits of its hash.
	ID   string `json:"id"`
	Role string `json:"role"`
	// Index picks the firewall mark, routing table and rule priority.
	Index int `json:"index"`
	// Address is this side's tunnel address with its prefix (10.66.0.1/30).
	Address string `json:"address"`
	// Peer is the other side's tunnel address.
	Peer string `json:"peer"`
	// PeerKey is the other side's public key. Empty until it is known; the
	// interface is still created so this side's key exists.
	PeerKey string `json:"peer_key,omitempty"`
	// ListenPort is the edge's UDP port. The origin dials out to it.
	ListenPort int `json:"listen_port,omitempty"`
	// Endpoint is the edge's public host:port (origin only).
	Endpoint string `json:"endpoint,omitempty"`
	// Forwards are the ports this tunnel carries.
	Forwards []Forward `json:"forwards,omitempty"`
	// Egress (origin only) are Docker containers (by name) or addresses
	// whose new outbound connections to the internet leave through the edge.
	Egress []string `json:"egress,omitempty"`
}

// Forward is one public port, or a range, carried through a tunnel.
type Forward struct {
	ID       string `json:"id"`
	Protocol string `json:"protocol"` // udp or tcp
	// Port and PortEnd are the public ports on the edge (PortEnd 0 is one port).
	Port    int `json:"port"`
	PortEnd int `json:"port_end,omitempty"`
	// ToPort is the first port on the origin (0 is the same as Port).
	ToPort int `json:"to_port,omitempty"`
	// Target (origin only) is a LAN address on the origin's side to pass the
	// traffic on to, such as a system container. Empty is the origin itself,
	// which includes game servers and Docker containers it publishes.
	Target string `json:"target,omitempty"`
	// Masquerade (origin only, with Target) hides the player's address from
	// Target. Needed when Target sends replies to its own gateway instead of
	// back through the origin.
	Masquerade bool `json:"masquerade,omitempty"`
}

// IfName is the interface for a tunnel ID.
func IfName(id string) string {
	sum := sha256.Sum256([]byte(id))
	return IfPrefix + hex.EncodeToString(sum[:])[:8]
}

// Mark is the firewall mark for a tunnel index.
func Mark(index int) uint32 { return uint32(MarkBase + index) }

// Table is the routing table for a tunnel index.
func Table(index int) int { return TableBase + index }

var idPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9:._-]{0,95}$`)

// Hash identifies a spec, so an unchanged spec is not applied again.
func (s Spec) Hash() string {
	raw, _ := json.Marshal(s)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

// Normalize validates a spec and puts it in a stable order.
func Normalize(s Spec) (Spec, error) {
	seenID := map[string]bool{}
	seenIdx := map[int]bool{}
	seenPort := map[int]bool{}
	for i := range s.Tunnels {
		t := &s.Tunnels[i]
		t.ID = strings.TrimSpace(t.ID)
		if !idPattern.MatchString(t.ID) {
			return Spec{}, fmt.Errorf("tunnel id %q is invalid", t.ID)
		}
		if seenID[t.ID] {
			return Spec{}, fmt.Errorf("tunnel %s is listed twice", t.ID)
		}
		seenID[t.ID] = true
		if t.Role != RoleEdge && t.Role != RoleOrigin {
			return Spec{}, fmt.Errorf("tunnel %s: role must be edge or origin", t.ID)
		}
		if t.Index < 1 || t.Index > MaxIndex {
			return Spec{}, fmt.Errorf("tunnel %s: index must be 1 to %d", t.ID, MaxIndex)
		}
		if seenIdx[t.Index] {
			return Spec{}, fmt.Errorf("tunnel %s: index %d is used twice", t.ID, t.Index)
		}
		seenIdx[t.Index] = true
		pfx, err := netip.ParsePrefix(strings.TrimSpace(t.Address))
		if err != nil || !pfx.Addr().Is4() || pfx.Bits() < 16 || pfx.Bits() > 31 {
			return Spec{}, fmt.Errorf("tunnel %s: address must be IPv4 with a /16 to /31 prefix", t.ID)
		}
		t.Address = pfx.String()
		peer, err := netip.ParseAddr(strings.TrimSpace(t.Peer))
		if err != nil || !peer.Is4() || !pfx.Masked().Contains(peer) || peer == pfx.Addr() {
			return Spec{}, fmt.Errorf("tunnel %s: peer must be another address in %s", t.ID, pfx.Masked())
		}
		t.Peer = peer.String()
		t.PeerKey = strings.TrimSpace(t.PeerKey)
		if t.PeerKey != "" && !validKey(t.PeerKey) {
			return Spec{}, fmt.Errorf("tunnel %s: peer key is invalid", t.ID)
		}
		switch t.Role {
		case RoleEdge:
			if t.ListenPort < 1 || t.ListenPort > 65535 {
				return Spec{}, fmt.Errorf("tunnel %s: listen port is invalid", t.ID)
			}
			if seenPort[t.ListenPort] {
				return Spec{}, fmt.Errorf("tunnel %s: listen port %d is used twice", t.ID, t.ListenPort)
			}
			seenPort[t.ListenPort] = true
			t.Endpoint = ""
			t.Egress = nil
		case RoleOrigin:
			t.ListenPort = 0
			t.Endpoint = strings.TrimSpace(t.Endpoint)
			if t.Endpoint != "" {
				if _, err := netip.ParseAddrPort(t.Endpoint); err != nil {
					return Spec{}, fmt.Errorf("tunnel %s: endpoint must be address:port", t.ID)
				}
			}
			eg, err := cleanEgress(t.Egress)
			if err != nil {
				return Spec{}, fmt.Errorf("tunnel %s: %w", t.ID, err)
			}
			t.Egress = eg
		}
		fwds, err := normalizeForwards(*t)
		if err != nil {
			return Spec{}, fmt.Errorf("tunnel %s: %w", t.ID, err)
		}
		t.Forwards = fwds
	}
	// Edge forwards must not take a tunnel's own port.
	for _, t := range s.Tunnels {
		if t.Role != RoleEdge {
			continue
		}
		for _, f := range t.Forwards {
			for p := range seenPort {
				if f.Protocol == "udp" && p >= f.Port && p <= f.last() {
					return Spec{}, fmt.Errorf("forward %s takes tunnel port %d", f.ID, p)
				}
			}
		}
	}
	if err := checkOriginPorts(s.Tunnels); err != nil {
		return Spec{}, err
	}
	if err := checkOverlap(s.Tunnels); err != nil {
		return Spec{}, err
	}
	sort.Slice(s.Tunnels, func(i, j int) bool { return s.Tunnels[i].Index < s.Tunnels[j].Index })
	return s, nil
}

// reserved are ports an edge forward may never take: SSH and the agent.
var reserved = map[int]string{22: "SSH", 9444: "the No-dal agent"}

func normalizeForwards(t Tunnel) ([]Forward, error) {
	out := make([]Forward, 0, len(t.Forwards))
	seen := map[string]bool{}
	for _, f := range t.Forwards {
		f.ID = strings.TrimSpace(f.ID)
		if !idPattern.MatchString(f.ID) {
			return nil, fmt.Errorf("forward id %q is invalid", f.ID)
		}
		if seen[f.ID] {
			return nil, fmt.Errorf("forward %s is listed twice", f.ID)
		}
		seen[f.ID] = true
		f.Protocol = strings.ToLower(strings.TrimSpace(f.Protocol))
		if f.Protocol != "udp" && f.Protocol != "tcp" {
			return nil, fmt.Errorf("forward %s: protocol must be udp or tcp", f.ID)
		}
		if f.Port < 1 || f.Port > 65535 {
			return nil, fmt.Errorf("forward %s: port is invalid", f.ID)
		}
		if f.PortEnd == f.Port {
			f.PortEnd = 0
		}
		if f.PortEnd != 0 && (f.PortEnd < f.Port || f.PortEnd > 65535) {
			return nil, fmt.Errorf("forward %s: port range is invalid", f.ID)
		}
		if f.ToPort == 0 {
			f.ToPort = f.Port
		}
		if f.ToPort < 1 || f.ToPort+(f.last()-f.Port) > 65535 {
			return nil, fmt.Errorf("forward %s: origin port is invalid", f.ID)
		}
		if f.PortEnd != 0 && f.ToPort != f.Port {
			return nil, fmt.Errorf("forward %s: a port range keeps its ports on the origin", f.ID)
		}
		if f.Protocol == "tcp" && t.Role == RoleEdge {
			for p, what := range reserved {
				if p >= f.Port && p <= f.last() {
					return nil, fmt.Errorf("forward %s: TCP %d is kept for %s", f.ID, p, what)
				}
			}
		}
		f.Target = strings.TrimSpace(f.Target)
		if t.Role == RoleEdge {
			f.Target = ""
			f.Masquerade = false
		}
		if f.Target != "" {
			a, err := netip.ParseAddr(f.Target)
			if err != nil || !a.Is4() {
				return nil, fmt.Errorf("forward %s: target must be an IPv4 address", f.ID)
			}
			f.Target = a.String()
		} else {
			f.Masquerade = false
		}
		out = append(out, f)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Protocol != out[j].Protocol {
			return out[i].Protocol < out[j].Protocol
		}
		return out[i].Port < out[j].Port
	})
	return out, nil
}

func (f Forward) last() int {
	if f.PortEnd > 0 {
		return f.PortEnd
	}
	return f.Port
}

// checkOverlap refuses two forwards on one node that take the same public
// port: only the first would ever match.
func checkOverlap(tunnels []Tunnel) error {
	type span struct {
		id       string
		lo, hi   int
		protocol string
	}
	var spans []span
	for _, t := range tunnels {
		if t.Role != RoleEdge {
			continue
		}
		for _, f := range t.Forwards {
			spans = append(spans, span{f.ID, f.Port, f.last(), f.Protocol})
		}
	}
	for i := range spans {
		for j := i + 1; j < len(spans); j++ {
			a, b := spans[i], spans[j]
			if a.protocol == b.protocol && a.lo <= b.hi && b.lo <= a.hi {
				return fmt.Errorf("forwards %s and %s both take %s %d", a.id, b.id, strings.ToUpper(a.protocol), max(a.lo, b.lo))
			}
		}
	}
	return nil
}

// checkOriginPorts refuses one origin port served through two tunnels:
// replies are routed by their source port and could only follow one.
func checkOriginPorts(tunnels []Tunnel) error {
	owner := map[string]string{}
	for _, t := range tunnels {
		if t.Role != RoleOrigin {
			continue
		}
		for _, f := range t.Forwards {
			if f.Target != "" || f.Protocol != "udp" {
				continue
			}
			for p := f.ToPort; p <= f.ToPort+f.last()-f.Port; p++ {
				k := strconv.Itoa(p)
				if prev, ok := owner[k]; ok && prev != t.ID {
					return fmt.Errorf("UDP %d is forwarded through two tunnels (%s and %s)", p, prev, t.ID)
				}
				owner[k] = t.ID
			}
		}
	}
	return nil
}

var containerPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$`)

func cleanEgress(in []string) ([]string, error) {
	seen := map[string]bool{}
	var out []string
	for _, e := range in {
		e = strings.TrimSpace(e)
		if e == "" || seen[e] {
			continue
		}
		if a, err := netip.ParseAddr(e); err == nil {
			if !a.Is4() {
				return nil, fmt.Errorf("egress address %s must be IPv4", e)
			}
		} else if !containerPattern.MatchString(e) {
			return nil, fmt.Errorf("egress %q is not an address or a container name", e)
		}
		seen[e] = true
		out = append(out, e)
	}
	sort.Strings(out)
	return out, nil
}

func validKey(k string) bool {
	if len(k) != 44 || !strings.HasSuffix(k, "=") {
		return false
	}
	for _, c := range k[:43] {
		if !(c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '+' || c == '/') {
			return false
		}
	}
	return true
}
