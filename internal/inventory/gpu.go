package inventory

import "strings"

func collectGPUs(opt Options, pci []PCIDevice) []GPU {
	fs := opt.fs()
	var out []GPU
	for _, p := range pci {
		if !isDisplayPCI(p) {
			continue
		}
		g := GPU{
			ID:         p.Address,
			Vendor:     gpuVendorLabel(p.Vendor),
			PCI:        p.Address,
			Driver:     p.Driver,
			IOMMUGroup: p.IOMMUGroup,
			Hint:       gpuDeviceHint(fs, p.Address),
		}
		if v, d := normalizeHex(p.Vendor), normalizeHex(p.Device); v != "" && d != "" {
			g.Model = "PCI " + v + ":" + d
		}
		out = append(out, g)
	}
	return out
}

// gpuDeviceHint records observed DRI locators for a PCI GPU. It never invents
// /dev/dri/renderD128; only by-path nodes that exist on this host are listed.
func gpuDeviceHint(fs FS, pci string) string {
	pci = strings.ToLower(strings.TrimSpace(pci))
	if pci == "" {
		return ""
	}
	var nodes []string
	for _, suffix := range []string{"render", "card"} {
		p := "/dev/dri/by-path/pci-" + pci + "-" + suffix
		if fs.exists(p) {
			nodes = append(nodes, p)
		}
	}
	return strings.Join(nodes, " ")
}

func isDisplayPCI(p PCIDevice) bool {
	class := normalizeHex(p.Class)
	return len(class) >= 2 && class[:2] == "03"
}

func gpuVendorLabel(vendor string) string {
	switch normalizeHex(vendor) {
	case "10de":
		return "NVIDIA"
	case "1002":
		return "AMD"
	case "8086":
		return "Intel"
	case "":
		return ""
	default:
		return "other"
	}
}
