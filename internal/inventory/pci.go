package inventory

import (
	"sort"
	"strings"
)

func collectPCI(opt Options) []PCIDevice {
	fs := opt.fs()
	groups := iommuGroupByDevice(listIOMMUGroups(fs))
	names := fs.list("sys/bus/pci/devices")
	sort.Strings(names)

	var out []PCIDevice
	for _, addr := range names {
		base := "sys/bus/pci/devices/" + addr
		uevent := parseUevent(fs.readOK(base + "/uevent"))
		out = append(out, PCIDevice{
			Address:    addr,
			Vendor:     fs.readOK(base + "/vendor"),
			Device:     fs.readOK(base + "/device"),
			Class:      fs.readOK(base + "/class"),
			Driver:     uevent["DRIVER"],
			IOMMUGroup: groups[addr],
		})
	}
	return out
}

func parseUevent(s string) map[string]string {
	out := map[string]string{}
	for _, line := range strings.Split(s, "\n") {
		key, val, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		out[strings.TrimSpace(key)] = strings.TrimSpace(val)
	}
	return out
}

func normalizeHex(s string) string {
	s = strings.TrimSpace(strings.ToLower(s))
	return strings.TrimPrefix(s, "0x")
}
