// Package physdisk discovers host physical disks and decides whether they
// can be assigned exclusively to a QEMU VM. It is not a storage-pool backend.
package physdisk

import (
	"fmt"
	"path"
	"strings"
)

const (
	ByIDDir = "/dev/disk/by-id"

	SourcePhysical = "physical"
	SourceVolume   = "volume"

	BusAHCI   = "ahci"
	BusVirtio = "virtio-blk"

	RoleBoot = "boot"
	RoleData = "data"

	qemuUser = "ndl-qemu"
)

// DeviceID is the stable by-id basename. /dev/sdX is never identity.
type DeviceID string

// ParseDeviceID accepts a by-id basename or a /dev/disk/by-id path.
func ParseDeviceID(raw string) (DeviceID, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return "", fmt.Errorf("physical disk id is required")
	}
	if strings.Contains(s, "..") || strings.ContainsAny(s, " \n\r\t,=") {
		return "", fmt.Errorf("physical disk id is invalid")
	}
	if strings.HasPrefix(s, ByIDDir+"/") {
		s = strings.TrimPrefix(s, ByIDDir+"/")
	}
	if strings.Contains(s, "/") {
		return "", fmt.Errorf("physical disk id must be a by-id name")
	}
	if strings.HasSuffix(s, "-part") || partitionSuffix(s) {
		return "", fmt.Errorf("physical disk id must name the whole disk, not a partition")
	}
	if !looksLikeByID(s) {
		return "", fmt.Errorf("physical disk id must be a /dev/disk/by-id name")
	}
	return DeviceID(s), nil
}

func looksLikeByID(s string) bool {
	prefixes := []string{"ata-", "wwn-", "nvme-", "scsi-", "usb-", "mmc-"}
	for _, p := range prefixes {
		if strings.HasPrefix(s, p) && len(s) > len(p) {
			return true
		}
	}
	return false
}

func partitionSuffix(s string) bool {
	if i := strings.LastIndex(s, "-part"); i > 0 {
		rest := s[i+5:]
		return rest != "" && allDigits(rest)
	}
	return false
}

func allDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// Path is the stable /dev/disk/by-id locator.
func (id DeviceID) Path() string {
	if id == "" {
		return ""
	}
	return path.Join(ByIDDir, string(id))
}

func (id DeviceID) String() string { return string(id) }

// PreferID picks the most stable whole-disk by-id name from a set of names
// that all resolve to the same kernel disk. Partition names are ignored.
func PreferID(names []string) DeviceID {
	var wwn, eui, ata, scsi, nvme, other DeviceID
	for _, n := range names {
		n = strings.TrimSpace(n)
		if n == "" || partitionSuffix(n) {
			continue
		}
		id, err := ParseDeviceID(n)
		if err != nil {
			continue
		}
		s := string(id)
		switch {
		case strings.HasPrefix(s, "wwn-"):
			if wwn == "" {
				wwn = id
			}
		case strings.HasPrefix(s, "nvme-eui.") || strings.HasPrefix(s, "nvme-nvme."):
			if eui == "" {
				eui = id
			}
		case strings.HasPrefix(s, "ata-"):
			if ata == "" {
				ata = id
			}
		case strings.HasPrefix(s, "scsi-"):
			if scsi == "" {
				scsi = id
			}
		case strings.HasPrefix(s, "nvme-"):
			if nvme == "" {
				nvme = id
			}
		default:
			if other == "" {
				other = id
			}
		}
	}
	for _, id := range []DeviceID{wwn, eui, ata, scsi, nvme, other} {
		if id != "" {
			return id
		}
	}
	return ""
}

// Find returns the discovered device with the given stable id.
func Find(disks []Device, id DeviceID) (Device, bool) {
	for _, d := range disks {
		if d.ID == id {
			return d, true
		}
	}
	return Device{}, false
}

// BlockedError is the user-facing assignment refusal for an ineligible disk.
func BlockedError(dev Device) error {
	name := dev.DisplayName()
	id := string(dev.ID)
	if id == "" {
		id = dev.KernelName
	}
	if len(dev.Mounted) > 0 && !dev.HostDisk {
		return fmt.Errorf("Physical disk %s (%s) is currently mounted at %s and cannot be assigned.", name, id, strings.Join(dev.Mounted, ", "))
	}
	if dev.AssignedName != "" && dev.AssignedID != "" {
		if dev.AssignedRunning {
			return fmt.Errorf("Physical disk %s is already assigned to running VM %s.", id, dev.AssignedName)
		}
		return fmt.Errorf("Physical disk %s is already assigned to VM %s.", id, dev.AssignedName)
	}
	if len(dev.Reasons) > 0 {
		return fmt.Errorf("Physical disk %s (%s) cannot be assigned. %s.", name, id, strings.Join(dev.Reasons, "; "))
	}
	return fmt.Errorf("Physical disk %s (%s) cannot be assigned.", name, id)
}
