package lxc

import "time"

// Workload kind stored and returned by the API.
const KindSystemContainer = "system-container"

// Status values are observed, never invented.
const (
	StatusRunning     = "running"
	StatusStopped     = "stopped"
	StatusUnavailable = "unavailable"
	StatusFailed      = "failed"
	StatusChecking    = "checking"
)

const (
	DefaultUIDMap      = "u 0 100000 65536"
	DefaultGIDMap      = "g 0 100000 65536"
	DefaultCPUs        = 1
	DefaultMemoryBytes = 256 << 20
	DefaultRootSize    = 4 << 30
	LastAppliedSchema  = "ndl.workload.last-applied.v1"
	RootfsMarker       = ".ndl-rootfs-ok"
)

// Compile-time privileged binary paths. Never assembled from a shell string.
const (
	BinLXCStart   = "/usr/bin/lxc-start"
	BinLXCStop    = "/usr/bin/lxc-stop"
	BinLXCInfo    = "/usr/bin/lxc-info"
	BinLXCCopy    = "/usr/bin/lxc-copy"
	BinLXCAttach  = "/usr/bin/lxc-attach"
	BinLXCConsole = "/usr/bin/lxc-console"
	BinSystemctl  = "/usr/bin/systemctl"
	BinTar        = "/usr/bin/tar"
	BinCP         = "/usr/bin/cp"
	BinGPGV       = "/usr/bin/gpgv"
)

const (
	defaultDataDir    = "/var/lib/ndl"
	defaultRuntimeLXC = "/var/lib/ndl/runtime/lxc"
	defaultWorkloads  = "/var/lib/ndl/workloads"
	defaultImageCache = "/var/lib/ndl/cache/lxc-images"
	imageIndexPath    = "/streams/v1/images.json"
	DefaultImageBase  = "https://images.linuxcontainers.org"
	CTUnitPrefix      = "nodal-ct@"
)

// Spec is the desired system-container identity plus locators.
// WorkloadID is the UUID identity. Paths, lxc names, and volume refs are locators.
type Spec struct {
	WorkloadID  string   `json:"workload_id"`
	Name        string   `json:"name"`
	ImagePin    string   `json:"image_pin"`
	CPUs        int      `json:"cpus"`
	MemoryBytes int64    `json:"memory_bytes"`
	VolumeID    string   `json:"volume_id"`
	RootfsPath  string   `json:"rootfs_path"`
	NetworkID   string   `json:"network_id"`
	BridgeName  string   `json:"bridge_name"`
	MAC         string   `json:"mac"`
	Privileged  bool     `json:"privileged"`
	UIDMap      string   `json:"uid_map"`
	GIDMap      string   `json:"gid_map"`
	GPUDevices  []string `json:"gpu_devices,omitempty"`
	SkipImage   bool     `json:"skip_image,omitempty"`
	NoStart     bool     `json:"no_start,omitempty"`
}

// Applied is last-applied on disk.
type Applied struct {
	SchemaVersion string    `json:"schema_version"`
	Spec          Spec      `json:"spec"`
	ImageVerified bool      `json:"image_verified"`
	ImageSHA256   string    `json:"image_sha256,omitempty"`
	AppliedAt     time.Time `json:"applied_at"`
}

// Result is returned by create, clone, and lifecycle.
type Result struct {
	WorkloadID    string `json:"workload_id"`
	VolumeID      string `json:"volume_id"`
	RootfsPath    string `json:"rootfs_path"`
	MAC           string `json:"mac"`
	ImageVerified bool   `json:"image_verified"`
	ImageSHA256   string `json:"image_sha256,omitempty"`
	Status        string `json:"status"`
}

// Hint is what the control plane sends when observing known workloads.
type Hint struct {
	WorkloadID string `json:"workload_id"`
	Kind       string `json:"kind"`
	VolumeID   string `json:"volume_id"`
	NetworkID  string `json:"network_id"`
}

// Observed is agent-side workload state. Missing is unavailable, not deleted.
type Observed struct {
	WorkloadID      string    `json:"workload_id"`
	Kind            string    `json:"kind"`
	Status          string    `json:"status"`
	Reason          string    `json:"reason,omitempty"`
	PID             int       `json:"pid"`
	UnitActive      bool      `json:"unit_active"`
	ImageVerified   bool      `json:"image_verified"`
	IPv4            string    `json:"ipv4,omitempty"`
	MAC             string    `json:"mac,omitempty"`
	Warnings        []string  `json:"warnings,omitempty"`
	MigrateReady    bool      `json:"migrate_ready"`
	MigrateBlockers []string  `json:"migrate_blockers,omitempty"`
	ObservedAt      time.Time `json:"observed_at"`
}

// Observation is the agent's workload snapshot.
type Observation struct {
	ObservedAt time.Time  `json:"observed_at"`
	Workloads  []Observed `json:"workloads"`
}

// LifecycleRequest is a typed start/stop/restart/delete/clone.
type LifecycleRequest struct {
	WorkloadID      string `json:"workload_id"`
	Action          string `json:"action"`
	CloneID         string `json:"clone_id,omitempty"`
	CloneVolumeID   string `json:"clone_volume_id,omitempty"`
	CloneRootfsPath string `json:"clone_rootfs_path,omitempty"`
	CloneMAC        string `json:"clone_mac,omitempty"`
	CloneName       string `json:"clone_name,omitempty"`
}

func unitName(id string) string {
	return CTUnitPrefix + id + ".service"
}
