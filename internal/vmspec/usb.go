package vmspec

import (
	"fmt"
	"strings"
)

// ValidateUSB requires inventory locators, not a QEMU option string.
func ValidateUSB(u USB) error {
	if strings.TrimSpace(u.Address) == "" {
		return fmt.Errorf("usb address is required")
	}
	if strings.ContainsAny(u.Address, ",=\n\r\x00 /") {
		return fmt.Errorf("usb address contains a banned character")
	}
	if err := ValidateUSBHex(u.Vendor); err != nil {
		return fmt.Errorf("vendor: %w", err)
	}
	if err := ValidateUSBHex(u.Product); err != nil {
		return fmt.Errorf("product: %w", err)
	}
	return nil
}

func ValidateUSBHex(v string) error {
	v = strings.ToLower(strings.TrimSpace(v))
	if len(v) != 4 {
		return fmt.Errorf("must be 4 hex digits")
	}
	for _, c := range v {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return fmt.Errorf("must be 4 hex digits")
		}
	}
	return nil
}

func USBDeviceID(address string) string {
	clean := strings.NewReplacer(".", "_", "-", "_", ":", "_").Replace(strings.TrimSpace(address))
	if clean == "" {
		return "usb0"
	}
	return "usb_" + clean
}
