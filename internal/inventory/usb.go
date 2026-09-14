package inventory

import "sort"

func collectUSB(opt Options) []USBDevice {
	fs := opt.fs()
	names := fs.list("sys/bus/usb/devices")
	sort.Strings(names)

	var out []USBDevice
	for _, name := range names {
		base := "sys/bus/usb/devices/" + name
		vendor := fs.readOK(base + "/idVendor")
		if vendor == "" {
			continue
		}
		out = append(out, USBDevice{
			Address: name,
			Vendor:  vendor,
			Product: fs.readOK(base + "/idProduct"),
			Name: firstNonEmpty(
				fs.readOK(base+"/product"),
				fs.readOK(base+"/manufacturer"),
			),
		})
	}
	return out
}
