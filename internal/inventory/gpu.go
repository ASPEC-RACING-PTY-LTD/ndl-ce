package inventory

import (
	"strconv"
	"strings"
)

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
		if p.Driver == "nvidia" {
			g.NVIDIAMinor = nvidiaDeviceMinor(fs, p.Address)
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

// nvidiaDeviceMinor reads the /dev/nvidiaN minor the proprietary driver gave
// this PCI function. It returns nil when the driver does not report one, so
// callers never guess which /dev/nvidiaN belongs to which GPU.
func nvidiaDeviceMinor(fs FS, pci string) *int {
	pci = strings.ToLower(strings.TrimSpace(pci))
	if pci == "" {
		return nil
	}
	raw := fs.readOK("/proc/driver/nvidia/gpus/" + pci + "/information")
	for _, line := range strings.Split(raw, "\n") {
		key, val, ok := strings.Cut(line, ":")
		if !ok || !strings.EqualFold(strings.TrimSpace(key), "Device Minor") {
			continue
		}
		n, err := strconv.Atoi(strings.TrimSpace(val))
		if err != nil || n < 0 || n > 254 {
			return nil
		}
		return &n
	}
	return nil
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
