package vmspec

import (
	"fmt"
	"strings"

	"github.com/google/uuid"
)

func Validate(spec Spec) error {
	spec = Normalize(spec)
	if spec.Name == "" {
		return fmt.Errorf("name is required")
	}
	if strings.ContainsAny(spec.Name, "\n\r\x00") {
		return fmt.Errorf("name contains a banned character")
	}
	if spec.CPUs < 1 || spec.CPUs > 64 {
		return fmt.Errorf("cpus must be between 1 and 64")
	}
	if spec.MemoryBytes < 64<<20 || spec.MemoryBytes > 512<<30 {
		return fmt.Errorf("memory must be between 64 MiB and 512 GiB")
	}
	if err := ValidateMachine(spec.Machine); err != nil {
		return err
	}
	switch spec.Firmware {
	case FirmwareBIOS, FirmwareUEFI:
	default:
		return fmt.Errorf("firmware must be bios or uefi")
	}
	if spec.Firmware == FirmwareUEFI && strings.ContainsAny(spec.Firmware, ",=\n") {
		return fmt.Errorf("firmware contains a banned character")
	}
	boot := 0
	for i, d := range spec.Disks {
		switch d.Role {
		case DiskRoleBoot, DiskRoleData, DiskRoleCDROM, DiskRoleCIDATA, DiskRoleVars:
		default:
			return fmt.Errorf("disk %d has an unsupported role", i)
		}
		if d.Role == DiskRoleBoot {
			boot++
		}
		if d.VolumeID != "" {
			if _, err := uuid.Parse(d.VolumeID); err != nil {
				return fmt.Errorf("disk %d volume_id must be a UUID", i)
			}
		}
		if d.Format != "" && d.Format != "qcow2" && d.Format != "raw" {
			return fmt.Errorf("disk %d format must be qcow2 or raw", i)
		}
		if d.PCIAddr != "" {
			if err := ValidatePCIAddr(d.PCIAddr); err != nil {
				return fmt.Errorf("disk %d pci: %w", i, err)
			}
		}
	}
	if boot != 1 {
		return fmt.Errorf("exactly one boot disk is required")
	}
	if len(spec.NICs) < 1 {
		return fmt.Errorf("at least one NIC is required")
	}
	if len(spec.NICs) > 8 {
		return fmt.Errorf("at most 8 NICs are supported")
	}
	macs := map[string]struct{}{}
	for i, n := range spec.NICs {
		if _, err := uuid.Parse(strings.TrimSpace(n.NetworkID)); err != nil {
			return fmt.Errorf("nic %d network_id must be a UUID", i)
		}
		if n.Model != "" && n.Model != NICModelVirtio {
			return fmt.Errorf("nic %d model must be virtio", i)
		}
		if n.MAC != "" {
			if err := ValidateMAC(n.MAC); err != nil {
				return fmt.Errorf("nic %d mac: %w", i, err)
			}
			key := strings.ToLower(n.MAC)
			if _, ok := macs[key]; ok {
				return fmt.Errorf("nic %d mac is not unique", i)
			}
			macs[key] = struct{}{}
		}
		if n.PCIAddr != "" {
			if err := ValidatePCIAddr(n.PCIAddr); err != nil {
				return fmt.Errorf("nic %d pci: %w", i, err)
			}
		}
	}
	if spec.ISOLibraryID != "" {
		if _, err := uuid.Parse(spec.ISOLibraryID); err != nil {
			return fmt.Errorf("iso_library_id must be a UUID")
		}
	}
	if spec.CloudImageID != "" {
		if _, err := uuid.Parse(spec.CloudImageID); err != nil {
			return fmt.Errorf("cloud_image_id must be a UUID")
		}
	}
	for _, item := range spec.BootOrder {
		switch item {
		case "disk", "cdrom":
		default:
			return fmt.Errorf("boot_order values must be disk or cdrom")
		}
	}
	if strings.Contains(spec.NoCloud.UserData, "\x00") || strings.Contains(spec.NoCloud.NetworkConfig, "\x00") {
		return fmt.Errorf("nocloud data contains a banned character")
	}
	if spec.SecureBoot && spec.Firmware != FirmwareUEFI {
		return fmt.Errorf("secure boot requires UEFI")
	}
	for i, u := range spec.USBs {
		if err := ValidateUSB(u); err != nil {
			return fmt.Errorf("usb %d: %w", i, err)
		}
	}
	for i, host := range spec.PCIHosts {
		if strings.ContainsAny(host, ",=\n\r\x00") {
			return fmt.Errorf("pci host %d contains a banned character", i)
		}
	}
	return nil
}

func ValidateMachine(machine string) error {
	if machine == "q35" || strings.EqualFold(machine, "pc") {
		return fmt.Errorf("machine type must be a pinned pc-q35-X.Y, not an alias")
	}
	if !strings.HasPrefix(machine, "pc-q35-") {
		return fmt.Errorf("machine type must be pc-q35-X.Y")
	}
	rest := strings.TrimPrefix(machine, "pc-q35-")
	parts := strings.Split(rest, ".")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return fmt.Errorf("machine type must be pc-q35-X.Y")
	}
	for _, part := range parts {
		for _, c := range part {
			if c < '0' || c > '9' {
				return fmt.Errorf("machine type must be pc-q35-X.Y")
			}
		}
	}
	if strings.ContainsAny(machine, ",=\n\r\x00") {
		return fmt.Errorf("machine type contains a banned character")
	}
	return nil
}

func ValidatePCIAddr(addr string) error {
	if !strings.HasPrefix(addr, "0x") || len(addr) < 3 || len(addr) > 6 {
		return fmt.Errorf("pci address must be 0xNN")
	}
	for _, c := range addr[2:] {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') && (c < 'A' || c > 'F') {
			return fmt.Errorf("pci address must be 0xNN")
		}
	}
	return nil
}

func ValidateMAC(mac string) error {
	parts := strings.Split(strings.ToLower(strings.TrimSpace(mac)), ":")
	if len(parts) != 6 {
		return fmt.Errorf("mac must be six octets")
	}
	for _, p := range parts {
		if len(p) != 2 {
			return fmt.Errorf("mac must be six octets")
		}
		for _, c := range p {
			if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
				return fmt.Errorf("mac must be hex")
			}
		}
	}
	return nil
}

func ValidateWorkloadID(id string) error {
	if _, err := uuid.Parse(strings.TrimSpace(id)); err != nil {
		return fmt.Errorf("workload_id must be a UUID")
	}
	return nil
}

func ValidateCleanPath(p, kind string) error {
	if p == "" || !strings.HasPrefix(p, "/") {
		return fmt.Errorf("%s must be an absolute locator", kind)
	}
	if strings.Contains(p, "..") || strings.ContainsAny(p, ",=\n\r\x00") {
		return fmt.Errorf("%s contains a banned character", kind)
	}
	return nil
}
