package physdisk

import (
	"path"
	"strconv"
	"strings"

	"github.com/no-dal/ndl-ce/internal/inventory"
)

// Discover lists whole disks from a host or fixture tree and applies eligibility.
func Discover(fs inventory.FS, hints HostHints) []Device {
	if hints.Mounts == nil {
		hints.Mounts = readMounts(fs)
	}
	if hints.Swaps == nil {
		hints.Swaps = readSwaps(fs)
	}
	if hints.RootKernel == "" {
		hints.RootKernel = parentKernel(hints.Mounts["/"])
		if hints.RootKernel == "" {
			for name, mp := range hints.Mounts {
				if mp == "/" {
					hints.RootKernel = parentKernel(name)
					break
				}
			}
		}
	}
	if len(hints.BootKernels) == 0 {
		hints.BootKernels = bootKernels(hints.Mounts)
	}
	if hints.HolderKinds == nil {
		hints.HolderKinds = readHolders(fs)
	}
	if hints.Udev == nil {
		hints.Udev = readUdev(fs)
	}
	if hints.ZFSMembers == nil {
		hints.ZFSMembers = membersOf(hints.HolderKinds, hints.Udev, "zfs")
	}
	if hints.LVMPVs == nil {
		hints.LVMPVs = membersOf(hints.HolderKinds, hints.Udev, "lvm")
	}

	byID := byIDIndex(fs)
	names := fs.List("sys/class/block")
	var out []Device
	for _, name := range names {
		if skipKernel(name) {
			continue
		}
		base := "sys/class/block/" + name
		if fs.Exists(base + "/partition") {
			continue
		}
		dev := Device{
			KernelName: name,
			KernelPath: "/dev/" + name,
		}
		if sectors, ok := readUint(fs, base+"/size"); ok {
			dev.SizeBytes = sectors * 512
		}
		dev.Rotational = readBool01(fs, base+"/queue/rotational")
		dev.Removable = readBool01(fs, base+"/queue/removable")
		if dev.Removable == nil {
			dev.Removable = readBool01(fs, base+"/removable")
		}
		dev.Model = firstNonEmpty(fs.ReadOK(base+"/device/model"), fs.ReadOK(base+"/nvme/model"))
		dev.Vendor = firstNonEmpty(fs.ReadOK(base+"/device/vendor"), fs.ReadOK(base+"/nvme/vendor"))
		dev.Serial = firstNonEmpty(fs.ReadOK(base+"/device/serial"), fs.ReadOK(base+"/nvme/serial"))
		dev.Transport = transportOf(name)
		ids := byID[name]
		dev.ID = PreferID(ids)
		if dev.ID != "" {
			dev.ByIDPath = dev.ID.Path()
		}
		dev.Partitions = collectPartitions(fs, name, hints)
		dev.FSSignatures = signaturesOf(dev.Partitions, hints.Udev, name)
		dev.Mounted = mountsOf(name, hints.Mounts, dev.Partitions)
		dev.Swap = swapOf(name, hints.Swaps, dev.Partitions)
		dev.ExistingData = len(dev.Partitions) > 0 || len(dev.FSSignatures) > 0
		applyEligibility(&dev, hints)
		out = append(out, dev)
	}
	return out
}

func collectPartitions(fs inventory.FS, disk string, hints HostHints) []Partition {
	var out []Partition
	for _, name := range fs.List("sys/class/block") {
		if !isPartitionOf(name, disk) {
			continue
		}
		p := Partition{Kernel: name}
		if n := partitionNumber(name, disk); n > 0 {
			p.Number = n
		}
		if sectors, ok := readUint(fs, "sys/class/block/"+name+"/size"); ok {
			p.SizeBytes = sectors * 512
		}
		if u, ok := hints.Udev[name]; ok {
			p.FSType = u.FSType
			p.Label = u.FSLabel
			p.PartType = u.PartType
		}
		if mp, ok := hints.Mounts[name]; ok {
			p.MountPoint = mp
		}
		for _, sw := range hints.Swaps {
			if sw == name || sw == "/dev/"+name {
				p.Swap = true
			}
		}
		out = append(out, p)
	}
	return out
}

func applyEligibility(dev *Device, hints HostHints) {
	var reasons []string
	add := func(r string) {
		for _, e := range reasons {
			if e == r {
				return
			}
		}
		reasons = append(reasons, r)
	}
	if skipKernel(dev.KernelName) || strings.HasPrefix(dev.KernelName, "loop") || strings.HasPrefix(dev.KernelName, "zram") {
		add(ReasonVirtual)
	}
	if dev.ID == "" {
		add(ReasonNoID)
	}
	if parentKernel(hints.RootKernel) == dev.KernelName || hints.RootKernel == dev.KernelName {
		dev.HostDisk = true
		add(ReasonHostRoot)
	}
	for _, b := range hints.BootKernels {
		if parentKernel(b) == dev.KernelName || b == dev.KernelName {
			dev.HostDisk = true
			add(ReasonHostBoot)
		}
	}
	if len(dev.Mounted) > 0 {
		for _, mp := range dev.Mounted {
			if mp == "/" {
				dev.HostDisk = true
				add(ReasonHostRoot)
			} else if mp == "/boot" || strings.HasPrefix(mp, "/boot/") || mp == "/efi" || mp == "/boot/efi" {
				dev.HostDisk = true
				add(ReasonHostBoot)
			} else {
				add(ReasonMounted + " at " + mp)
			}
		}
	}
	if dev.Swap {
		add(ReasonSwap)
	}
	if matchesAny(dev, hints.ZFSMembers) || hints.HolderKinds[dev.KernelName] == "zfs" || udevUsage(hints.Udev, dev.KernelName) == "zfs" {
		add(ReasonZFS)
	}
	if matchesAny(dev, hints.LVMPVs) || hints.HolderKinds[dev.KernelName] == "lvm" || udevUsage(hints.Udev, dev.KernelName) == "lvm" {
		add(ReasonLVM)
	}
	for _, p := range hints.PoolDisks {
		if sameDisk(p.Disk, *dev) {
			kind := firstNonEmpty(p.Kind, "storage pool")
			name := strings.TrimSpace(p.Name)
			if name != "" {
				add("In use: member of " + kind + " pool " + strconv.Quote(name))
			} else {
				add(ReasonPool)
			}
			dev.PoolKind = kind
			dev.PoolName = name
		}
	}
	for _, a := range hints.Assignments {
		if a.DeviceID == dev.ID || sameDisk(string(a.DeviceID), *dev) || (a.KernelName != "" && a.KernelName == dev.KernelName) {
			dev.AssignedID = a.WorkloadID
			dev.AssignedName = a.WorkloadName
			dev.AssignedRunning = a.Running
			if hints.SelfWorkloadID != "" && a.WorkloadID == hints.SelfWorkloadID {
				continue
			}
			if hints.SelfWorkloadID != "" && a.WorkloadID != hints.SelfWorkloadID {
				if a.WorkloadName != "" {
					add("In use: assigned to VM " + strconv.Quote(a.WorkloadName))
				} else {
					add(ReasonAssigned)
				}
				continue
			}
			if a.WorkloadName != "" {
				add("In use: assigned to VM " + strconv.Quote(a.WorkloadName))
			} else {
				add(ReasonAssigned)
			}
		}
	}
	if dev.Removable != nil && *dev.Removable {
		add(ReasonRemovable)
	}
	dev.Reasons = reasons
	dev.Eligible = len(reasons) == 0
}

// Evaluate returns a copy of d with eligibility recomputed against hints.
func Evaluate(d Device, hints HostHints) Device {
	applyEligibility(&d, hints)
	return d
}

func byIDIndex(fs inventory.FS) map[string][]string {
	out := map[string][]string{}
	for _, name := range fs.List("dev/disk/by-id") {
		if name == "" || strings.HasPrefix(name, ".") {
			continue
		}
		target := fs.Readlink("dev/disk/by-id/" + name)
		kernel := kernelFromLink(target)
		if kernel == "" {
			continue
		}
		out[kernel] = append(out[kernel], name)
	}
	return out
}

func kernelFromLink(target string) string {
	target = strings.TrimSpace(target)
	target = strings.TrimPrefix(target, "/dev/")
	if i := strings.LastIndex(target, "/"); i >= 0 {
		target = target[i+1:]
	}
	return target
}

func readMounts(fs inventory.FS) map[string]string {
	out := map[string]string{}
	for _, line := range strings.Split(fs.ReadOK("proc/mounts"), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		dev := fields[0]
		if !strings.HasPrefix(dev, "/dev/") {
			continue
		}
		name := strings.TrimPrefix(dev, "/dev/")
		if _, exists := out[name]; !exists {
			out[name] = fields[1]
		}
	}
	return out
}

func readSwaps(fs inventory.FS) []string {
	var out []string
	for i, line := range strings.Split(fs.ReadOK("proc/swaps"), "\n") {
		if i == 0 {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) == 0 || !strings.HasPrefix(fields[0], "/dev/") {
			continue
		}
		out = append(out, strings.TrimPrefix(fields[0], "/dev/"))
	}
	return out
}

func bootKernels(mounts map[string]string) []string {
	var out []string
	for name, mp := range mounts {
		if mp == "/boot" || mp == "/boot/efi" || mp == "/efi" {
			out = append(out, name)
		}
	}
	return out
}

func readHolders(fs inventory.FS) map[string]string {
	out := map[string]string{}
	for _, name := range fs.List("sys/class/block") {
		for _, h := range fs.List("sys/class/block/" + name + "/holders") {
			kind := holderKind(h)
			if kind != "" {
				out[name] = kind
				out[parentKernel(name)] = kind
			}
		}
	}
	return out
}

func holderKind(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	switch {
	case strings.HasPrefix(name, "dm-"):
		return "lvm"
	case strings.HasPrefix(name, "zd") || strings.Contains(name, "zvol"):
		return "zfs"
	case strings.HasPrefix(name, "md"):
		return "md"
	default:
		return ""
	}
}

func readUdev(fs inventory.FS) map[string]UdevInfo {
	out := map[string]UdevInfo{}
	for _, name := range fs.List("sys/class/block") {
		raw := fs.ReadOK("sys/class/block/" + name + "/dev")
		if raw == "" {
			continue
		}
		info := parseUdev(fs.ReadOK("run/udev/data/b" + strings.TrimSpace(raw)))
		if info != (UdevInfo{}) {
			out[name] = info
		}
	}
	return out
}

func parseUdev(raw string) UdevInfo {
	var u UdevInfo
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "E:") {
			continue
		}
		kv := strings.SplitN(strings.TrimPrefix(line, "E:"), "=", 2)
		if len(kv) != 2 {
			continue
		}
		switch kv[0] {
		case "ID_FS_TYPE":
			u.FSType = kv[1]
		case "ID_FS_LABEL":
			u.FSLabel = kv[1]
		case "ID_PART_ENTRY_TYPE":
			u.PartType = kv[1]
		case "ID_FS_USAGE":
			u.Usage = kv[1]
		}
	}
	return u
}

func membersOf(holders map[string]string, udev map[string]UdevInfo, kind string) []string {
	var out []string
	for name, h := range holders {
		if h == kind {
			out = append(out, name)
		}
	}
	for name, u := range udev {
		fs := strings.ToLower(u.FSType)
		if kind == "zfs" && (fs == "zfs_member" || fs == "zfs") {
			out = append(out, name)
		}
		if kind == "lvm" && (fs == "lvm2_member" || fs == "lvm2" || strings.Contains(fs, "lvm")) {
			out = append(out, name)
		}
	}
	return out
}

func signaturesOf(parts []Partition, udev map[string]UdevInfo, disk string) []string {
	seen := map[string]struct{}{}
	var out []string
	add := func(s string) {
		s = strings.ToLower(strings.TrimSpace(s))
		if s == "" {
			return
		}
		if _, ok := seen[s]; ok {
			return
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	if u, ok := udev[disk]; ok {
		add(u.FSType)
	}
	for _, p := range parts {
		add(p.FSType)
		if p.PartType != "" {
			if isEFI(p.PartType) {
				add("efi")
			}
		}
	}
	return out
}

func isEFI(partType string) bool {
	t := strings.ToLower(strings.TrimSpace(partType))
	return t == "c12a7328-f81f-11d2-ba4b-00a0c93ec93b" || t == "ef" || strings.Contains(t, "efi")
}

func mountsOf(disk string, mounts map[string]string, parts []Partition) []string {
	var out []string
	add := func(mp string) {
		if mp == "" {
			return
		}
		for _, e := range out {
			if e == mp {
				return
			}
		}
		out = append(out, mp)
	}
	if mp, ok := mounts[disk]; ok {
		add(mp)
	}
	for _, p := range parts {
		add(p.MountPoint)
		if mp, ok := mounts[p.Kernel]; ok {
			add(mp)
		}
	}
	return out
}

func swapOf(disk string, swaps []string, parts []Partition) bool {
	for _, s := range swaps {
		if s == disk || parentKernel(s) == disk {
			return true
		}
	}
	for _, p := range parts {
		if p.Swap {
			return true
		}
	}
	return false
}

func matchesAny(dev *Device, names []string) bool {
	for _, n := range names {
		if sameDisk(n, *dev) {
			return true
		}
	}
	return false
}

func sameDisk(ref string, dev Device) bool {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return false
	}
	if strings.HasPrefix(ref, ByIDDir+"/") {
		ref = strings.TrimPrefix(ref, ByIDDir+"/")
	}
	if id, err := ParseDeviceID(ref); err == nil && id == dev.ID {
		return true
	}
	ref = strings.TrimPrefix(ref, "/dev/")
	return ref == dev.KernelName || parentKernel(ref) == dev.KernelName
}

func udevUsage(udev map[string]UdevInfo, kernel string) string {
	u, ok := udev[kernel]
	if !ok {
		return ""
	}
	fs := strings.ToLower(u.FSType)
	switch {
	case fs == "zfs_member" || fs == "zfs":
		return "zfs"
	case strings.Contains(fs, "lvm"):
		return "lvm"
	case fs == "swap":
		return "swap"
	default:
		return ""
	}
}

func skipKernel(name string) bool {
	return unitPrefixed(name, "loop") || unitPrefixed(name, "ram") || unitPrefixed(name, "fd") || unitPrefixed(name, "zram")
}

func unitPrefixed(name, prefix string) bool {
	if !strings.HasPrefix(name, prefix) {
		return false
	}
	rest := name[len(prefix):]
	return rest == "" || allDigits(rest)
}

func transportOf(name string) string {
	switch {
	case strings.HasPrefix(name, "nvme"):
		return "nvme"
	case strings.HasPrefix(name, "vd"):
		return "virtio"
	case strings.HasPrefix(name, "sd"):
		return "sata"
	default:
		return "unknown"
	}
}

func isPartitionOf(name, disk string) bool {
	if name == disk {
		return false
	}
	if strings.HasPrefix(disk, "nvme") || strings.HasPrefix(disk, "mmcblk") {
		return strings.HasPrefix(name, disk+"p") && allDigits(strings.TrimPrefix(name, disk+"p"))
	}
	return strings.HasPrefix(name, disk) && allDigits(strings.TrimPrefix(name, disk))
}

func partitionNumber(name, disk string) int {
	rest := strings.TrimPrefix(name, disk)
	rest = strings.TrimPrefix(rest, "p")
	n, err := strconv.Atoi(rest)
	if err != nil {
		return 0
	}
	return n
}

func parentKernel(name string) string {
	name = strings.TrimPrefix(strings.TrimSpace(name), "/dev/")
	if name == "" {
		return ""
	}
	if strings.HasPrefix(name, "nvme") {
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

func readUint(fs inventory.FS, p string) (uint64, bool) {
	raw := strings.TrimSpace(fs.ReadOK(p))
	if raw == "" {
		return 0, false
	}
	if i := strings.IndexByte(raw, ':'); i > 0 {
		raw = raw[:i]
	}
	n, err := strconv.ParseUint(strings.Fields(raw)[0], 10, 64)
	if err != nil {
		return 0, false
	}
	return n, true
}

func readBool01(fs inventory.FS, p string) *bool {
	raw := strings.TrimSpace(fs.ReadOK(p))
	if raw != "0" && raw != "1" {
		return nil
	}
	v := raw == "1"
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

// ResolvePath maps a persisted device id onto the current host.
func ResolvePath(fs inventory.FS, id DeviceID) (string, error) {
	if id == "" {
		return "", fmtMissing("")
	}
	p := id.Path()
	rel := strings.TrimPrefix(p, "/")
	target := fs.Readlink(rel)
	if target == "" {
		target = fs.Readlink(p)
	}
	if fs.Exists(rel) || fs.Exists(p) || target != "" {
		if target != "" && !path.IsAbs(target) {
			target = path.Clean(path.Join(ByIDDir, target))
		}
		return p, nil
	}
	return "", fmtMissing(string(id))
}

func fmtMissing(id string) error {
	if id == "" {
		return errMissing
	}
	return missingError{ID: id}
}

var errMissing = missingError{}

type missingError struct{ ID string }

func (e missingError) Error() string {
	if e.ID == "" {
		return ReasonMissing + "."
	}
	return "Physical disk " + e.ID + " could not be resolved. The device may have been removed."
}
