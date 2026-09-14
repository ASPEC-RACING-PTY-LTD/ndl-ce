package qemu

import (
	"fmt"
	"os"
	"strings"

	"github.com/no-dal/ndl-ce/internal/vmspec"
)

func usbHostDevice(u vmspec.LaunchUSB) (string, error) {
	if err := vmspec.ValidateUSB(vmspec.USB{Address: u.Address, Vendor: u.Vendor, Product: u.Product}); err != nil {
		return "", err
	}
	id := u.ID
	if id == "" {
		id = vmspec.USBDeviceID(u.Address)
	}
	if strings.ContainsAny(id, ",=\n") {
		return "", fmt.Errorf("usb id contains a banned character")
	}
	return fmt.Sprintf("usb-host,id=%s,vendorid=0x%s,productid=0x%s", id, strings.ToLower(u.Vendor), strings.ToLower(u.Product)), nil
}

// MergeUSBs appends add unless that bus address is already present.
func MergeUSBs(current []vmspec.LaunchUSB, add vmspec.LaunchUSB) []vmspec.LaunchUSB {
	out := append([]vmspec.LaunchUSB{}, current...)
	for _, u := range out {
		if u.Address == add.Address {
			return out
		}
	}
	return append(out, add)
}

// DropUSB removes devices with the given bus address from frozen USB host list.
func DropUSB(current []vmspec.LaunchUSB, address string) []vmspec.LaunchUSB {
	kept := make([]vmspec.LaunchUSB, 0, len(current))
	for _, u := range current {
		if u.Address != address {
			kept = append(kept, u)
		}
	}
	return kept
}

// ApplyUSBHost rewrites frozen argv with typed usb-host devices.
func (e *Engine) ApplyUSBHost(id string, usbs []vmspec.LaunchUSB) error {
	launch, err := e.ReadLaunch(id)
	if err != nil {
		return err
	}
	launch.USBs = usbs
	argv, err := e.CompileLaunch(launch)
	if err != nil {
		return err
	}
	return e.writeLaunch(launch, argv)
}

// DetectSecbootFirmware returns allowlisted OVMF secure-boot code, or empty.
func DetectSecbootFirmware() string {
	for _, p := range []string{
		"/usr/share/OVMF/OVMF_CODE_4M.secboot.fd",
		"/usr/share/OVMF/OVMF_CODE.secboot.fd",
	} {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}
