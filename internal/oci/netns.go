package oci

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// Network modes. An empty mode is a container created before networking was
// configurable; it keeps running exactly as before, with no network.
const (
	NetworkNone   = "none"
	NetworkBridge = "bridge"
	NetworkHost   = "host"
)

// Compile-time host binaries used to wire a container's network.
const (
	BinIP       = "/usr/sbin/ip"
	BinNFT      = "/usr/sbin/nft"
	BinDHClient = "/usr/sbin/dhclient"
	BinUDHCPC   = "/usr/sbin/udhcpc"
)

const udhcpcScript = "/etc/udhcpc/default.script"

// EffectiveNetworkMode reports how a spec's container is networked.
func EffectiveNetworkMode(spec Spec) string {
	switch spec.NetworkMode {
	case NetworkBridge, NetworkHost:
		return spec.NetworkMode
	default:
		return NetworkNone
	}
}

func idHex(id string) string {
	return strings.ReplaceAll(strings.ToLower(id), "-", "")
}

// NetnsName is the named network namespace a bridged container joins.
func NetnsName(id string) string {
	return "ndloci-" + idHex(id)[:12]
}

// HostVeth is the bridge side of the container's veth pair (15 chars max).
func HostVeth(id string) string {
	return "vo" + idHex(id)[:12]
}

func peerVeth(id string) string {
	return "vp" + idHex(id)[:12]
}

func nftTable(id string) string {
	return "ndl_oci_" + idHex(id)[:12]
}

// ContainerMAC is a stable locally administered MAC, so DHCP reservations
// keep working across restarts.
func ContainerMAC(id string) string {
	sum := sha256.Sum256([]byte("ndl-oci-mac:" + strings.ToLower(id)))
	h := hex.EncodeToString(sum[:5])
	return "02:" + h[0:2] + ":" + h[2:4] + ":" + h[4:6] + ":" + h[6:8] + ":" + h[8:10]
}

// ValidateNetwork checks the network part of a spec.
func ValidateNetwork(spec Spec) error {
	switch spec.NetworkMode {
	case "", NetworkNone, NetworkBridge, NetworkHost:
	default:
		return fmt.Errorf("network_mode must be none, bridge or host")
	}
	mode := EffectiveNetworkMode(spec)
	if mode == NetworkBridge && strings.TrimSpace(spec.BridgeName) == "" {
		return fmt.Errorf("bridge network mode needs a network")
	}
	if strings.ContainsAny(spec.BridgeName, " \n\r\x00,=/") {
		return fmt.Errorf("bridge name contains banned characters")
	}
	if spec.IPv4Address != "" {
		if mode != NetworkBridge {
			return fmt.Errorf("a fixed IP address needs bridge network mode")
		}
		pfx, err := netip.ParsePrefix(strings.TrimSpace(spec.IPv4Address))
		if err != nil || !pfx.Addr().Is4() {
			return fmt.Errorf("ipv4_address must be an IPv4 address with a prefix, for example 192.168.1.50/24")
		}
	}
	if spec.IPv4Gateway != "" {
		gw, err := netip.ParseAddr(strings.TrimSpace(spec.IPv4Gateway))
		if err != nil || !gw.Is4() {
			return fmt.Errorf("ipv4_gateway must be an IPv4 address")
		}
		if spec.IPv4Address == "" {
			return fmt.Errorf("ipv4_gateway needs ipv4_address; DHCP supplies its own gateway")
		}
	}
	for _, d := range spec.DNS {
		if _, err := netip.ParseAddr(strings.TrimSpace(d)); err != nil {
			return fmt.Errorf("dns server %q is not an IP address", d)
		}
	}
	seen := map[string]bool{}
	for _, p := range spec.Ports {
		proto := strings.ToLower(strings.TrimSpace(p.Protocol))
		if proto == "" {
			proto = "tcp"
		}
		host := p.HostPort
		if host == 0 {
			host = p.ContainerPort
		}
		if mode == NetworkHost && p.HostPort != 0 && p.HostPort != p.ContainerPort {
			return fmt.Errorf("on the host network the app listens on the host directly; port %d cannot be published as %d", p.ContainerPort, p.HostPort)
		}
		key := proto + "/" + strconv.Itoa(host)
		if seen[key] {
			return fmt.Errorf("host port %d/%s is published twice", host, proto)
		}
		seen[key] = true
	}
	return nil
}

// NetnsSetupArgv builds the commands that create the namespace, join it to
// the bridge and, for a fixed address, configure it.
func NetnsSetupArgv(spec Spec) ([][]string, error) {
	if EffectiveNetworkMode(spec) != NetworkBridge {
		return nil, nil
	}
	if err := ValidateNetwork(spec); err != nil {
		return nil, err
	}
	ns, host, peer := NetnsName(spec.WorkloadID), HostVeth(spec.WorkloadID), peerVeth(spec.WorkloadID)
	cmds := [][]string{
		{BinIP, "netns", "add", ns},
		{BinIP, "link", "add", host, "type", "veth", "peer", "name", peer},
		{BinIP, "link", "set", host, "master", spec.BridgeName},
		{BinIP, "link", "set", host, "up"},
		{BinIP, "link", "set", peer, "netns", ns},
		{BinIP, "-n", ns, "link", "set", peer, "name", "eth0"},
		{BinIP, "-n", ns, "link", "set", "eth0", "address", ContainerMAC(spec.WorkloadID)},
		{BinIP, "-n", ns, "link", "set", "lo", "up"},
		{BinIP, "-n", ns, "link", "set", "eth0", "up"},
	}
	if spec.IPv4Address != "" {
		cmds = append(cmds, []string{BinIP, "-n", ns, "addr", "add", strings.TrimSpace(spec.IPv4Address), "dev", "eth0"})
		if spec.IPv4Gateway != "" {
			cmds = append(cmds, []string{BinIP, "-n", ns, "route", "add", "default", "via", strings.TrimSpace(spec.IPv4Gateway)})
		}
	}
	return cmds, nil
}

// PortRules is the nftables script publishing ports on the host to a bridged
// container at ip. Each container owns one table, so teardown is one delete.
func PortRules(id, ip string, ports []Port) string {
	if len(ports) == 0 || ip == "" {
		return ""
	}
	t := nftTable(id)
	var dnat, masq strings.Builder
	for _, p := range ports {
		proto := strings.ToLower(strings.TrimSpace(p.Protocol))
		if proto == "" {
			proto = "tcp"
		}
		host := p.HostPort
		if host == 0 {
			host = p.ContainerPort
		}
		fmt.Fprintf(&dnat, "    fib daddr type local %s dport %d dnat to %s:%d\n", proto, host, ip, p.ContainerPort)
		fmt.Fprintf(&masq, "    ip daddr %s %s dport %d ct status dnat masquerade\n", ip, proto, p.ContainerPort)
	}
	return "table ip " + t + " {\n" +
		"  chain pre {\n    type nat hook prerouting priority dstnat; policy accept;\n" + dnat.String() + "  }\n" +
		"  chain out {\n    type nat hook output priority -100; policy accept;\n" + dnat.String() + "  }\n" +
		"  chain post {\n    type nat hook postrouting priority srcnat; policy accept;\n" + masq.String() + "  }\n" +
		"}\n"
}

func (e *Engine) etcDir() string {
	if e.EtcDir != "" {
		return e.EtcDir
	}
	return "/etc"
}

func (e *Engine) runDir(id string) string {
	base := e.RuntimeDir
	if base == "" {
		base = "/run/ndl-oci"
	}
	return filepath.Join(base, idHex(id))
}

func (e *Engine) netCmd(ctx context.Context, argv []string) ([]byte, error) {
	return e.runHost(ctx, argv[0], argv[1:]...)
}

func exists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// hostResolvers lists the host's upstream DNS servers, skipping loopback
// stubs a separate network namespace cannot reach.
func hostResolvers() []string {
	var out []string
	for _, f := range []string{"/run/systemd/resolve/resolv.conf", "/etc/resolv.conf"} {
		raw, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(raw), "\n") {
			fields := strings.Fields(line)
			if len(fields) < 2 || fields[0] != "nameserver" {
				continue
			}
			addr, err := netip.ParseAddr(fields[1])
			if err != nil || addr.IsLoopback() {
				continue
			}
			out = append(out, addr.String())
		}
		if len(out) > 0 {
			return out
		}
	}
	return out
}

func resolvConf(servers []string) string {
	var b strings.Builder
	for _, s := range servers {
		b.WriteString("nameserver " + strings.TrimSpace(s) + "\n")
	}
	return b.String()
}

// PrepareNetwork wires the container's network before it starts and fills
// in what the run needs (namespace path and resolv.conf). The returned spec
// is only for this run; last-applied is not changed.
func (e *Engine) PrepareNetwork(ctx context.Context, spec Spec) (Spec, error) {
	switch EffectiveNetworkMode(spec) {
	case NetworkHost:
		if exists("/etc/resolv.conf") {
			spec.ResolvPath = "/etc/resolv.conf"
		}
		return spec, nil
	case NetworkBridge:
	default:
		return spec, nil
	}
	// A unit killed hard leaves its namespace behind; start clean.
	e.TeardownNetwork(ctx, spec.WorkloadID)
	cmds, err := NetnsSetupArgv(spec)
	if err != nil {
		return spec, err
	}
	ns := NetnsName(spec.WorkloadID)
	nsEtc := filepath.Join(e.etcDir(), "netns", ns)
	if err := os.MkdirAll(nsEtc, 0o755); err != nil {
		return spec, err
	}
	resolv := filepath.Join(nsEtc, "resolv.conf")
	servers := spec.DNS
	if len(servers) == 0 && spec.IPv4Address != "" {
		servers = hostResolvers()
		if len(servers) == 0 && spec.IPv4Gateway != "" {
			servers = []string{spec.IPv4Gateway}
		}
	}
	if err := os.WriteFile(resolv, []byte(resolvConf(servers)), 0o644); err != nil {
		return spec, err
	}
	for _, argv := range cmds {
		if _, err := e.netCmd(ctx, argv); err != nil {
			e.TeardownNetwork(ctx, spec.WorkloadID)
			return spec, fmt.Errorf("could not connect the container to %s: %w", spec.BridgeName, err)
		}
	}
	if spec.IPv4Address == "" {
		if err := e.startDHCP(ctx, spec); err != nil {
			e.TeardownNetwork(ctx, spec.WorkloadID)
			return spec, err
		}
		if len(spec.DNS) > 0 {
			// Fixed DNS servers win over the ones DHCP wrote.
			_ = os.WriteFile(resolv, []byte(resolvConf(spec.DNS)), 0o644)
		}
	}
	if len(spec.Ports) > 0 {
		ip := ""
		if spec.IPv4Address != "" {
			if pfx, err := netip.ParsePrefix(spec.IPv4Address); err == nil {
				ip = pfx.Addr().String()
			}
		} else {
			ip = e.leasedIP(ctx, ns)
		}
		if ip == "" {
			e.TeardownNetwork(ctx, spec.WorkloadID)
			return spec, fmt.Errorf("the container has no IPv4 address to publish ports to")
		}
		if err := e.publishPorts(ctx, spec.WorkloadID, ip, spec.Ports); err != nil {
			e.TeardownNetwork(ctx, spec.WorkloadID)
			return spec, err
		}
	}
	spec.NetnsPath = filepath.Join("/var/run/netns", ns)
	spec.ResolvPath = resolv
	return spec, nil
}

func (e *Engine) startDHCP(ctx context.Context, spec Spec) error {
	ns := NetnsName(spec.WorkloadID)
	dir := e.runDir(spec.WorkloadID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	pid := filepath.Join(dir, "dhcp.pid")
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	switch {
	case e.Run != nil || exists(BinDHClient):
		// -1 tries once and fails if no lease; on success it stays to renew.
		_, err := e.netCmd(ctx, []string{BinIP, "netns", "exec", ns, BinDHClient, "-4", "-1", "-pf", pid, "-lf", filepath.Join(dir, "dhclient.leases"), "eth0"})
		if err != nil {
			return fmt.Errorf("no DHCP lease on %s; check the network has a DHCP server or give the container a fixed IP: %w", spec.BridgeName, err)
		}
		return nil
	case exists(BinUDHCPC) && exists(udhcpcScript):
		_, err := e.netCmd(ctx, []string{BinIP, "netns", "exec", ns, BinUDHCPC, "-i", "eth0", "-n", "-b", "-t", "8", "-p", pid, "-s", udhcpcScript})
		if err != nil {
			return fmt.Errorf("no DHCP lease on %s; check the network has a DHCP server or give the container a fixed IP: %w", spec.BridgeName, err)
		}
		return nil
	default:
		return fmt.Errorf("the host has no DHCP client for containers; install isc-dhcp-client, or give the container a fixed IP")
	}
}

// leasedIP reads the address DHCP gave eth0 inside the namespace.
func (e *Engine) leasedIP(ctx context.Context, ns string) string {
	for i := 0; i < 20; i++ {
		out, err := e.netCmd(ctx, []string{BinIP, "-n", ns, "-4", "-o", "addr", "show", "dev", "eth0"})
		if err == nil {
			if ip := parseInetAddr(string(out)); ip != "" {
				return ip
			}
		}
		if e.Run != nil {
			return ""
		}
		select {
		case <-ctx.Done():
			return ""
		case <-time.After(500 * time.Millisecond):
		}
	}
	return ""
}

func parseInetAddr(out string) string {
	fields := strings.Fields(out)
	for i := 0; i+1 < len(fields); i++ {
		if fields[i] == "inet" {
			if pfx, err := netip.ParsePrefix(fields[i+1]); err == nil {
				return pfx.Addr().String()
			}
		}
	}
	return ""
}

func (e *Engine) publishPorts(ctx context.Context, id, ip string, ports []Port) error {
	script := PortRules(id, ip, ports)
	if script == "" {
		return nil
	}
	dir := e.runDir(id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	file := filepath.Join(dir, "ports.nft")
	if err := os.WriteFile(file, []byte(script), 0o600); err != nil {
		return err
	}
	if e.Run == nil {
		// Published ports are routed from the host's address to the container.
		_ = os.WriteFile("/proc/sys/net/ipv4/ip_forward", []byte("1\n"), 0o644)
	}
	if _, err := e.netCmd(ctx, []string{BinNFT, "-f", file}); err != nil {
		return fmt.Errorf("could not publish the container's ports: %w", err)
	}
	return nil
}

// TeardownNetwork removes everything PrepareNetwork made. It is safe to run
// when nothing exists.
func (e *Engine) TeardownNetwork(ctx context.Context, id string) {
	dir := e.runDir(id)
	if raw, err := os.ReadFile(filepath.Join(dir, "dhcp.pid")); err == nil {
		if pid, err := strconv.Atoi(strings.TrimSpace(string(raw))); err == nil && pid > 1 && e.Run == nil {
			_ = syscall.Kill(pid, syscall.SIGTERM)
		}
	}
	_, _ = e.netCmd(ctx, []string{BinNFT, "delete", "table", "ip", nftTable(id)})
	_, _ = e.netCmd(ctx, []string{BinIP, "netns", "del", NetnsName(id)})
	_, _ = e.netCmd(ctx, []string{BinIP, "link", "del", HostVeth(id)})
	_ = os.RemoveAll(filepath.Join(e.etcDir(), "netns", NetnsName(id)))
	_ = os.RemoveAll(dir)
}
