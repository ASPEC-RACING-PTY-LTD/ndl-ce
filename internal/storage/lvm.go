package storage

import (
	"fmt"
	"path"
	"regexp"
	"strings"
)

const (
	BackendLVM      = "lvm"
	FormatThinLV    = "thin-lv"
	LVMBinPV        = "/usr/sbin/pvcreate"
	LVMBinVG        = "/usr/sbin/vgcreate"
	LVMBinLV        = "/usr/sbin/lvcreate"
	LVMBinConvert   = "/usr/sbin/lvconvert"
	LVMBinVGS       = "/usr/sbin/vgs"
	LVMBinLVS       = "/usr/sbin/lvs"
	LVMBinPVS       = "/usr/sbin/pvs"
	LVMMkfsBin      = "/usr/sbin/mkfs.ext4"
	LVMMountBin     = "/usr/bin/mount"
	LVMThinPoolName = "thinpool"
	LVMMountRoot    = "/var/lib/ndl/storage/lvm"
	LVMMissing      = "LVM is not installed on this host. Directory storage remains first-class."
	LVMUnsupported  = "LVM runtime install uses the Debian 13 adapter. This host is not Debian 13 amd64."
	LVMExportRefuse = "vgexport is refused"
	LVMRootRefuse   = "LVM-thin pools must be created on extra disks, not the host root disk"
	LVMMetadataMsg  = "This LVM thin pool metadata percent is high. Filling metadata can make the pool unavailable."
	LVMNoSend       = "LVM-thin does not support incremental send"
)

var (
	lvmVGNameRe = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9._+-]{0,62}$`)
	lvmLVNameRe = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._+-]{0,126}$`)
	lvmUUIDRe   = regexp.MustCompile(`^[A-Za-z0-9_-]{8,64}$`)
)

// LVMCapabilities is honest LVM-thin: snapshots exist, incremental send does not.
func LVMCapabilities() Capabilities {
	return Capabilities{
		VolumeCreate:    true,
		SparseFiles:     false,
		Snapshots:       true,
		IncrementalSend: false,
		XattrIdentity:   false,
		SharedWarning:   false,
		SupportedClasses: []string{
			ClassVMDisk, ClassContainerRoot, ClassISO, ClassTemplate, ClassBackupStaging,
		},
	}
}

// ParseVGName validates a volume group name. It is not a UUID.
func ParseVGName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", fmt.Errorf("volume group name is required")
	}
	if strings.ContainsAny(name, " \n\r\t/\\\"'") || strings.Contains(name, "..") {
		return "", fmt.Errorf("volume group name is invalid")
	}
	if strings.Contains(strings.ToLower(name), "vgexport") {
		return "", fmt.Errorf(LVMExportRefuse)
	}
	if !lvmVGNameRe.MatchString(name) {
		return "", fmt.Errorf("volume group name is invalid")
	}
	return name, nil
}

// ParseLVName validates a logical volume name. Volume UUIDs are allowed locators.
func ParseLVName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", fmt.Errorf("logical volume name is required")
	}
	if strings.ContainsAny(name, " \n\r\t/\\\"'@") || strings.Contains(name, "..") {
		return "", fmt.Errorf("logical volume name is invalid")
	}
	if strings.EqualFold(name, "vgexport") || strings.Contains(strings.ToLower(name), "vgexport") {
		return "", fmt.Errorf(LVMExportRefuse)
	}
	if !lvmLVNameRe.MatchString(name) {
		return "", fmt.Errorf("logical volume name is invalid")
	}
	return name, nil
}

// ParseVGUUID accepts an LVM volume group UUID locator.
func ParseVGUUID(id string) (string, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return "", fmt.Errorf("vg_uuid is required")
	}
	if strings.ContainsAny(id, " \n\r\t/\\\"'") || strings.Contains(id, "..") {
		return "", fmt.Errorf("vg_uuid is invalid")
	}
	if !lvmUUIDRe.MatchString(id) {
		return "", fmt.Errorf("vg_uuid is invalid")
	}
	return id, nil
}

// ParseLVMDisk allows extra-disk locators only. The host root disk is refused.
func ParseLVMDisk(p, rootDev string) (string, error) {
	p = strings.TrimSpace(p)
	if p == "" {
		return "", fmt.Errorf("disk locator is required")
	}
	if p == "/" || p == "/dev/root" {
		return "", fmt.Errorf(LVMRootRefuse)
	}
	loc, err := ParseDiskLocator(p, rootDev)
	if err != nil {
		if strings.Contains(err.Error(), "host root") || strings.Contains(err.Error(), "ZFS") {
			return "", fmt.Errorf(LVMRootRefuse)
		}
		return "", err
	}
	return loc, nil
}

// LVMDevicePath is the host locator for a thin LV. It is not product identity.
func LVMDevicePath(vg, lv string) string {
	return "/dev/" + vg + "/" + lv
}

// ValidateLVMDevice allows /dev/<vg>/<lv> or /dev/mapper/<vg>-<lv> locators only.
func ValidateLVMDevice(p string) error {
	p = strings.TrimSpace(p)
	if p == "" || strings.Contains(p, "..") || strings.ContainsAny(p, " \n\r\t") {
		return fmt.Errorf("lvm locator is invalid")
	}
	if strings.HasPrefix(p, "/dev/mapper/") {
		rest := strings.TrimPrefix(p, "/dev/mapper/")
		if rest == "" || strings.Contains(rest, "/") {
			return fmt.Errorf("lvm locator is invalid")
		}
		for _, r := range rest {
			ok := (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '.' || r == '_' || r == '-' || r == '+'
			if !ok {
				return fmt.Errorf("lvm locator is invalid")
			}
		}
		return nil
	}
	if !strings.HasPrefix(p, "/dev/") {
		return fmt.Errorf("lvm locator is invalid")
	}
	rest := strings.TrimPrefix(p, "/dev/")
	parts := strings.Split(rest, "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return fmt.Errorf("lvm locator is invalid")
	}
	if _, err := ParseVGName(parts[0]); err != nil {
		return fmt.Errorf("lvm locator is invalid")
	}
	if _, err := ParseLVName(parts[1]); err != nil {
		return fmt.Errorf("lvm locator is invalid")
	}
	return nil
}

// PVCreateArgv initializes one extra disk as a PV. vgexport is never present.
func PVCreateArgv(disk string) ([]string, error) {
	disk, err := ParseLVMDisk(disk, "")
	if err != nil {
		return nil, err
	}
	return []string{LVMBinPV, "--yes", disk}, nil
}

// VGCreateArgv creates a volume group on extra disks.
func VGCreateArgv(name string, disks []string) ([]string, error) {
	name, err := ParseVGName(name)
	if err != nil {
		return nil, err
	}
	if len(disks) == 0 {
		return nil, fmt.Errorf("at least one extra disk is required")
	}
	argv := []string{LVMBinVG, name}
	argv = append(argv, disks...)
	return argv, nil
}

// LVCreateThinPoolArgv creates the thin pool LV. Percent-free is typed, not caller argv.
func LVCreateThinPoolArgv(vg string) ([]string, error) {
	vg, err := ParseVGName(vg)
	if err != nil {
		return nil, err
	}
	return []string{LVMBinLV, "--type", "thin-pool", "-n", LVMThinPoolName, "-l", "95%FREE", vg}, nil
}

// LVCreateThinArgv creates a thin LV for a disk. Size is bytes, never a shell expression.
func LVCreateThinArgv(vg, lv string, sizeBytes int64) ([]string, error) {
	vg, err := ParseVGName(vg)
	if err != nil {
		return nil, err
	}
	lv, err = ParseLVName(lv)
	if err != nil {
		return nil, err
	}
	if sizeBytes < MinBlockBytes {
		return nil, fmt.Errorf("thin lv size is too small")
	}
	return []string{LVMBinLV, "-V", fmt.Sprintf("%dB", sizeBytes), "-T", vg + "/" + LVMThinPoolName, "-n", lv}, nil
}

// LVSnapshotArgv creates a thin snapshot of an origin LV.
func LVSnapshotArgv(vg, origin, snap string) ([]string, error) {
	vg, err := ParseVGName(vg)
	if err != nil {
		return nil, err
	}
	origin, err = ParseLVName(origin)
	if err != nil {
		return nil, err
	}
	snap, err = ParseLVName(snap)
	if err != nil {
		return nil, err
	}
	return []string{LVMBinLV, "-s", "-n", snap, vg + "/" + origin}, nil
}

// LVMergeArgv rolls a thin snapshot back onto its origin. The origin must be inactive.
func LVMergeArgv(vg, snap string) ([]string, error) {
	vg, err := ParseVGName(vg)
	if err != nil {
		return nil, err
	}
	snap, err = ParseLVName(snap)
	if err != nil {
		return nil, err
	}
	return []string{LVMBinConvert, "--merge", vg + "/" + snap}, nil
}

// VGSReportArgv observes VG size, UUID, and partial/missing PV attributes.
func VGSReportArgv(vg string) ([]string, error) {
	vg, err := ParseVGName(vg)
	if err != nil {
		return nil, err
	}
	return []string{LVMBinVGS, "-o", "vg_name,vg_uuid,vg_size,vg_free,vg_attr", "--units", "b", "--nosuffix", "--reportformat", "json", vg}, nil
}

// LVSReportArgv observes thin pool data and metadata percent.
func LVSReportArgv(vg string) ([]string, error) {
	vg, err := ParseVGName(vg)
	if err != nil {
		return nil, err
	}
	return []string{LVMBinLVS, "-o", "lv_name,lv_uuid,lv_size,lv_attr,data_percent,metadata_percent,pool_lv", "--units", "b", "--nosuffix", "--reportformat", "json", vg}, nil
}

// PVSReportArgv observes missing physical volumes. Desired rows remain when a PV is gone.
func PVSReportArgv(vg string) ([]string, error) {
	vg, err := ParseVGName(vg)
	if err != nil {
		return nil, err
	}
	return []string{LVMBinPVS, "-o", "pv_name,vg_name,pv_missing", "--reportformat", "json", vg}, nil
}

// LVUUIDArgv reads one LV UUID locator.
func LVUUIDArgv(vg, lv string) ([]string, error) {
	vg, err := ParseVGName(vg)
	if err != nil {
		return nil, err
	}
	lv, err = ParseLVName(lv)
	if err != nil {
		return nil, err
	}
	return []string{LVMBinLVS, "-o", "lv_uuid", "--noheadings", "--nosuffix", vg + "/" + lv}, nil
}

// VGUUIDArgv reads one VG UUID locator.
func VGUUIDArgv(vg string) ([]string, error) {
	vg, err := ParseVGName(vg)
	if err != nil {
		return nil, err
	}
	return []string{LVMBinVGS, "-o", "vg_uuid", "--noheadings", "--nosuffix", vg}, nil
}

// MkfsExt4Argv formats a filesystem thin LV. It is not generic host exec.
func MkfsExt4Argv(dev string) ([]string, error) {
	if err := ValidateLVMDevice(dev); err != nil {
		return nil, err
	}
	return []string{LVMMkfsBin, "-F", "-q", dev}, nil
}

// MountLVMArgv mounts a filesystem thin LV under the LVM storage root.
func MountLVMArgv(dev, mount string) ([]string, error) {
	if err := ValidateLVMDevice(dev); err != nil {
		return nil, err
	}
	cleaned := path.Clean(mount)
	if cleaned != mount || !strings.HasPrefix(mount, LVMMountRoot+"/") {
		return nil, fmt.Errorf("lvm mount must be under the LVM storage root")
	}
	if strings.Contains(mount, "..") {
		return nil, fmt.Errorf("lvm mount is invalid")
	}
	return []string{LVMMountBin, "-o", "nouuid", dev, mount}, nil
}

func refuseExportArgv(argv []string) error {
	for _, a := range argv {
		if a == "vgexport" || strings.HasSuffix(a, "/vgexport") || strings.EqualFold(a, "--export") {
			return fmt.Errorf(LVMExportRefuse)
		}
	}
	if len(argv) > 0 && (argv[0] == LVMBinPV || argv[0] == LVMBinVG || argv[0] == LVMBinLV || argv[0] == LVMBinConvert || argv[0] == LVMBinVGS || argv[0] == LVMBinLVS || argv[0] == LVMBinPVS || argv[0] == LVMMkfsBin || argv[0] == LVMMountBin) {
		return nil
	}
	if len(argv) == 0 {
		return fmt.Errorf("lvm argv is not typed")
	}
	return fmt.Errorf("lvm argv is not typed")
}
