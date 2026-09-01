package qemu

import (
	"errors"
	"time"

	"github.com/no-dal/ndl-ce/internal/vmspec"
)

// ErrAlreadyRunning means nodal-vm@id is already active or activating.
// Prepare must not rewrite a live ABI. Start is idempotent.
var ErrAlreadyRunning = errors.New("qemu unit is already active")

const (
	KindVM            = "vm"
	LastAppliedSchema = "ndl.qemu.last-applied.v1"
	BinQEMU           = "/usr/bin/qemu-system-x86_64"
	BinSystemctl      = "/usr/bin/systemctl"
	LaunchBin         = "/usr/sbin/ndl-qemu-launch"
	DefaultMachine    = "pc-q35-10.0"
	DefaultMemory     = 128 << 20
	QEMUUser          = "ndl-qemu"
	GuestAgentName    = "org.qemu.guest_agent.0"
)

const (
	StatusRunning     = "running"
	StatusStopped     = "stopped"
	StatusFailed      = "failed"
	StatusUnavailable = "unavailable"
	StatusStarting    = "starting"
	StatusStopping    = "stopping"
	StatusCrashed     = "crashed"
)

// Spec is the frozen prototype VM description.
type Spec struct {
	WorkloadID    string `json:"workload_id"`
	VolumeID      string `json:"volume_id"`
	DiskPath      string `json:"disk_path"`
	DiskFormat    string `json:"disk_format"`
	MemoryBytes   int64  `json:"memory_bytes"`
	CPUs          int    `json:"cpus"`
	Machine       string `json:"machine"`
	Accel         string `json:"accel"`
	Autostart     bool   `json:"autostart"`
	PCIDiskAddr   string `json:"pci_disk_addr"`
	PCISerialAddr string `json:"pci_serial_addr"`
}

// ArgvFile is the frozen argv artifact read by ndl-qemu-launch.
type ArgvFile struct {
	WorkloadID string        `json:"workload_id"`
	Argv       []string      `json:"argv"`
	Launch     vmspec.Launch `json:"launch,omitempty"`
}

// Applied is last-applied host-native state.
type Applied struct {
	SchemaVersion string        `json:"schema_version"`
	Spec          Spec          `json:"spec,omitempty"`
	Launch        vmspec.Launch `json:"launch,omitempty"`
	Argv          []string      `json:"argv"`
	AppliedAt     time.Time     `json:"applied_at"`
}

// Observed is honest runtime state.
type Observed struct {
	WorkloadID   string            `json:"workload_id"`
	Status       string            `json:"status"`
	Reason       string            `json:"reason"`
	UnitActive   bool              `json:"unit_active"`
	PID          *int              `json:"pid"`
	Machine      string            `json:"machine"`
	Accel        string            `json:"accel"`
	QMP          string            `json:"qmp"`
	SerialSocket string            `json:"serial_socket"`
	VNCSocket    string            `json:"vnc_socket"`
	QGASocket    string            `json:"qga_socket"`
	RunningAs    string            `json:"running_as"`
	PCI          map[string]string `json:"pci,omitempty"`
	PCILiveMatch *bool             `json:"pci_live_match,omitempty"`
	AccelHonest  string            `json:"accel_honest,omitempty"`
}

// Result is a typed execute outcome.
type Result struct {
	WorkloadID string   `json:"workload_id"`
	Status     string   `json:"status"`
	Machine    string   `json:"machine"`
	Accel      string   `json:"accel"`
	Reason     string   `json:"reason,omitempty"`
	UnitActive bool     `json:"unit_active,omitempty"`
	RunningAs  string   `json:"running_as,omitempty"`
	Argv       []string `json:"argv,omitempty"`
}

func resultFromObserved(obs Observed) Result {
	return Result{
		WorkloadID: obs.WorkloadID,
		Status:     obs.Status,
		Machine:    obs.Machine,
		Accel:      obs.Accel,
		Reason:     obs.Reason,
		UnitActive: obs.UnitActive,
		RunningAs:  obs.RunningAs,
	}
}

// Discovery is last-applied identity plus honest unit state.
// Identity comes from qemu-last-applied.json, not from a live process name.
type Discovery struct {
	WorkloadID string  `json:"workload_id"`
	VolumeID   string  `json:"volume_id"`
	DiskPath   string  `json:"disk_path"`
	Applied    Applied `json:"applied"`
	UnitState  string  `json:"unit_state"`
	UnitActive bool    `json:"unit_active"`
	Status     string  `json:"status"`
	Reason     string  `json:"reason,omitempty"`
}

func unitName(id string) string {
	return "nodal-vm@" + id + ".service"
}
