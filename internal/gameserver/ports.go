package gameserver

import (
	"errors"
	"fmt"
	"net"
	"sort"
	"strconv"
	"syscall"
)

// PortKey identifies a published host port.
func PortKey(port int, proto string) string {
	if proto == "" {
		proto = "tcp"
	}
	return strconv.Itoa(port) + "/" + proto
}

// HostPortOf is the host side of a published port.
func HostPortOf(p Port) int {
	if p.HostPort > 0 {
		return p.HostPort
	}
	return p.ContainerPort
}

// PortProbe reports whether a host port is free to bind on this node.
type PortProbe func(port int, proto string) bool

// LocalPortFree probes the local host. Only "address in use" counts as busy;
// a permission error (privileged ports for an unprivileged control plane)
// is treated as free because Docker publishes the port, not this process.
func LocalPortFree(port int, proto string) bool {
	var err error
	if proto == "udp" {
		var c net.PacketConn
		c, err = net.ListenPacket("udp", ":"+strconv.Itoa(port))
		if err == nil {
			_ = c.Close()
		}
	} else {
		var l net.Listener
		l, err = net.Listen("tcp", ":"+strconv.Itoa(port))
		if err == nil {
			_ = l.Close()
		}
	}
	if err == nil {
		return true
	}
	return !errors.Is(err, syscall.EADDRINUSE)
}

// PortPlan is the result of allocating a template's ports on a node.
type PortPlan struct {
	Ports []Port            `json:"ports"`
	Env   map[string]string `json:"env"`
	Delta int               `json:"delta"`
	Notes []string          `json:"notes,omitempty"`
}

// AllocatePorts finds host ports for a template. The whole port set moves
// by one delta so implicit relationships (query = game + 1) survive.
// Ports bound through a variable keep container == host and the variable is
// updated; Fixed ports keep their container port and are NAT-mapped;
// derived ports move with the set. used holds host ports already reserved
// by other game servers on the node. hostNetwork disables NAT, so a Fixed
// port can then never move.
func AllocatePorts(t Template, used map[string]string, probe PortProbe, hostNetwork bool) (PortPlan, error) {
	if len(t.DefaultPorts) == 0 {
		return PortPlan{Env: map[string]string{}}, nil
	}
	maxDelta := 2000
	for delta := 0; delta <= maxDelta; delta++ {
		plan, ok := tryDelta(t, delta, used, probe, hostNetwork)
		if ok {
			return plan, nil
		}
		if hostNetwork && hasFixed(t.DefaultPorts) && delta == 0 {
			var busy []string
			for _, p := range t.DefaultPorts {
				if owner, taken := used[PortKey(p.ContainerPort, p.Protocol)]; taken {
					busy = append(busy, fmt.Sprintf("%s (used by %s)", PortKey(p.ContainerPort, p.Protocol), owner))
				}
			}
			return PortPlan{}, fmt.Errorf("host networking is on and this game binds fixed ports that are already taken: %v", busy)
		}
	}
	return PortPlan{}, fmt.Errorf("no free port range found for %s within %d of its default ports", t.Name, maxDelta)
}

func hasFixed(ports []Port) bool {
	for _, p := range ports {
		if p.Fixed {
			return true
		}
	}
	return false
}

func tryDelta(t Template, delta int, used map[string]string, probe PortProbe, hostNetwork bool) (PortPlan, bool) {
	plan := PortPlan{Env: map[string]string{}, Delta: delta}
	seen := map[string]bool{}
	for _, p := range t.DefaultPorts {
		out := p
		host := p.ContainerPort + delta
		if host > 65535 {
			return plan, false
		}
		if p.Fixed {
			if hostNetwork && delta != 0 {
				return plan, false
			}
			out.HostPort = host
		} else {
			out.ContainerPort = host
			out.HostPort = host
			if p.Env != "" {
				plan.Env[p.Env] = strconv.Itoa(host)
			}
		}
		key := PortKey(host, p.Protocol)
		if seen[key] {
			return plan, false
		}
		seen[key] = true
		if _, taken := used[key]; taken {
			return plan, false
		}
		if probe != nil && !probe(host, p.Protocol) {
			return plan, false
		}
		plan.Ports = append(plan.Ports, out)
	}
	if delta != 0 {
		plan.Notes = append(plan.Notes, fmt.Sprintf("Default ports are in use on this node, so every port moved by +%d.", delta))
		for _, p := range plan.Ports {
			if p.Fixed {
				plan.Notes = append(plan.Notes, fmt.Sprintf("%s port %d is fixed by the game and is published on host port %d.", p.Name, p.ContainerPort, p.HostPort))
			}
		}
	}
	return plan, true
}

// CheckExplicitPorts validates operator-chosen ports against reservations.
func CheckExplicitPorts(ports []Port, used map[string]string, probe PortProbe) error {
	seen := map[string]bool{}
	for _, p := range ports {
		host := HostPortOf(p)
		if host < 1 || host > 65535 || p.ContainerPort < 1 || p.ContainerPort > 65535 {
			return fmt.Errorf("port %d is out of range", host)
		}
		proto := p.Protocol
		if proto == "" {
			proto = "tcp"
		}
		if proto != "tcp" && proto != "udp" {
			return fmt.Errorf("port %d protocol %q must be tcp or udp", host, p.Protocol)
		}
		key := PortKey(host, proto)
		if seen[key] {
			return fmt.Errorf("port %s is listed twice", key)
		}
		seen[key] = true
		if owner, taken := used[key]; taken {
			return fmt.Errorf("port %s is already used by %s", key, owner)
		}
		if probe != nil && !probe(host, proto) {
			return fmt.Errorf("port %s is already in use on this node", key)
		}
	}
	return nil
}

// SortedPortKeys is a stable listing for messages and tests.
func SortedPortKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
