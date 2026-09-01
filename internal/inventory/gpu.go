package inventory

func collectGPUs(_ Options, pci []PCIDevice) []GPU {
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
		}
		if v, d := normalizeHex(p.Vendor), normalizeHex(p.Device); v != "" && d != "" {
			g.Model = "PCI " + v + ":" + d
		}
		out = append(out, g)
	}
	return out
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
