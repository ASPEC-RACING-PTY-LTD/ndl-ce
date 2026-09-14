package inventory

import (
	"regexp"
	"sort"
	"strings"
)

var nvmeNamespace = regexp.MustCompile(`^nvme[0-9]+n[0-9]+$`)

func collectBlock(opt Options) []BlockDevice {
	fs := opt.fs()
	mounts := mountHints(fs.readOK("proc/mounts"))
	names := fs.list("sys/class/block")
	sort.Strings(names)

	var out []BlockDevice
	for _, name := range names {
		if skipBlockName(name) {
			continue
		}
		base := "sys/class/block/" + name
		if fs.exists(base + "/partition") {
			continue
		}
		dev := BlockDevice{
			Name:        name,
			Kernel:      name,
			SMARTStatus: StatusNotReported,
		}
		if sectors, ok := fs.readUint(base + "/size"); ok {
			dev.SizeBytes = sectors * 512
		}
		dev.Rotational = readBool01(fs, base+"/queue/rotational")
		dev.Removable = readBool01(fs, base+"/removable")
		if n, ok := fs.readUint(base + "/queue/logical_block_size"); ok {
			dev.LogicalBlock = n
		}
		if n, ok := fs.readUint(base + "/queue/physical_block_size"); ok {
			dev.PhysicalBlock = n
		}
		dev.Model = firstNonEmpty(
			fs.readOK(base+"/device/model"),
			fs.readOK(base+"/nvme/model"),
		)
		dev.Vendor = firstNonEmpty(
			fs.readOK(base+"/device/vendor"),
			fs.readOK(base+"/nvme/vendor"),
		)
		dev.Serial = firstNonEmpty(
			fs.readOK(base+"/device/serial"),
			fs.readOK(base+"/nvme/serial"),
		)
		uevent := parseUevent(fs.readOK(base + "/device/uevent"))
		if nvmeNamespace.MatchString(name) {
			dev.Kind = "nvme"
		} else {
			dev.Kind = "disk"
		}
		dev.Transport = blockTransport(name, uevent)
		dev.MountHint = mounts[name]
		out = append(out, dev)
	}
	return out
}

func skipBlockName(name string) bool {
	return unitPrefixed(name, "loop") || unitPrefixed(name, "ram") || unitPrefixed(name, "fd")
}

func unitPrefixed(name, prefix string) bool {
	if !strings.HasPrefix(name, prefix) {
		return false
	}
	rest := name[len(prefix):]
	return rest == "" || allDigits(rest)
}

func blockTransport(name string, uevent map[string]string) string {
	driver := strings.ToLower(uevent["DRIVER"])
	if nvmeNamespace.MatchString(name) || strings.HasPrefix(name, "nvme") || driver == "nvme" {
		return "nvme"
	}
	if strings.HasPrefix(name, "vd") || strings.Contains(driver, "virtio") {
		return "virtio"
	}
	if strings.HasPrefix(name, "sd") || strings.Contains(driver, "ahci") || strings.Contains(driver, "ata") {
		return "sata"
	}
	return "unknown"
}

func mountHints(mounts string) map[string]string {
	out := map[string]string{}
	for _, line := range strings.Split(mounts, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		dev := fields[0]
		name := strings.TrimPrefix(dev, "/dev/")
		if name == dev {
			continue
		}
		if _, exists := out[name]; !exists {
			out[name] = fields[1]
		}
	}
	for name, mp := range out {
		if mp != "/" {
			continue
		}
		parent := parentBlockName(name)
		if parent != name {
			if _, exists := out[parent]; !exists {
				out[parent] = "/"
			}
		}
	}
	return out
}

// parentBlockName maps a partition (sda1, nvme0n1p2) to its disk. Whole disks stay unchanged.
func parentBlockName(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return name
	}
	if nvmeNamespace.MatchString(name) {
		return name
	}
	if strings.HasPrefix(name, "nvme") {
		if i := strings.LastIndex(name, "p"); i > 0 && allDigits(name[i+1:]) {
			base := name[:i]
			if nvmeNamespace.MatchString(base) {
				return base
			}
		}
		return name
	}
	if strings.Contains(name, "p") && (strings.HasPrefix(name, "mmcblk") || strings.HasPrefix(name, "loop") || strings.HasPrefix(name, "nbd")) {
		if i := strings.LastIndex(name, "p"); i > 0 && allDigits(name[i+1:]) {
			return name[:i]
		}
		return name
	}
	i := len(name)
	for i > 0 && name[i-1] >= '0' && name[i-1] <= '9' {
		i--
	}
	if i > 0 && i < len(name) {
		return name[:i]
	}
	return name
}

func readBool01(fs FS, p string) *bool {
	n, ok := fs.readInt(p)
	if !ok || (n != 0 && n != 1) {
		return nil
	}
	v := n == 1
	return &v
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}
