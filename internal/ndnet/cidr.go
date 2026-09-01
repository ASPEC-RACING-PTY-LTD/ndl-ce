package ndnet

import (
	"fmt"
	"net"
	"strings"
)

func parseIPv4Net(cidr string) (net.IP, *net.IPNet, error) {
	ip, n, err := net.ParseCIDR(strings.TrimSpace(cidr))
	if err != nil {
		return nil, nil, fmt.Errorf("invalid ipv4_cidr")
	}
	if ip.To4() == nil || n.IP.To4() == nil {
		return nil, nil, fmt.Errorf("ipv4_cidr must be IPv4")
	}
	ones, bits := n.Mask.Size()
	if bits != 32 || ones < 16 || ones > 30 {
		return nil, nil, fmt.Errorf("ipv4_cidr prefix must be between /16 and /30")
	}
	if n.IP.Equal(net.IPv4zero) || n.Contains(net.IPv4zero) && ones == 0 {
		return nil, nil, fmt.Errorf("ipv4_cidr must not be 0.0.0.0/0")
	}
	return ip.To4(), n, nil
}

func gatewayOf(n *net.IPNet) net.IP {
	ip := cloneIP(n.IP.To4())
	ip[3]++
	return ip
}

func dhcpRange(n *net.IPNet) (start, end net.IP, err error) {
	ones, _ := n.Mask.Size()
	size := 1 << (32 - ones)
	if size < 8 {
		return nil, nil, fmt.Errorf("ipv4_cidr is too small for DHCP")
	}
	base := ip4int(n.IP.To4())
	// Skip network, gateway, and a few reserved low addresses.
	s := base + 50
	e := base + uint32(size) - 6
	if s >= e {
		s = base + 2
		e = base + uint32(size) - 2
	}
	if e <= s {
		return nil, nil, fmt.Errorf("ipv4_cidr is too small for DHCP")
	}
	return intIP4(s), intIP4(e), nil
}

func cidrString(ip net.IP, n *net.IPNet) string {
	ones, _ := n.Mask.Size()
	return fmt.Sprintf("%s/%d", ip.String(), ones)
}

func cloneIP(ip net.IP) net.IP {
	out := make(net.IP, len(ip))
	copy(out, ip)
	return out
}

func ip4int(ip net.IP) uint32 {
	v := ip.To4()
	return uint32(v[0])<<24 | uint32(v[1])<<16 | uint32(v[2])<<8 | uint32(v[3])
}

func intIP4(v uint32) net.IP {
	return net.IPv4(byte(v>>24), byte(v>>16), byte(v>>8), byte(v)).To4()
}

func overlaps(n *net.IPNet, addrs []string) bool {
	for _, addr := range addrs {
		ip := net.ParseIP(stripCIDR(addr))
		if ip != nil && n.Contains(ip) {
			return true
		}
	}
	return false
}
