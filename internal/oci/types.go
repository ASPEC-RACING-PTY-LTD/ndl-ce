package oci

import "time"

// KindOCI is the first-class OCI application workload kind.
// Runtime is containerd (documented choice for No-dal CE).
const KindOCI = "oci"

// Status values are observed, never invented.
const (
	StatusRunning       = "running"
	StatusStopped       = "stopped"
	StatusUnavailable   = "unavailable"
	StatusFailed        = "failed"
	StatusCollecting    = "collecting"
	StatusNotConfigured = "not_configured"
)

const (
	DefaultCPUs        = 1
	DefaultMemoryBytes = 256 << 20
	LastAppliedSchema  = "ndl.oci.last-applied.v1"
	OCIUnitPrefix      = "nodal-oci@"
)

// Compile-time privileged binary paths. Never assembled from a shell string.
const (
	BinCTR       = "/usr/bin/ctr"
	BinSystemctl = "/usr/bin/systemctl"
)

const (
	defaultDataDir   = "/var/lib/ndl"
	defaultWorkloads = "/var/lib/ndl/workloads"
	defaultRuntime   = "/var/lib/ndl/runtime/oci"
)

// Port maps a container port to the host.
type Port struct {
	ContainerPort int    `json:"container_port"`
	HostPort      int    `json:"host_port,omitempty"`
	Protocol      string `json:"protocol,omitempty"`
}

// EnvVar is a plain environment binding. Secrets use SecretRef.
type EnvVar struct {
	Name  string `json:"name"`
	Value string `json:"value,omitempty"`
}

// SecretRef binds an env name to a stored secret (never returned in API JSON).
type SecretRef struct {
	Name     string `json:"name"`
	SecretID string `json:"secret_id"`
}

// VolumeMount attaches a No-dal volume UUID. Anonymous volumes and host path / are refused.
type VolumeMount struct {
	VolumeID      string `json:"volume_id"`
	ContainerPath string `json:"container_path"`
	ReadOnly      bool   `json:"read_only,omitempty"`
	// HostPath is rejected by Validate. Kept only so bad clients get a clear error.
	HostPath string `json:"host_path,omitempty"`
}

// Healthcheck is desired probe configuration. Observed health is separate and honest.
type Healthcheck struct {
	HTTPPath string `json:"http_path,omitempty"`
	Port     int    `json:"port,omitempty"`
	Interval int    `json:"interval_seconds,omitempty"`
	Timeout  int    `json:"timeout_seconds,omitempty"`
}

// Resources are soft limits passed to the runtime.
type Resources struct {
	CPUs        int   `json:"cpus,omitempty"`
	MemoryBytes int64 `json:"memory_bytes,omitempty"`
}

// RegistryCreds are pull credentials. Never serialized into last-applied or API JSON.
type RegistryCreds struct {
	Username string
	Password string
}

// Spec is the desired OCI identity plus locators.
// WorkloadID is the UUID identity. Image, registry, and volume refs are locators.
type Spec struct {
	WorkloadID  string            `json:"workload_id"`
	Name        string            `json:"name"`
	ImagePin    string            `json:"image_pin"`
	RegistryID  string            `json:"registry_id,omitempty"`
	RegistryURL string            `json:"registry_url,omitempty"`
	Ports       []Port            `json:"ports,omitempty"`
	Env         []EnvVar          `json:"env,omitempty"`
	SecretRefs  []SecretRef       `json:"secret_refs,omitempty"`
	Volumes     []VolumeMount     `json:"volumes,omitempty"`
	Health      *Healthcheck      `json:"health,omitempty"`
	Resources   Resources         `json:"resources,omitempty"`
	GPUDevices  []string          `json:"gpu_devices,omitempty"`
	Privileged  bool              `json:"privileged,omitempty"`
	NetworkID   string            `json:"network_id,omitempty"`
	BridgeName  string            `json:"bridge_name,omitempty"`
	Command     []string          `json:"command,omitempty"`
	VolumePaths map[string]string `json:"volume_paths,omitempty"` // volume_id -> host path locator
	// Pull credentials are request-scoped. Redact clears them before last-applied.
	PullUsername string `json:"pull_username,omitempty"`
	PullPassword string `json:"pull_password,omitempty"`
}

// Applied is last-applied on disk. Secrets and passwords are never stored here.
type Applied struct {
	SchemaVersion string    `json:"schema_version"`
	Spec          Spec      `json:"spec"`
	ImageDigest   string    `json:"image_digest,omitempty"`
	Pulled        bool      `json:"pulled"`
	AppliedAt     time.Time `json:"applied_at"`
}

// Result is returned by create and lifecycle.
type Result struct {
	WorkloadID  string `json:"workload_id"`
	ImageDigest string `json:"image_digest,omitempty"`
	Status      string `json:"status"`
	Health      Health `json:"health"`
}

// Health is observed probe state. Missing runtime stays unavailable or not_configured.
type Health struct {
	Status  string `json:"status"`
	Message string `json:"message,omitempty"`
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
	WorkloadID string    `json:"workload_id"`
	Kind       string    `json:"kind"`
	Status     string    `json:"status"`
	Reason     string    `json:"reason,omitempty"`
	UnitActive bool      `json:"unit_active"`
	Health     Health    `json:"health"`
	Warnings   []string  `json:"warnings,omitempty"`
	ObservedAt time.Time `json:"observed_at"`
}

// Observation is the agent's OCI workload snapshot.
type Observation struct {
	ObservedAt time.Time  `json:"observed_at"`
	Workloads  []Observed `json:"workloads"`
}

// LifecycleRequest is a typed start/stop/restart/delete.
type LifecycleRequest struct {
	WorkloadID string `json:"workload_id"`
	Action     string `json:"action"`
}

// PullRequest asks the runtime to fetch an image with optional stored creds.
type PullRequest struct {
	Image    string
	Creds    *RegistryCreds
	Insecure bool
}

func unitName(id string) string {
	return OCIUnitPrefix + id + ".service"
}

// UnitName returns the systemd instance for a workload UUID.
func UnitName(id string) string {
	return unitName(id)
}
