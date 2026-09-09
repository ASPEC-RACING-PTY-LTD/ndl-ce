package ndnet

import (
	"bufio"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// CollectHostView reads sysfs, live addresses, and default routes.
func CollectHostView(root string) (HostView, error) {
	if root == "" {
		root = "/"
	}
	view := HostView{}
	netDir := filepath.Join(root, "sys/class/net")
	entries, err := os.ReadDir(netDir)
	if err != nil {
		view = viewFromLive()
		return attachManagement(view), nil
	}
	for _, ent := range entries {
		name := ent.Name()
		iface := Iface{Name: name, Kind: "device"}
		if raw, err := os.ReadFile(filepath.Join(netDir, name, "ifindex")); err == nil {
			iface.IfIndex, _ = strconv.Atoi(strings.TrimSpace(string(raw)))
		}
		if raw, err := os.ReadFile(filepath.Join(netDir, name, "flags")); err == nil {
			hex := strings.TrimSpace(string(raw))
			hex = strings.TrimPrefix(strings.ToLower(hex), "0x")
			if flags, err := strconv.ParseUint(hex, 16, 64); err == nil {
				iface.Up = flags&1 != 0
			}
		} else if raw, err := os.ReadFile(filepath.Join(netDir, name, "operstate")); err == nil {
			iface.Up = strings.TrimSpace(string(raw)) == "up"
		}
		if raw, err := os.ReadFile(filepath.Join(netDir, name, "type")); err == nil {
			if strings.TrimSpace(string(raw)) == "772" {
				iface.Kind = "loopback"
			}
		}
		if _, err := os.Stat(filepath.Join(netDir, name, "bridge")); err == nil {
			iface.Kind = "bridge"
		}
		if raw, err := os.ReadFile(filepath.Join(netDir, name, "master/uevent")); err == nil {
			for _, line := range strings.Split(string(raw), "\n") {
				if strings.HasPrefix(line, "INTERFACE=") {
					iface.Master = strings.TrimPrefix(line, "INTERFACE=")
				}
			}
		}
		if raw, err := os.ReadFile(filepath.Join(netDir, name, "address")); err == nil {
			iface.HardwareAddr = normalizeMAC(string(raw))
		}
		view.Ifaces = append(view.Ifaces, iface)
	}
	attachLiveAddresses(&view)
	routeIf, gw := defaultIPv4Route(filepath.Join(root, "proc/net/route"))
	view.DefaultRouteIf = firstNonEmpty(routeIf, ipv6DefaultIf(filepath.Join(root, "proc/net/ipv6_route")))
	view.DefaultGateway = gw
	view.Nameservers = readNameservers(filepath.Join(root, "etc/resolv.conf"))
	return attachManagement(view), nil
}

func viewFromLive() HostView {
	view := HostView{}
	ifaces, err := net.Interfaces()
	if err != nil {
		return view
	}
	for _, iface := range ifaces {
		item := Iface{Name: iface.Name, IfIndex: iface.Index, Kind: "device", Up: iface.Flags&net.FlagUp != 0, HardwareAddr: normalizeMAC(iface.HardwareAddr.String())}
		if iface.Flags&net.FlagLoopback != 0 {
			item.Kind = "loopback"
		}
		view.Ifaces = append(view.Ifaces, item)
	}
	attachLiveAddresses(&view)
	return view
}

func attachLiveAddresses(view *HostView) {
	ifaces, err := net.Interfaces()
	if err != nil {
		return
	}
	byName := map[string][]string{}
	for _, iface := range ifaces {
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		var list []string
		for _, a := range addrs {
			if s := a.String(); s != "" {
				list = append(list, s)
			}
		}
		if len(list) > 0 {
			byName[iface.Name] = list
		}
	}
	for i := range view.Ifaces {
		if addrs, ok := byName[view.Ifaces[i].Name]; ok {
			view.Ifaces[i].Addresses = addrs
		}
	}
}

func attachManagement(view HostView) HostView {
	if view.DefaultRouteIf == "" {
		return view
	}
	if iface, ok := lookup(view, view.DefaultRouteIf); ok {
		view.ManagementIfName = iface.Name
		view.ManagementIfIndex = iface.IfIndex
		view.ManagementAddresses = append([]string{}, iface.Addresses...)
	}
	return view
}

func defaultIPv4Route(path string) (iface, gateway string) {
	f, err := os.Open(path)
	if err != nil {
		return "", ""
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	if sc.Scan() {
		// header
	}
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) < 3 {
			continue
		}
		if fields[1] == "00000000" {
			return fields[0], procHexIPv4(fields[2])
		}
	}
	return "", ""
}

func procHexIPv4(h string) string {
	h = strings.TrimSpace(h)
	if len(h) != 8 {
		return ""
	}
	var b [4]byte
	for i := 0; i < 4; i++ {
		v, err := strconv.ParseUint(h[i*2:i*2+2], 16, 8)
		if err != nil {
			return ""
		}
		b[3-i] = byte(v)
	}
	ip := net.IPv4(b[0], b[1], b[2], b[3]).To4()
	if ip == nil || ip.IsUnspecified() {
		return ""
	}
	return ip.String()
}

func readNameservers(path string) []string {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var out []string
	seen := map[string]struct{}{}
	for _, line := range strings.Split(string(b), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 || fields[0] != "nameserver" {
			continue
		}
		ip := net.ParseIP(fields[1])
		if ip == nil || ip.IsLoopback() || ip.IsUnspecified() {
			continue
		}
		s := ip.String()
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	return out
}

func normalizeMAC(raw string) string {
	mac, err := net.ParseMAC(strings.TrimSpace(raw))
	if err != nil || len(mac) != 6 {
		return ""
	}
	if mac[0]&1 != 0 {
		return ""
	}
	zero := true
	for _, v := range mac {
		if v != 0 {
			zero = false
			break
		}
	}
	if zero {
		return ""
	}
	return mac.String()
}

func sameMAC(a, b string) bool {
	left, right := normalizeMAC(a), normalizeMAC(b)
	return left != "" && left == right
}

func ipv6DefaultIf(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) < 10 {
			continue
		}
		if fields[0] == strings.Repeat("0", 32) && fields[1] == "00" {
			return fields[len(fields)-1]
		}
	}
	return ""
}

// ProbeManagement reports whether the management path still has addresses.
// After a LAN-bridge apply the recorded addresses may move from the uplink
// onto the No-dal bridge. That is success, not lockout.
func ProbeManagement(host HostView, name string, ifindex int, addrs ...string) error {
	if len(addrs) > 0 {
		for _, iface := range host.Ifaces {
			if containsAny(iface.Addresses, addrs) {
				return nil
			}
		}
	}
	if host.DefaultRouteIf != "" {
		if iface, ok := lookup(host, host.DefaultRouteIf); ok && len(iface.Addresses) > 0 {
			return nil
		}
	}
	if name != "" {
		if iface, ok := lookup(host, name); ok && len(iface.Addresses) > 0 {
			return nil
		}
	}
	if ifindex > 0 {
		for _, iface := range host.Ifaces {
			if iface.IfIndex == ifindex && len(iface.Addresses) > 0 {
				return nil
			}
		}
	}
	if name == "" && ifindex <= 0 && len(addrs) == 0 {
		return nil
	}
	return fmt.Errorf("management path has no addresses")
}

// WithAddresses copies addrs onto matching interface names.
func (h HostView) WithAddresses(name string, addrs ...string) HostView {
	for i := range h.Ifaces {
		if sameIface(h.Ifaces[i].Name, name) {
			h.Ifaces[i].Addresses = append([]string{}, addrs...)
		}
	}
	h.ManagementAddresses = append([]string{}, addrs...)
	return h
}

func firstNonEmpty(v ...string) string {
	for _, s := range v {
		if strings.TrimSpace(s) != "" {
			return s
		}
	}
	return ""
}
