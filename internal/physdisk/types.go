package physdisk

import "strings"

// Partition is observed partition metadata. It is never modified.
type Partition struct {
	Kernel     string `json:"kernel,omitempty"`
	Number     int    `json:"number,omitempty"`
	SizeBytes  uint64 `json:"size_bytes,omitempty"`
	FSType     string `json:"fs_type,omitempty"`
	Label      string `json:"label,omitempty"`
	PartType   string `json:"part_type,omitempty"`
	MountPoint string `json:"mount_point,omitempty"`
	Swap       bool   `json:"swap,omitempty"`
}

// Device is one observed whole disk plus eligibility.
type Device struct {
	ID              DeviceID    `json:"id"`
	ByIDPath        string      `json:"by_id_path"`
	KernelName      string      `json:"kernel_name,omitempty"`
	KernelPath      string      `json:"kernel_path,omitempty"`
	Model           string      `json:"model,omitempty"`
	Vendor          string      `json:"vendor,omitempty"`
	Serial          string      `json:"serial,omitempty"`
	SizeBytes       uint64      `json:"size_bytes,omitempty"`
	Transport       string      `json:"transport,omitempty"`
	Rotational      *bool       `json:"rotational,omitempty"`
	Removable       *bool       `json:"removable,omitempty"`
	Partitions      []Partition `json:"partitions,omitempty"`
	FSSignatures    []string    `json:"fs_signatures,omitempty"`
	Mounted         []string    `json:"mounted,omitempty"`
	Swap            bool        `json:"swap,omitempty"`
	HostDisk        bool        `json:"host_disk"`
	PoolKind        string      `json:"pool_kind,omitempty"`
	PoolName        string      `json:"pool_name,omitempty"`
	AssignedID      string      `json:"assigned_workload_id,omitempty"`
	AssignedName    string      `json:"assigned_workload_name,omitempty"`
	AssignedRunning bool        `json:"assigned_running,omitempty"`
	ExistingData    bool        `json:"existing_data"`
	Eligible        bool        `json:"eligible"`
	Reasons         []string    `json:"reasons,omitempty"`
}

// DisplayName is the human label used in UI, never a kernel path.
func (d Device) DisplayName() string {
	if strings.TrimSpace(d.Model) != "" {
		return strings.TrimSpace(d.Model)
	}
	if d.ID != "" {
		return string(d.ID)
	}
	return d.KernelName
}

// Reason codes are stable and user-facing.
const (
	ReasonHostRoot     = "Protected: contains the host root filesystem"
	ReasonHostBoot     = "Protected: contains host /boot or EFI"
	ReasonMounted      = "Mounted on the host"
	ReasonSwap         = "Active swap device"
	ReasonZFS          = "In use: ZFS pool member"
	ReasonLVM          = "In use: LVM physical volume"
	ReasonPool         = "In use: member of an NDL storage pool"
	ReasonAssigned     = "In use: assigned to another VM"
	ReasonAssignedSelf = "Already assigned to this VM"
	ReasonMissing      = "The device could not be resolved. It may have been removed"
	ReasonNoID         = "No stable /dev/disk/by-id name is available"
	ReasonRemovable    = "Removable media is not eligible for exclusive passthrough"
	ReasonVirtual      = "Virtual or ramdisk devices cannot be passed through"
)

// Assignment is persistent exclusive ownership of a physical disk by one VM.
type Assignment struct {
	ID           string
	ClusterID    string
	WorkloadID   string
	WorkloadName string
	DeviceID     DeviceID
	ByIDPath     string
	KernelName   string
	Model        string
	Serial       string
	SizeBytes    int64
	Role         string
	Slot         int
	Bus          string
	Running      bool
}

// HostHints are observed host facts used for eligibility. Tests inject them.
type HostHints struct {
	RootKernel     string
	BootKernels    []string
	Mounts         map[string]string // kernel name -> mountpoint
	Swaps          []string          // kernel names
	ZFSMembers     []string          // kernel or by-id
	LVMPVs         []string
	PoolDisks      []PoolDisk
	Assignments    []Assignment
	HolderKinds    map[string]string // kernel name -> zfs|lvm|md
	Udev           map[string]UdevInfo
	SelfWorkloadID string // owning VM: assignment is not a block
}

// PoolDisk is an NDL storage-pool member disk.
type PoolDisk struct {
	Kind string
	Name string
	Disk string
}

// UdevInfo is non-destructive udev identity for a block node.
type UdevInfo struct {
	FSType   string
	FSLabel  string
	PartType string
	Usage    string
}
