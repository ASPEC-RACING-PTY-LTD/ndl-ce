package qemu

import (
	"bufio"
	"bytes"
	"os"
	"strings"
)

const procNetARP = "/proc/net/arp"

// IPv4ForMAC returns the first complete IPv4 neighbor for mac from the
// host ARP table. Incomplete (0x0) entries are ignored.
func IPv4ForMAC(mac string) string {
	b, err := os.ReadFile(procNetARP)
	if err != nil {
		return ""
	}
	return IPv4ForMACTable(mac, b)
}

// IPv4ForMACTable parses a /proc/net/arp snapshot.
func IPv4ForMACTable(mac string, table []byte) string {
	want := normalizeMAC(mac)
	if want == "" {
		return ""
	}
	sc := bufio.NewScanner(bytes.NewReader(table))
	if sc.Scan() {
		// skip header
	}
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) < 4 {
			continue
		}
		if normalizeMAC(fields[3]) != want {
			continue
		}
		if len(fields) > 2 && fields[2] == "0x0" {
			continue
		}
		if ip := fields[0]; looksIPv4(ip) {
			return ip
		}
	}
	return ""
}

func normalizeMAC(mac string) string {
	mac = strings.ToLower(strings.TrimSpace(mac))
	mac = strings.ReplaceAll(mac, "-", ":")
	return mac
}

func looksIPv4(s string) bool {
	parts := strings.Split(s, ".")
	if len(parts) != 4 {
		return false
	}
	for _, p := range parts {
		if p == "" {
			return false
		}
		for _, r := range p {
			if r < '0' || r > '9' {
				return false
			}
		}
	}
	return true
}
