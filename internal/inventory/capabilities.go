package inventory

func deriveCapabilities(opt Options, inv Inventory) []Capability {
	fs := opt.fs()
	return []Capability{
		kvmCapability(fs),
		{ID: "iommu", Status: inv.IOMMU.Status},
		gpuCapability(inv),
		hwmonCapability(fs, inv),
		toolCapability(opt, "smart_tool", "smartctl"),
		toolCapability(opt, "nvme_cli", "nvme"),
		virtCapability(inv.CPU),
	}
}

func kvmCapability(fs FS) Capability {
	if fs.exists("dev/kvm") {
		return Capability{ID: "kvm", Status: StatusAvailable}
	}
	return Capability{ID: "kvm", Status: StatusUnavailable}
}

func gpuCapability(inv Inventory) Capability {
	if len(inv.GPUs) > 0 {
		return Capability{ID: "gpu", Status: StatusAvailable}
	}
	return Capability{ID: "gpu", Status: StatusUnavailable}
}

func hwmonCapability(fs FS, inv Inventory) Capability {
	if len(inv.Temperatures) > 0 || fs.exists("sys/class/hwmon") {
		return Capability{ID: "hwmon", Status: StatusAvailable}
	}
	return Capability{ID: "hwmon", Status: StatusUnavailable}
}

func toolCapability(opt Options, id, bin string) Capability {
	if _, err := opt.lookPath(bin); err != nil {
		return Capability{ID: id, Status: StatusNotReported}
	}
	detail := "tool present"
	if id == "smart_tool" {
		detail = "smartctl present; disk health is not implied"
	}
	return Capability{ID: id, Status: StatusAvailable, Detail: detail}
}

func virtCapability(cpu CPU) Capability {
	c := Capability{ID: "virt_extensions"}
	if cpu.VirtCapability == "vmx" || cpu.VirtCapability == "svm" {
		c.Status = StatusAvailable
		c.Detail = cpu.VirtCapability
		return c
	}
	if cpu.Status == StatusUnavailable {
		c.Status = StatusNotReported
		return c
	}
	c.Status = StatusUnavailable
	return c
}
