package lxc

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
)

const (
	IPModeDHCP     = "dhcp"
	IPModeStatic   = "static"
	IPModeDisabled = "disabled"
)

// IPConfig is independent IPv4 and IPv6 policy for one container NIC.
type IPConfig struct {
	IPv4Mode    string   `json:"ipv4_mode,omitempty"`
	IPv4Address string   `json:"ipv4_address,omitempty"`
	IPv4Gateway string   `json:"ipv4_gateway,omitempty"`
	IPv6Mode    string   `json:"ipv6_mode,omitempty"`
	IPv6Address string   `json:"ipv6_address,omitempty"`
	IPv6Gateway string   `json:"ipv6_gateway,omitempty"`
	DNS         []string `json:"dns,omitempty"`
}

// NormalizeIPConfig fills defaults: IPv4 DHCP, IPv6 disabled.
func NormalizeIPConfig(in IPConfig) (IPConfig, error) {
	out := IPConfig{
		IPv4Mode:    strings.ToLower(strings.TrimSpace(in.IPv4Mode)),
		IPv4Address: strings.TrimSpace(in.IPv4Address),
		IPv4Gateway: strings.TrimSpace(in.IPv4Gateway),
		IPv6Mode:    strings.ToLower(strings.TrimSpace(in.IPv6Mode)),
		IPv6Address: strings.TrimSpace(in.IPv6Address),
		IPv6Gateway: strings.TrimSpace(in.IPv6Gateway),
	}
	if out.IPv4Mode == "" {
		out.IPv4Mode = IPModeDHCP
	}
	if out.IPv6Mode == "" {
		out.IPv6Mode = IPModeDisabled
	}
	if err := validateIPMode("IPv4", out.IPv4Mode); err != nil {
		return IPConfig{}, err
	}
	if err := validateIPMode("IPv6", out.IPv6Mode); err != nil {
		return IPConfig{}, err
	}
	if out.IPv4Mode != IPModeStatic {
		out.IPv4Address = ""
		out.IPv4Gateway = ""
	}
	if out.IPv6Mode != IPModeStatic {
		out.IPv6Address = ""
		out.IPv6Gateway = ""
	}
	if out.IPv4Mode == IPModeStatic {
		if err := validateStatic("IPv4", out.IPv4Address, out.IPv4Gateway, false); err != nil {
			return IPConfig{}, err
		}
	}
	if out.IPv6Mode == IPModeStatic {
		if err := validateStatic("IPv6", out.IPv6Address, out.IPv6Gateway, true); err != nil {
			return IPConfig{}, err
		}
	}
	for _, d := range in.DNS {
		d = strings.TrimSpace(d)
		if d == "" {
			continue
		}
		if net.ParseIP(d) == nil {
			return IPConfig{}, fmt.Errorf("DNS %q is not an IP address", d)
		}
		out.DNS = append(out.DNS, d)
	}
	return out, nil
}

// RequireStaticGateways rejects static families that omit a gateway.
func RequireStaticGateways(in IPConfig) error {
	if in.IPv4Mode == IPModeStatic && strings.TrimSpace(in.IPv4Gateway) == "" {
		return fmt.Errorf("IPv4 gateway is required for static mode")
	}
	if in.IPv6Mode == IPModeStatic && strings.TrimSpace(in.IPv6Gateway) == "" {
		return fmt.Errorf("IPv6 gateway is required for static mode")
	}
	return nil
}

func validateIPMode(family, mode string) error {
	switch mode {
	case IPModeDHCP, IPModeStatic, IPModeDisabled:
		return nil
	default:
		return fmt.Errorf("%s mode must be dhcp, static, or disabled", family)
	}
}

func validateStatic(family, addr, gateway string, ipv6 bool) error {
	if addr == "" {
		return fmt.Errorf("%s address/CIDR is required for static mode", family)
	}
	ip, _, err := net.ParseCIDR(addr)
	if err != nil {
		return fmt.Errorf("%s address must be CIDR notation", family)
	}
	if ipv6 {
		if ip.To4() != nil || ip.To16() == nil {
			return fmt.Errorf("IPv6 address is required")
		}
	} else if ip.To4() == nil {
		return fmt.Errorf("IPv4 address is required")
	}
	if gateway == "" {
		return nil
	}
	gw := net.ParseIP(gateway)
	if gw == nil {
		return fmt.Errorf("%s gateway is not a valid IP", family)
	}
	if ipv6 {
		if gw.To4() != nil || gw.To16() == nil {
			return fmt.Errorf("IPv6 gateway is required")
		}
	} else if gw.To4() == nil {
		return fmt.Errorf("IPv4 gateway is required")
	}
	return nil
}

// Equal compares normalized IP policy.
func (c IPConfig) Equal(other IPConfig) bool {
	a, errA := NormalizeIPConfig(c)
	b, errB := NormalizeIPConfig(other)
	if errA != nil || errB != nil {
		return c.IPv4Mode == other.IPv4Mode && c.IPv4Address == other.IPv4Address &&
			c.IPv4Gateway == other.IPv4Gateway && c.IPv6Mode == other.IPv6Mode &&
			c.IPv6Address == other.IPv6Address && c.IPv6Gateway == other.IPv6Gateway &&
			strings.Join(c.DNS, ",") == strings.Join(other.DNS, ",")
	}
	return a.IPv4Mode == b.IPv4Mode && a.IPv4Address == b.IPv4Address &&
		a.IPv4Gateway == b.IPv4Gateway && a.IPv6Mode == b.IPv6Mode &&
		a.IPv6Address == b.IPv6Address && a.IPv6Gateway == b.IPv6Gateway &&
		strings.Join(a.DNS, ",") == strings.Join(b.DNS, ",")
}

// FamilySummary is one family for Review and UI.
func FamilySummary(mode, addr, gateway string) string {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case IPModeStatic:
		if gateway != "" {
			return "Static " + addr + " via " + gateway
		}
		return "Static " + addr
	case IPModeDisabled:
		return "Disabled"
	default:
		return "DHCP"
	}
}

// Summary is IPv4, IPv6, and DNS for Review.
func (c IPConfig) Summary() string {
	n, err := NormalizeIPConfig(c)
	if err != nil {
		n = c
	}
	parts := []string{
		"IPv4 " + FamilySummary(n.IPv4Mode, n.IPv4Address, n.IPv4Gateway),
		"IPv6 " + FamilySummary(n.IPv6Mode, n.IPv6Address, n.IPv6Gateway),
	}
	if len(n.DNS) > 0 {
		parts = append(parts, "DNS "+strings.Join(n.DNS, ", "))
	}
	return strings.Join(parts, ", ")
}

func renderNetIP(cfg IPConfig) string {
	n, err := NormalizeIPConfig(cfg)
	if err != nil {
		return ""
	}
	var b strings.Builder
	switch n.IPv4Mode {
	case IPModeStatic:
		fmt.Fprintf(&b, "lxc.net.0.ipv4.address = %s\n", n.IPv4Address)
		if n.IPv4Gateway != "" {
			fmt.Fprintf(&b, "lxc.net.0.ipv4.gateway = %s\n", n.IPv4Gateway)
		}
	case IPModeDisabled:
		// Omit ipv4.address. LXC 6 on Debian 13 rejects the sentinel "none".
	}
	switch n.IPv6Mode {
	case IPModeStatic:
		fmt.Fprintf(&b, "lxc.net.0.ipv6.address = %s\n", n.IPv6Address)
		if n.IPv6Gateway != "" {
			fmt.Fprintf(&b, "lxc.net.0.ipv6.gateway = %s\n", n.IPv6Gateway)
		}
	case IPModeDisabled:
		// Guest networkd sets IPv6AcceptRA=no. Do not write
		// lxc.net.0.ipv6.address = none; LXC 6 treats that as an invalid address
		// and lxc-start refuses to load the container.
	}
	return b.String()
}

func ensureGuestNetwork(rootfs string, cfg IPConfig) error {
	n, err := NormalizeIPConfig(cfg)
	if err != nil {
		return err
	}
	ifaces := filepath.Join(rootfs, "etc", "network", "interfaces")
	if err := os.MkdirAll(filepath.Dir(ifaces), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(ifaces, []byte(guestIfupdown(n)), 0o644); err != nil {
		return err
	}
	netd := filepath.Join(rootfs, "etc", "systemd", "network")
	if st, err := os.Stat(netd); err == nil && st.IsDir() {
		if err := os.WriteFile(filepath.Join(netd, "10-eth0.network"), []byte(guestNetworkd(n)), 0o644); err != nil {
			return err
		}
	}
	if len(n.DNS) == 0 {
		return nil
	}
	resolv := filepath.Join(rootfs, "etc", "resolv.conf")
	if err := os.MkdirAll(filepath.Dir(resolv), 0o755); err != nil {
		return err
	}
	var b strings.Builder
	for _, d := range n.DNS {
		fmt.Fprintf(&b, "nameserver %s\n", d)
	}
	return os.WriteFile(resolv, []byte(b.String()), 0o644)
}

func guestIfupdown(n IPConfig) string {
	var b strings.Builder
	b.WriteString("auto lo\niface lo inet loopback\n")
	if n.IPv4Mode == IPModeDisabled && n.IPv6Mode == IPModeDisabled {
		b.WriteString("\nauto eth0\niface eth0 inet manual\n")
		return b.String()
	}
	b.WriteString("\nauto eth0\n")
	switch n.IPv4Mode {
	case IPModeDHCP:
		b.WriteString("iface eth0 inet dhcp\n")
		writeDNS(&b, n.DNS)
	case IPModeStatic:
		b.WriteString("iface eth0 inet static\n")
		fmt.Fprintf(&b, "    address %s\n", n.IPv4Address)
		if n.IPv4Gateway != "" {
			fmt.Fprintf(&b, "    gateway %s\n", n.IPv4Gateway)
		}
		writeDNS(&b, n.DNS)
	}
	switch n.IPv6Mode {
	case IPModeDHCP:
		b.WriteString("iface eth0 inet6 dhcp\n")
		if n.IPv4Mode == IPModeDisabled {
			writeDNS(&b, n.DNS)
		}
	case IPModeStatic:
		b.WriteString("iface eth0 inet6 static\n")
		fmt.Fprintf(&b, "    address %s\n", n.IPv6Address)
		if n.IPv6Gateway != "" {
			fmt.Fprintf(&b, "    gateway %s\n", n.IPv6Gateway)
		}
		if n.IPv4Mode == IPModeDisabled {
			writeDNS(&b, n.DNS)
		}
	}
	return b.String()
}

func writeDNS(b *strings.Builder, dns []string) {
	if len(dns) == 0 {
		return
	}
	fmt.Fprintf(b, "    dns-nameservers %s\n", strings.Join(dns, " "))
}

func guestNetworkd(n IPConfig) string {
	var b strings.Builder
	b.WriteString("[Match]\nName=eth0\n\n[Network]\n")
	switch {
	case n.IPv4Mode == IPModeDHCP && n.IPv6Mode == IPModeDHCP:
		b.WriteString("DHCP=yes\n")
	case n.IPv4Mode == IPModeDHCP:
		b.WriteString("DHCP=ipv4\n")
	case n.IPv6Mode == IPModeDHCP:
		b.WriteString("DHCP=ipv6\n")
	}
	if n.IPv4Mode == IPModeStatic {
		fmt.Fprintf(&b, "Address=%s\n", n.IPv4Address)
		if n.IPv4Gateway != "" {
			fmt.Fprintf(&b, "Gateway=%s\n", n.IPv4Gateway)
		}
	}
	if n.IPv6Mode == IPModeStatic {
		fmt.Fprintf(&b, "Address=%s\n", n.IPv6Address)
		if n.IPv6Gateway != "" {
			fmt.Fprintf(&b, "Gateway=%s\n", n.IPv6Gateway)
		}
	}
	if n.IPv6Mode == IPModeDisabled {
		b.WriteString("IPv6AcceptRA=no\n")
		if n.IPv4Mode == IPModeDisabled {
			b.WriteString("LinkLocalAddressing=no\n")
		} else {
			b.WriteString("LinkLocalAddressing=ipv4\n")
		}
	}
	if n.IPv4Mode == IPModeDisabled && n.IPv6Mode != IPModeDisabled {
		b.WriteString("LinkLocalAddressing=ipv6\n")
	}
	for _, d := range n.DNS {
		fmt.Fprintf(&b, "DNS=%s\n", d)
	}
	return b.String()
}

// SplitDNS parses a comma or space separated nameserver list.
func SplitDNS(s string) []string {
	s = strings.ReplaceAll(s, ",", " ")
	var out []string
	for _, p := range strings.Fields(s) {
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// JoinDNS stores nameservers as a single text column.
func JoinDNS(dns []string) string {
	return strings.Join(dns, ",")
}
