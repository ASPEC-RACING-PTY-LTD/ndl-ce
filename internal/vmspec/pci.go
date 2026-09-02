package vmspec

import (
	"fmt"
	"strconv"
	"strings"
)

// PCI slots persisted after first compile. q35 host bridge occupies 0x00.
const (
	PCIVGA       = "0x02"
	PCISerial    = "0x03"
	PCIBalloon   = "0x04"
	PCIFirstDisk = 0x05
)

func AllocatePCI(spec Spec) (Spec, map[string]string, error) {
	used := map[int]string{}
	claim := func(key, addr string) error {
		if addr == "" {
			return nil
		}
		if err := ValidatePCIAddr(addr); err != nil {
			return err
		}
		n, err := strconv.ParseInt(strings.TrimSpace(addr), 0, 64)
		if err != nil || n < 2 || n > 0x1f {
			return fmt.Errorf("pci address %s is out of range", addr)
		}
		slot := int(n)
		if owner, ok := used[slot]; ok && owner != key {
			return fmt.Errorf("pci address %s is already used by %s", addr, owner)
		}
		used[slot] = key
		return nil
	}
	pci := map[string]string{
		"vga":    PCIVGA,
		"serial": PCISerial,
	}
	if spec.Balloon {
		pci["balloon"] = PCIBalloon
	}
	if err := claim("vga", pci["vga"]); err != nil {
		return spec, nil, err
	}
	if err := claim("serial", pci["serial"]); err != nil {
		return spec, nil, err
	}
	if spec.Balloon {
		if err := claim("balloon", pci["balloon"]); err != nil {
			return spec, nil, err
		}
	}
	next := PCIFirstDisk
	for i := range spec.Disks {
		key := fmt.Sprintf("disk:%s:%d", spec.Disks[i].Role, spec.Disks[i].Slot)
		addr := spec.Disks[i].PCIAddr
		if addr == "" {
			addr, next = nextFree(used, next)
		}
		if err := claim(key, addr); err != nil {
			return spec, nil, err
		}
		spec.Disks[i].PCIAddr = addr
		pci[key] = addr
	}
	for i := range spec.NICs {
		key := fmt.Sprintf("nic:%d", i)
		addr := spec.NICs[i].PCIAddr
		if addr == "" {
			addr, next = nextFree(used, next)
		}
		if err := claim(key, addr); err != nil {
			return spec, nil, err
		}
		spec.NICs[i].PCIAddr = addr
		pci[key] = addr
	}
	if spec.NoCloud.Enable {
		if _, ok := pci["disk:"+DiskRoleCIDATA+":0"]; !ok {
			addr, n := nextFree(used, next)
			if err := claim("disk:"+DiskRoleCIDATA+":0", addr); err != nil {
				return spec, nil, err
			}
			pci["disk:"+DiskRoleCIDATA+":0"] = addr
			next = n
		}
	}
	if spec.ISOLibraryID != "" {
		if _, ok := pci["scsi"]; !ok {
			addr, n := nextFree(used, next)
			if err := claim("scsi", addr); err != nil {
				return spec, nil, err
			}
			pci["scsi"] = addr
			next = n
		}
	}
	return spec, pci, nil
}

func nextFree(used map[int]string, start int) (string, int) {
	slot := start
	for {
		if _, ok := used[slot]; !ok && slot != 0 && slot != 1 {
			return fmt.Sprintf("0x%x", slot), slot + 1
		}
		slot++
		if slot > 0x1f {
			slot = 2
		}
		if slot == start {
			return "0x1f", slot
		}
	}
}
