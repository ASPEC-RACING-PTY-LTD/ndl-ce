package gpu

import (
	"fmt"
	"path"
	"regexp"
	"strings"

	"github.com/no-dal/ndl-ce/internal/inventory"
)

const (
	ModeRender  = "render"
	ModeCompute = "compute"
	ModeEncode  = "encode"
	ModeVFIO    = "vfio"
)

const (
	StatusUnassigned  = "unassigned"
	StatusAssigned    = "assigned"
	StatusFailed      = "failed"
	StatusUnsupported = "unsupported"
)

var bdfRe = regexp.MustCompile(`^[0-9a-fA-F]{4}:[0-9a-fA-F]{2}:[0-9a-fA-F]{2}\.[0-9a-fA-F]$`)

// ParseGPUID rejects gpu=all and requires a PCI BDF locator.
func ParseGPUID(id string) (string, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return "", fmt.Errorf("gpu_id is required")
	}
	if strings.EqualFold(id, "all") {
		return "", fmt.Errorf("gpu=all is refused")
	}
	if !bdfRe.MatchString(id) {
		return "", fmt.Errorf("gpu_id must be a PCI address")
	}
	return strings.ToLower(id), nil
}

// ParseMode returns a supported assignment mode.
func ParseMode(mode string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case ModeRender, ModeCompute, ModeEncode, ModeVFIO:
		return strings.ToLower(strings.TrimSpace(mode)), nil
	case "":
		return "", fmt.Errorf("mode is required")
	default:
		return "", fmt.Errorf("mode is unsupported")
	}
}

// RefuseACSOverride is the product default. ACS override is not a supported assign flag.
func RefuseACSOverride(override bool) error {
	if override {
		return fmt.Errorf("ACS override is refused as the product default")
	}
	return nil
}

// ExclusiveForMode is true when the mode takes the whole device or IOMMU group.
func ExclusiveForMode(mode string, exclusive bool) bool {
	if mode == ModeVFIO {
		return true
	}
	return exclusive
}

// AllowDeviceNode is the only host device path prefix list used for CT bind-mounts.
func AllowDeviceNode(p string) bool {
	p = path.Clean(strings.TrimSpace(p))
	if !strings.HasPrefix(p, "/dev/") || strings.Contains(p, "..") {
		return false
	}
	for _, prefix := range []string{"/dev/dri/renderD", "/dev/dri/card", "/dev/nvidia", "/dev/kfd", "/dev/dri/by-path/"} {
		if strings.HasPrefix(p, prefix) {
			return true
		}
	}
	return false
}

// GroupMembers returns every PCI function in the GPU's IOMMU group, including HDMI audio.
func GroupMembers(gpuID string, inv inventory.Inventory) (groupID string, members []inventory.PCIDevice, err error) {
	gpuID, err = ParseGPUID(gpuID)
	if err != nil {
		return "", nil, err
	}
	var gpu inventory.GPU
	found := false
	for _, g := range inv.GPUs {
		if strings.EqualFold(g.ID, gpuID) || strings.EqualFold(g.PCI, gpuID) {
			gpu = g
			found = true
			break
		}
	}
	if !found {
		return "", nil, fmt.Errorf("gpu is not present on this node")
	}
	groupID = gpu.IOMMUGroup
	if groupID == "" {
		for _, g := range inv.IOMMU.Groups {
			for _, d := range g.Devices {
				if strings.EqualFold(d, gpuID) {
					groupID = g.ID
					break
				}
			}
		}
	}
	if groupID == "" {
		return "", []inventory.PCIDevice{{Address: gpuID, Class: "0x030000"}}, nil
	}
	var addrs []string
	for _, g := range inv.IOMMU.Groups {
		if g.ID == groupID {
			addrs = g.Devices
			break
		}
	}
	byAddr := map[string]inventory.PCIDevice{}
	for _, p := range inv.PCI {
		byAddr[strings.ToLower(p.Address)] = p
	}
	for _, addr := range addrs {
		p, ok := byAddr[strings.ToLower(addr)]
		if !ok {
			p = inventory.PCIDevice{Address: addr, IOMMUGroup: groupID}
		}
		members = append(members, p)
	}
	return groupID, members, nil
}

// MemberKind classifies a PCI function for honest IOMMU group listing.
func MemberKind(class string) string {
	c := strings.ToLower(strings.TrimPrefix(strings.TrimSpace(class), "0x"))
	c = strings.ReplaceAll(c, ":", "")
	if len(c) >= 2 && c[:2] == "03" {
		return "display"
	}
	if len(c) >= 4 && c[:4] == "0403" {
		return "audio"
	}
	if len(c) >= 2 && c[:2] == "04" {
		return "multimedia"
	}
	return "other"
}

// Conflicts reports whether an existing exclusive-or-same exclusive claim blocks a new one.
func Conflicts(existingExclusive bool, existingGPU, newGPU string, newExclusive bool) bool {
	if !strings.EqualFold(existingGPU, newGPU) {
		return false
	}
	return existingExclusive || newExclusive
}
