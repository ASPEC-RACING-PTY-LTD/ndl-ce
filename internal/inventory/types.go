package inventory

import "time"

// SchemaVersion is the inventory JSON schema written on the wire.
const SchemaVersion = "ndl.inventory.v1"

// Status is an honest availability value. Never invent a healthy reading.
type Status string

const (
	StatusAvailable   Status = "available"
	StatusUnavailable Status = "unavailable"
	StatusNotReported Status = "not_reported"
	StatusCollecting  Status = "collecting"
	StatusStale       Status = "stale"
)

// Inventory is observed host hardware. It is not desired state.
type Inventory struct {
	SchemaVersion string        `json:"schema_version"`
	ObservedAt    time.Time     `json:"observed_at"`
	Stale         bool          `json:"stale"`
	Host          Host          `json:"host"`
	CPU           CPU           `json:"cpu"`
	Memory        Memory        `json:"memory"`
	BlockDevices  []BlockDevice `json:"block_devices"`
	NICs          []NIC         `json:"nics"`
	PCI           []PCIDevice   `json:"pci"`
	USB           []USBDevice   `json:"usb"`
	GPUs          []GPU         `json:"gpus"`
	IOMMU         IOMMU         `json:"iommu"`
	Temperatures  []Sensor      `json:"temperatures"`
	Firmware      Firmware      `json:"firmware"`
	Capabilities  []Capability  `json:"capabilities"`
}

// Host is the detected No-dal host OS, not a guest or OCI image.
type Host struct {
	Status       Status   `json:"status"`
	ID           string   `json:"id,omitempty"`
	VersionID    string   `json:"version_id,omitempty"`
	Family       string   `json:"family,omitempty"`
	Architecture string   `json:"architecture,omitempty"`
	PrettyName   string   `json:"pretty_name,omitempty"`
	SupportTier  string   `json:"support_tier,omitempty"`
	Capabilities []string `json:"host_capabilities,omitempty"`
	Note         string   `json:"note,omitempty"`
}

// CPU is processor topology from sysfs and cpuinfo.
type CPU struct {
	Status         Status   `json:"status"`
	Vendor         string   `json:"vendor,omitempty"`
	Model          string   `json:"model,omitempty"`
	Architecture   string   `json:"architecture,omitempty"`
	Sockets        int      `json:"sockets,omitempty"`
	Cores          int      `json:"cores,omitempty"`
	Threads        int      `json:"threads,omitempty"`
	Online         int      `json:"online,omitempty"`
	VirtCapability string   `json:"virt_capability,omitempty"`
	MaxMHz         *float64 `json:"max_mhz,omitempty"`
	Note           string   `json:"note,omitempty"`
}

// Memory is RAM size and optional DIMM topology.
type Memory struct {
	Status         Status  `json:"status"`
	TotalBytes     uint64  `json:"total_bytes,omitempty"`
	AvailableBytes *uint64 `json:"available_bytes,omitempty"`
	UsedBytes      *uint64 `json:"used_bytes,omitempty"`
	DIMMs          []DIMM  `json:"dimms,omitempty"`
	DIMMStatus     Status  `json:"dimm_status"`
	Note           string  `json:"note,omitempty"`
}

// DIMM is a memory module when DMI memory devices are present.
type DIMM struct {
	Locator string `json:"locator,omitempty"`
	Size    string `json:"size,omitempty"`
	Type    string `json:"type,omitempty"`
	Speed   string `json:"speed,omitempty"`
}

// BlockDevice is a kernel block device. Partitions are omitted.
type BlockDevice struct {
	Name          string `json:"name"`
	Kernel        string `json:"kernel,omitempty"`
	Model         string `json:"model,omitempty"`
	Vendor        string `json:"vendor,omitempty"`
	Serial        string `json:"serial,omitempty"`
	SizeBytes     uint64 `json:"size_bytes,omitempty"`
	Rotational    *bool  `json:"rotational,omitempty"`
	Removable     *bool  `json:"removable,omitempty"`
	Transport     string `json:"transport,omitempty"`
	Kind          string `json:"kind,omitempty"`
	LogicalBlock  uint64 `json:"logical_block_bytes,omitempty"`
	PhysicalBlock uint64 `json:"physical_block_bytes,omitempty"`
	MountHint     string `json:"mount_hint,omitempty"`
	SMARTStatus   Status `json:"smart_status,omitempty"`
}

// NIC is an observed network interface. No configuration is applied.
type NIC struct {
	Name        string   `json:"name"`
	IfIndex     int      `json:"ifindex,omitempty"`
	MAC         string   `json:"mac,omitempty"`
	MTU         int      `json:"mtu,omitempty"`
	State       string   `json:"state,omitempty"`
	SpeedMbps   *int     `json:"speed_mbps,omitempty"`
	Driver      string   `json:"driver,omitempty"`
	PCI         string   `json:"pci,omitempty"`
	Kind        string   `json:"kind,omitempty"`
	Addresses   []string `json:"addresses,omitempty"`
	AddressNote string   `json:"address_note,omitempty"`
}

// PCIDevice is a sysfs PCI function.
type PCIDevice struct {
	Address    string `json:"address"`
	Vendor     string `json:"vendor,omitempty"`
	Device     string `json:"device,omitempty"`
	Class      string `json:"class,omitempty"`
	Driver     string `json:"driver,omitempty"`
	IOMMUGroup string `json:"iommu_group,omitempty"`
}

// USBDevice is a sysfs USB device.
type USBDevice struct {
	Address string `json:"address"`
	Vendor  string `json:"vendor,omitempty"`
	Product string `json:"product,omitempty"`
	Name    string `json:"name,omitempty"`
}

// GPU is display or 3D PCI hardware. Discovery only.
type GPU struct {
	ID         string `json:"id"`
	Vendor     string `json:"vendor,omitempty"`
	Model      string `json:"model,omitempty"`
	PCI        string `json:"pci,omitempty"`
	Driver     string `json:"driver,omitempty"`
	IOMMUGroup string `json:"iommu_group,omitempty"`
	Hint       string `json:"hint,omitempty"`
}

// IOMMU is group membership. No ACS or bind changes.
type IOMMU struct {
	Status Status       `json:"status"`
	Groups []IOMMUGroup `json:"groups,omitempty"`
	Note   string       `json:"note,omitempty"`
}

// IOMMUGroup lists PCI functions in one group.
type IOMMUGroup struct {
	ID      string   `json:"id"`
	Devices []string `json:"devices,omitempty"`
}

// Sensor is one hwmon temperature reading in millidegrees Celsius.
type Sensor struct {
	ID     string `json:"id"`
	Name   string `json:"name,omitempty"`
	Label  string `json:"label,omitempty"`
	MilliC *int64 `json:"milli_c,omitempty"`
	Status Status `json:"status"`
}

// Firmware is DMI/SMBIOS identity. Serials are filled only when present.
type Firmware struct {
	Status        Status `json:"status"`
	SysVendor     string `json:"sys_vendor,omitempty"`
	Product       string `json:"product,omitempty"`
	BoardVendor   string `json:"board_vendor,omitempty"`
	Board         string `json:"board,omitempty"`
	BIOSVendor    string `json:"bios_vendor,omitempty"`
	BIOSVersion   string `json:"bios_version,omitempty"`
	BIOSDate      string `json:"bios_date,omitempty"`
	ProductSerial string `json:"product_serial,omitempty"`
	Note          string `json:"note,omitempty"`
}

// Capability is an observed hardware or tooling flag, not a No-dal feature.
type Capability struct {
	ID     string `json:"id"`
	Status Status `json:"status"`
	Detail string `json:"detail,omitempty"`
}

// RedactForViewer removes administrative serials and unique firmware IDs.
func RedactForViewer(in Inventory) Inventory {
	out := in
	out.Firmware.ProductSerial = ""
	blocks := make([]BlockDevice, len(in.BlockDevices))
	copy(blocks, in.BlockDevices)
	for i := range blocks {
		blocks[i].Serial = ""
	}
	out.BlockDevices = blocks
	return out
}
