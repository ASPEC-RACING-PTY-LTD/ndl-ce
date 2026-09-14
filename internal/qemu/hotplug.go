package qemu

import (
	"fmt"
	"strings"
	"time"

	"github.com/no-dal/ndl-ce/internal/vmspec"
)

// HotplugUSB adds or removes a typed usb-host device over QMP when the VM is running.
func (e *Engine) HotplugUSB(id string, add bool, usb vmspec.LaunchUSB) error {
	if err := vmspec.ValidateWorkloadID(id); err != nil {
		return err
	}
	if _, err := usbHostDevice(usb); err != nil {
		return err
	}
	q, err := e.dialQMP(id, 3*time.Second)
	if err != nil {
		return fmt.Errorf("usb hotplug requires a live QMP session: %w", err)
	}
	defer q.Close()
	usbID := usb.ID
	if usbID == "" {
		usbID = vmspec.USBDeviceID(usb.Address)
	}
	if add {
		_, err = q.exec("device_add", map[string]any{
			"driver":    "usb-host",
			"id":        usbID,
			"vendorid":  "0x" + strings.ToLower(usb.Vendor),
			"productid": "0x" + strings.ToLower(usb.Product),
		})
		return err
	}
	_, err = q.exec("device_del", map[string]any{"id": usbID})
	return err
}
