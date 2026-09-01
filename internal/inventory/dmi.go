package inventory

func collectFirmware(opt Options) Firmware {
	fs := opt.fs()
	base := "sys/class/dmi/id"
	if !fs.exists(base) {
		return Firmware{Status: StatusUnavailable}
	}
	fw := Firmware{
		Status:        StatusAvailable,
		SysVendor:     fs.readOK(base + "/sys_vendor"),
		Product:       fs.readOK(base + "/product_name"),
		BoardVendor:   fs.readOK(base + "/board_vendor"),
		Board:         fs.readOK(base + "/board_name"),
		BIOSVendor:    fs.readOK(base + "/bios_vendor"),
		BIOSVersion:   fs.readOK(base + "/bios_version"),
		BIOSDate:      fs.readOK(base + "/bios_date"),
		ProductSerial: fs.readOK(base + "/product_serial"),
	}
	if fw.SysVendor == "" && fw.Product == "" && fw.BIOSVendor == "" && fw.BIOSVersion == "" {
		fw.Status = StatusNotReported
	}
	return fw
}
