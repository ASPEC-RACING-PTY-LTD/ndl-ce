package storage

import (
	"fmt"
	"path"
	"regexp"
	"strings"
)

const (
	BackendZFS     = "zfs"
	FormatZvol     = "zvol"
	FormatDataset  = "dataset"
	ZPoolBin       = "/usr/sbin/zpool"
	ZFSBin         = "/usr/sbin/zfs"
	ZVolDevPrefix  = "/dev/zvol/"
	ZFSMountRoot   = "/var/lib/ndl/storage/zfs"
	ZFSForceRefuse = "zpool import -f is refused"
	ZFSRootRefuse  = "ZFS pools must be created on extra disks, not the host root disk"
	ZFSMissing     = "ZFS is not installed on this host. Directory storage remains first-class."
	ZFSUnsupported = "ZFS runtime install uses the Debian 13 adapter. This host is not Debian 13 amd64."
)

var zpoolGUIDRe = regexp.MustCompile(`^[0-9]{1,20}$`)
var zfsNameRe = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9._:-]{0,62}$`)
var diskLocatorRe = regexp.MustCompile(`^/dev/(disk/by-id/[A-Za-z0-9._:+-]+|vd[a-z]+|sd[a-z]+|nvme[0-9]+n[0-9]+)$`)

// ZFSCapabilities is honest ZFS: snapshots and incremental send exist. Directory stays default.
func ZFSCapabilities() Capabilities {
	return Capabilities{
		VolumeCreate:    true,
		SparseFiles:     false,
		Snapshots:       true,
		IncrementalSend: true,
		XattrIdentity:   false,
		SharedWarning:   false,
		SupportedClasses: []string{
			ClassVMDisk, ClassContainerRoot, ClassISO, ClassTemplate, ClassBackupStaging,
		},
	}
}

// ParseZPoolGUID accepts a ZFS pool GUID locator. Names are not identity.
func ParseZPoolGUID(guid string) (string, error) {
	guid = strings.TrimSpace(guid)
	if guid == "" {
		return "", fmt.Errorf("zpool_guid is required")
	}
	if strings.Contains(guid, "-f") || strings.EqualFold(guid, "force") {
		return "", fmt.Errorf(ZFSForceRefuse)
	}
	if !zpoolGUIDRe.MatchString(guid) {
		return "", fmt.Errorf("zpool_guid must be a numeric pool GUID")
	}
	return guid, nil
}

// ParseZFSName validates a pool or dataset component. It is not a UUID.
func ParseZFSName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", fmt.Errorf("zfs name is required")
	}
	if strings.ContainsAny(name, " \n\r\t/\\\"'") || strings.Contains(name, "..") {
		return "", fmt.Errorf("zfs name is invalid")
	}
	if !zfsNameRe.MatchString(name) {
		return "", fmt.Errorf("zfs name is invalid")
	}
	return name, nil
}

// ParseDiskLocator allows extra-disk locators only.
func ParseDiskLocator(p, rootDev string) (string, error) {
	p = strings.TrimSpace(p)
	if p == "" {
		return "", fmt.Errorf("disk locator is required")
	}
	if strings.Contains(p, "..") || strings.ContainsAny(p, " \n\r,=") {
		return "", fmt.Errorf("disk locator is invalid")
	}
	if !diskLocatorRe.MatchString(p) {
		return "", fmt.Errorf("disk locator must be a by-id or extra-disk path")
	}
	if rootDev != "" && (p == rootDev || strings.HasPrefix(p, rootDev)) {
		return "", fmt.Errorf(ZFSRootRefuse)
	}
	if p == "/" || p == "/dev/root" {
		return "", fmt.Errorf(ZFSRootRefuse)
	}
	return p, nil
}

// RefuseForceImport is the product default. Destroyed/foreign pools stay unimported.
func RefuseForceImport(force bool) error {
	if force {
		return fmt.Errorf(ZFSForceRefuse)
	}
	return nil
}

// DatasetName is pool/volumeUUID. UUID is desired identity; the dataset name is a locator.
func DatasetName(poolName, volumeID string) (string, error) {
	poolName, err := ParseZFSName(poolName)
	if err != nil {
		return "", err
	}
	volumeID = strings.TrimSpace(volumeID)
	if volumeID == "" || strings.Contains(volumeID, "/") {
		return "", fmt.Errorf("volume_id must be a UUID")
	}
	return poolName + "/" + volumeID, nil
}

// ZVolPath is the host locator for a zvol. It is not product identity.
func ZVolPath(dataset string) string {
	return ZVolDevPrefix + dataset
}

// ZFSImportArgv imports by GUID under the ZFS storage root. Force is never present.
func ZFSImportArgv(guid string) ([]string, error) {
	guid, err := ParseZPoolGUID(guid)
	if err != nil {
		return nil, err
	}
	alt := ZFSMountRoot + "/" + guid
	return []string{ZPoolBin, "import", "-N", "-R", alt, guid}, nil
}

// ZFSCreatePoolArgv creates a pool on extra disks. Root disks are refused earlier.
func ZFSCreatePoolArgv(name string, disks []string) ([]string, error) {
	name, err := ParseZFSName(name)
	if err != nil {
		return nil, err
	}
	if len(disks) == 0 {
		return nil, fmt.Errorf("at least one extra disk is required")
	}
	mount := ZFSMountRoot + "/" + name
	argv := []string{ZPoolBin, "create", "-o", "ashift=12", "-m", mount, name}
	argv = append(argv, disks...)
	return argv, nil
}

// ZFSCreateDatasetArgv creates a per-UUID dataset for CT roots.
func ZFSCreateDatasetArgv(dataset, mount string) ([]string, error) {
	if strings.Contains(dataset, " ") || strings.Contains(dataset, "..") {
		return nil, fmt.Errorf("dataset locator is invalid")
	}
	if !strings.HasPrefix(mount, ZFSMountRoot+"/") {
		return nil, fmt.Errorf("dataset mount must be under the ZFS storage root")
	}
	return []string{ZFSBin, "create", "-o", "mountpoint=" + mount, dataset}, nil
}

// ZFSCreateZVolArgv creates a zvol for a VM disk.
func ZFSCreateZVolArgv(dataset string, sizeBytes int64) ([]string, error) {
	if sizeBytes < MinBlockBytes {
		return nil, fmt.Errorf("zvol size is too small")
	}
	if strings.Contains(dataset, " ") || strings.Contains(dataset, "..") {
		return nil, fmt.Errorf("zvol locator is invalid")
	}
	return []string{ZFSBin, "create", "-V", fmt.Sprintf("%d", sizeBytes), "-o", "volmode=dev", dataset}, nil
}

// ZFSSnapshotArgv snapshots one dataset or zvol.
func ZFSSnapshotArgv(dataset, snap string) ([]string, error) {
	snap = strings.TrimSpace(snap)
	if snap == "" || strings.ContainsAny(snap, " /@") {
		return nil, fmt.Errorf("snapshot name is invalid")
	}
	return []string{ZFSBin, "snapshot", dataset + "@" + snap}, nil
}

// ZFSSendArgv is a BackupSource. Incremental send uses -i when from is set.
func ZFSSendArgv(dataset, snap, from string) ([]string, error) {
	full := dataset + "@" + snap
	if from == "" {
		return []string{ZFSBin, "send", full}, nil
	}
	if strings.ContainsAny(from, " ") {
		return nil, fmt.Errorf("incremental from snapshot is invalid")
	}
	return []string{ZFSBin, "send", "-i", from, full}, nil
}

// ZFSGetGUIDArgv reads a pool GUID locator.
func ZFSGetGUIDArgv(name string) ([]string, error) {
	name, err := ParseZFSName(name)
	if err != nil {
		return nil, err
	}
	return []string{ZPoolBin, "get", "-H", "-o", "value", "guid", name}, nil
}

// ZFSStatusArgv observes pool health. Missing/pulled disks surface as faulted.
func ZFSStatusArgv(name string) ([]string, error) {
	name, err := ParseZFSName(name)
	if err != nil {
		return nil, err
	}
	return []string{ZPoolBin, "status", "-P", name}, nil
}

// ZFSRollbackArgv rolls one dataset or zvol back to a snapshot. It does not pass -R.
func ZFSRollbackArgv(dataset, snap string) ([]string, error) {
	snap = strings.TrimSpace(snap)
	if snap == "" || strings.ContainsAny(snap, " /@") {
		return nil, fmt.Errorf("snapshot name is invalid")
	}
	if strings.Contains(dataset, " ") || strings.Contains(dataset, "..") {
		return nil, fmt.Errorf("dataset locator is invalid")
	}
	return []string{ZFSBin, "rollback", dataset + "@" + snap}, nil
}

// ValidateZVolPath allows a typed /dev/zvol locator only.
func ValidateZVolPath(p string) error {
	if !strings.HasPrefix(p, ZVolDevPrefix) {
		return fmt.Errorf("zvol locator is invalid")
	}
	rest := strings.TrimPrefix(p, ZVolDevPrefix)
	if rest == "" || strings.Contains(rest, "..") {
		return fmt.Errorf("zvol locator is invalid")
	}
	for _, r := range rest {
		ok := (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '/' || r == '.' || r == '_' || r == '-'
		if !ok {
			return fmt.Errorf("zvol locator is invalid")
		}
	}
	return nil
}

// HostVolumePath returns the host locator for a volume. UUID remains desired identity.
func HostVolumePath(backendType, rootPath, backendRef string) (string, error) {
	backendRef = strings.TrimSpace(backendRef)
	if backendType == BackendZFS {
		if strings.HasPrefix(backendRef, ZVolDevPrefix) {
			if err := ValidateZVolPath(backendRef); err != nil {
				return "", err
			}
			return backendRef, nil
		}
		if strings.HasPrefix(backendRef, ZFSMountRoot+"/") {
			cleaned := path.Clean(backendRef)
			if cleaned != backendRef || strings.Contains(backendRef, "..") {
				return "", fmt.Errorf("volume locator is invalid")
			}
			return cleaned, nil
		}
		if strings.HasPrefix(backendRef, "/dev/") {
			return "", fmt.Errorf("volume locator is invalid")
		}
	}
	if backendType == BackendLVM {
		if strings.HasPrefix(backendRef, LVMMountRoot+"/") {
			cleaned := path.Clean(backendRef)
			if cleaned != backendRef || strings.Contains(backendRef, "..") {
				return "", fmt.Errorf("volume locator is invalid")
			}
			return cleaned, nil
		}
		if err := ValidateLVMDevice(backendRef); err != nil {
			return "", err
		}
		return backendRef, nil
	}
	return JoinUnder(rootPath, backendRef)
}

// QEMUFormat maps volume format to a QEMU driver. zvol and thin LV are raw, never user argv.
func QEMUFormat(backendType, format string) string {
	if backendType == BackendZFS || format == FormatZvol {
		return "raw"
	}
	if backendType == BackendLVM || format == FormatThinLV {
		return "raw"
	}
	if format == "" {
		return FormatQCOW2
	}
	return format
}

// ParseSendDest allows a backup stream file. It is not a shell redirect.
func ParseSendDest(p string) (string, error) {
	p = strings.TrimSpace(p)
	if p == "" || !strings.HasPrefix(p, "/") {
		return "", fmt.Errorf("send dest must be an absolute path")
	}
	if strings.Contains(p, "..") || strings.ContainsAny(p, " \n\r\x00,=") {
		return "", fmt.Errorf("send dest is invalid")
	}
	if path.Clean(p) != p {
		return "", fmt.Errorf("send dest is not a clean locator")
	}
	if p == "/" {
		return "", fmt.Errorf("send dest is not an allowed backup locator")
	}
	banned := []string{"/etc/", "/boot/", "/dev/", "/proc/", "/sys/", "/usr/", "/bin/", "/sbin/", "/lib/", "/root/"}
	for _, b := range banned {
		if strings.HasPrefix(p, b) {
			return "", fmt.Errorf("send dest is not an allowed backup locator")
		}
	}
	if !strings.HasSuffix(p, ".zfs") {
		return "", fmt.Errorf("send dest must end with .zfs")
	}
	if err := AllowedArtifactPath(p); err != nil {
		return "", fmt.Errorf("send dest is not an allowed backup locator")
	}
	return p, nil
}
