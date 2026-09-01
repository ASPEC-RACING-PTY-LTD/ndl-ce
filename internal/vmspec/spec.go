package vmspec

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"
)

const (
	KindVM            = "vm"
	SchemaDesired     = "ndl.vm.spec.v1"
	SchemaLaunch      = "ndl.vm.launch.v1"
	DefaultMachine    = "pc-q35-10.0"
	DefaultCPUs       = 2
	DefaultMemory     = 2 << 30
	DefaultDiskBytes  = 8 << 30
	FirmwareBIOS      = "bios"
	FirmwareUEFI      = "uefi"
	DiskRoleBoot      = "boot"
	DiskRoleData      = "data"
	DiskRoleCDROM     = "cdrom"
	DiskRoleCIDATA    = "cidata"
	DiskRoleVars      = "uefi-vars"
	NICModelVirtio    = "virtio"
	ApplyLive         = "live"
	ApplyRestart      = "restart"
	ApplyStop         = "stop"
	ApplyUnsupported  = "unsupported"
	GuestAgentChannel = "org.qemu.guest_agent.0"
)

// Spec is user-facing desired VM intent. Paths, TAP names, unit names,
// QMP sockets, and PCI addresses are not identity.
type Spec struct {
	SchemaVersion string   `json:"schema_version"`
	Name          string   `json:"name"`
	CPUs          int      `json:"cpus"`
	MemoryBytes   int64    `json:"memory_bytes"`
	Machine       string   `json:"machine"`
	Firmware      string   `json:"firmware"`
	BootOrder     []string `json:"boot_order,omitempty"`
	Disks         []Disk   `json:"disks"`
	NICs          []NIC    `json:"nics"`
	ISOLibraryID  string   `json:"iso_library_id,omitempty"`
	CloudImageID  string   `json:"cloud_image_id,omitempty"`
	NoCloud       NoCloud  `json:"nocloud"`
	Autostart     bool     `json:"autostart"`
	Console       Console  `json:"console"`
	Balloon       bool     `json:"balloon"`
}

// Disk is a volume attachment by UUID. Path is never product identity.
type Disk struct {
	VolumeID  string `json:"volume_id,omitempty"`
	Role      string `json:"role"`
	Slot      int    `json:"slot"`
	Format    string `json:"format,omitempty"`
	ReadOnly  bool   `json:"read_only,omitempty"`
	SizeBytes int64  `json:"size_bytes,omitempty"`
	PCIAddr   string `json:"pci_addr,omitempty"`
}

// NIC is a network attachment. MAC is allocated once and persisted.
type NIC struct {
	ID        string `json:"id,omitempty"`
	NetworkID string `json:"network_id"`
	MAC       string `json:"mac,omitempty"`
	Model     string `json:"model,omitempty"`
	PCIAddr   string `json:"pci_addr,omitempty"`
}

// NoCloud is structured cloud-init plus optional raw user-data.
type NoCloud struct {
	Enable            bool     `json:"enable"`
	Hostname          string   `json:"hostname,omitempty"`
	Username          string   `json:"username,omitempty"`
	SSHAuthorizedKeys []string `json:"ssh_authorized_keys,omitempty"`
	Password          string   `json:"password,omitempty"`
	UserData          string   `json:"user_data,omitempty"`
	NetworkConfig     string   `json:"network_config,omitempty"`
	HasPassword       bool     `json:"has_password,omitempty"`
}

// Console is the compatibility console. qemu-ga is always attached separately.
type Console struct {
	Serial bool `json:"serial"`
	VNC    bool `json:"vnc"`
}

// Resolved locators are backend facts, not desired identity.
type Resolved struct {
	Disks          []ResolvedDisk
	NICs           []ResolvedNIC
	ISOPath        string
	CloudImagePath string
	CloudImageFmt  string
	FirmwareCode   string
	FirmwareVarsIn string
	Accel          string
}

// ResolvedDisk is a VolumeHandle locator for one disk.
type ResolvedDisk struct {
	VolumeID string
	Role     string
	Slot     int
	Path     string
	Format   string
	ReadOnly bool
	PCIAddr  string
}

// ResolvedNIC is a network object locator for one NIC.
type ResolvedNIC struct {
	ID         string
	NetworkID  string
	BridgeName string
	MAC        string
	Model      string
	PCIAddr    string
	TAPName    string
}

// Launch is the frozen runtime configuration. Argv is filled by the QEMU compiler.
type Launch struct {
	SchemaVersion string            `json:"schema_version"`
	WorkloadID    string            `json:"workload_id"`
	Machine       string            `json:"machine"`
	Accel         string            `json:"accel"`
	CPUs          int               `json:"cpus"`
	MemoryMiB     int64             `json:"memory_mib"`
	Firmware      Firmware          `json:"firmware"`
	Disks         []LaunchDisk      `json:"disks"`
	NICs          []LaunchNIC       `json:"nics"`
	PCI           map[string]string `json:"pci"`
	Autostart     bool              `json:"autostart"`
	Console       LaunchConsole     `json:"console"`
	NoCloud       *LaunchNoCloud    `json:"nocloud,omitempty"`
	Balloon       bool              `json:"balloon"`
	BootOrder     string            `json:"boot_order"`
	QGA           bool              `json:"qemu_ga"`
}

// Firmware is the resolved firmware mode and locators.
type Firmware struct {
	Mode     string `json:"mode"`
	CodePath string `json:"code_path,omitempty"`
	VarsPath string `json:"vars_path,omitempty"`
}

// LaunchDisk is a compiled disk with a stable node-name and PCI address.
type LaunchDisk struct {
	VolumeID string `json:"volume_id,omitempty"`
	Role     string `json:"role"`
	Slot     int    `json:"slot"`
	Path     string `json:"path"`
	Format   string `json:"format"`
	ReadOnly bool   `json:"read_only"`
	PCIAddr  string `json:"pci_addr,omitempty"`
	NodeName string `json:"node_name"`
}

// LaunchNIC is a compiled NIC with persisted MAC and derived TAP name.
type LaunchNIC struct {
	ID         string `json:"id,omitempty"`
	NetworkID  string `json:"network_id"`
	BridgeName string `json:"bridge_name"`
	TAPName    string `json:"tap_name"`
	MAC        string `json:"mac"`
	Model      string `json:"model"`
	PCIAddr    string `json:"pci_addr"`
}

// LaunchConsole names unix socket backends. Paths are locators, not tickets.
type LaunchConsole struct {
	Serial bool `json:"serial"`
	VNC    bool `json:"vnc"`
}

// LaunchNoCloud records cidata state without secrets.
type LaunchNoCloud struct {
	Enable        bool   `json:"enable"`
	Hostname      string `json:"hostname,omitempty"`
	Username      string `json:"username,omitempty"`
	ImagePath     string `json:"image_path"`
	HasPassword   bool   `json:"has_password"`
	UserDataSHA   string `json:"user_data_sha,omitempty"`
	NetworkConfig string `json:"network_config,omitempty"`
}

// ApplyClass describes whether a spec change can happen live.
type ApplyClass struct {
	Field  string `json:"field"`
	Apply  string `json:"apply"`
	Reason string `json:"reason"`
}

func Normalize(spec Spec) Spec {
	spec.SchemaVersion = SchemaDesired
	spec.Name = strings.TrimSpace(spec.Name)
	spec.Firmware = strings.ToLower(strings.TrimSpace(spec.Firmware))
	if spec.Firmware == "" {
		spec.Firmware = FirmwareBIOS
	}
	if spec.Machine == "" {
		spec.Machine = DefaultMachine
	}
	if spec.CPUs < 1 {
		spec.CPUs = DefaultCPUs
	}
	if spec.MemoryBytes < 64<<20 {
		spec.MemoryBytes = DefaultMemory
	}
	if spec.Console == (Console{}) {
		spec.Console = Console{Serial: true, VNC: true}
	}
	if len(spec.BootOrder) == 0 {
		if strings.TrimSpace(spec.ISOLibraryID) != "" {
			spec.BootOrder = []string{"cdrom", "disk"}
		} else {
			spec.BootOrder = []string{"disk"}
		}
	}
	for i := range spec.NICs {
		if spec.NICs[i].Model == "" {
			spec.NICs[i].Model = NICModelVirtio
		}
	}
	hasBoot := false
	for _, d := range spec.Disks {
		if d.Role == DiskRoleBoot {
			hasBoot = true
			break
		}
	}
	if !hasBoot {
		spec.Disks = append([]Disk{{Role: DiskRoleBoot, Slot: 0, Format: "qcow2", SizeBytes: DefaultDiskBytes}}, spec.Disks...)
	}
	if spec.NoCloud.Username == "" && spec.NoCloud.Enable {
		spec.NoCloud.Username = "debian"
	}
	if spec.NoCloud.Hostname == "" && spec.Name != "" && spec.NoCloud.Enable {
		spec.NoCloud.Hostname = spec.Name
	}
	if spec.NoCloud.Password != "" {
		spec.NoCloud.HasPassword = true
	}
	return spec
}

// Redact removes secrets from API responses and logs.
func Redact(spec Spec) Spec {
	out := spec
	out.NoCloud.Password = ""
	if out.NoCloud.HasPassword && strings.Contains(strings.ToLower(out.NoCloud.UserData), "password") {
		out.NoCloud.UserData = ""
	}
	return out
}

func Parse(raw json.RawMessage) (Spec, error) {
	var spec Spec
	if len(raw) == 0 || string(raw) == "null" || string(raw) == "{}" {
		return Normalize(Spec{}), nil
	}
	if err := json.Unmarshal(raw, &spec); err != nil {
		return Spec{}, fmt.Errorf("vm spec is not valid JSON")
	}
	return Normalize(spec), nil
}

func MustJSON(spec Spec) json.RawMessage {
	b, err := json.Marshal(Redact(spec))
	if err != nil {
		return json.RawMessage(`{}`)
	}
	return b
}

func SHA256String(s string) string {
	sum := sha256.Sum256([]byte(s))
	return fmt.Sprintf("%x", sum[:])
}
