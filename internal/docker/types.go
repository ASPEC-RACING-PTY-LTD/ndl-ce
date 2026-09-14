package docker

import "time"

const (
	HealthHealthy  = "healthy"
	HealthDegraded = "degraded"
	HealthCritical = "critical"
	HealthUnknown  = "unknown"

	KindHost            = "host"
	KindSystemContainer = "system-container"
	HostMachineID       = "host"
	StandaloneProject   = "standalone"

	LabelProject    = "com.docker.compose.project"
	LabelService    = "com.docker.compose.service"
	LabelWorkingDir = "com.docker.compose.project.working_dir"
	LabelConfig     = "com.docker.compose.project.config_files"
	LabelOneoff     = "com.docker.compose.oneoff"
	LabelNumber     = "com.docker.compose.container-number"
)

// MachineHint is a No-DAL workload the agent should probe for Docker.
type MachineHint struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Kind string `json:"kind"`
}

// Inventory is the discovered Docker tree for one node.
type Inventory struct {
	ObservedAt time.Time   `json:"observed_at"`
	Summary    Summary     `json:"summary"`
	Machines   []Machine   `json:"machines"`
	Projects   []Project   `json:"projects"`
	Containers []Container `json:"containers"`
}

// Summary is dashboard counts.
type Summary struct {
	Machines     int `json:"machines"`
	Projects     int `json:"projects"`
	Containers   int `json:"containers"`
	Running      int `json:"running"`
	Stopped      int `json:"stopped"`
	Healthy      int `json:"healthy"`
	Degraded     int `json:"degraded"`
	Critical     int `json:"critical"`
	DaemonsDown  int `json:"daemons_down"`
	UpdateFailed int `json:"update_failed"`
}

// Machine is a Docker engine: the host or a system container.
type Machine struct {
	ID            string    `json:"id"`
	Name          string    `json:"name"`
	Kind          string    `json:"kind"`
	IPv4          string    `json:"ipv4,omitempty"`
	Socket        string    `json:"socket,omitempty"`
	DaemonOK      bool      `json:"daemon_ok"`
	DaemonError   string    `json:"daemon_error,omitempty"`
	DockerVersion string    `json:"docker_version,omitempty"`
	Health        string    `json:"health"`
	HealthReason  string    `json:"health_reason,omitempty"`
	ProjectCount  int       `json:"project_count"`
	ContainerN    int       `json:"container_count"`
	Projects      []Project `json:"projects"`
}

// Project is a Compose project or the synthetic standalone group.
type Project struct {
	ID           string      `json:"id"`
	MachineID    string      `json:"machine_id"`
	MachineName  string      `json:"machine_name"`
	Name         string      `json:"name"`
	WorkingDir   string      `json:"working_dir,omitempty"`
	ConfigFiles  string      `json:"config_files,omitempty"`
	Standalone   bool        `json:"standalone"`
	Health       string      `json:"health"`
	HealthReason string      `json:"health_reason,omitempty"`
	StatusLabel  string      `json:"status_label"`
	Services     int         `json:"service_count"`
	Running      int         `json:"running"`
	Stopped      int         `json:"stopped"`
	UpdateFailed bool        `json:"update_failed"`
	Containers   []Container `json:"containers"`
}

// Container is one engine container with health, metadata, and recent errors.
type Container struct {
	Ref          string            `json:"id"`
	MachineID    string            `json:"machine_id"`
	MachineName  string            `json:"machine_name"`
	MachineKind  string            `json:"machine_kind"`
	MachineIPv4  string            `json:"machine_ip,omitempty"`
	EngineID     string            `json:"container_id"`
	Name         string            `json:"name"`
	ProjectID    string            `json:"project_id"`
	Project      string            `json:"project,omitempty"`
	Service      string            `json:"service,omitempty"`
	WorkingDir   string            `json:"working_dir,omitempty"`
	ConfigFiles  string            `json:"config_files,omitempty"`
	Image        string            `json:"image"`
	ImageID      string            `json:"image_id,omitempty"`
	State        string            `json:"state"`
	Status       string            `json:"status"`
	Health       string            `json:"health"`
	HealthReason string            `json:"health_reason,omitempty"`
	HealthStatus string            `json:"healthcheck,omitempty"`
	StatusLabel  string            `json:"status_label"`
	ExitCode     int               `json:"exit_code,omitempty"`
	OOMKilled    bool              `json:"oom_killed"`
	Restarting   bool              `json:"restarting"`
	Dead         bool              `json:"dead"`
	RestartCount int               `json:"restart_count"`
	RestartLoop  bool              `json:"restart_loop"`
	UpdateFailed bool              `json:"update_failed"`
	UpdateError  string            `json:"update_error,omitempty"`
	Error        string            `json:"error,omitempty"`
	CreatedAt    string            `json:"created_at,omitempty"`
	StartedAt    string            `json:"started_at,omitempty"`
	FinishedAt   string            `json:"finished_at,omitempty"`
	Uptime       string            `json:"uptime,omitempty"`
	Ports        []Port            `json:"ports,omitempty"`
	Mounts       []Mount           `json:"mounts,omitempty"`
	Networks     []string          `json:"networks,omitempty"`
	CPUPercent   *float64          `json:"cpu_percent,omitempty"`
	MemoryBytes  *uint64           `json:"memory_bytes,omitempty"`
	MemoryLimit  *uint64           `json:"memory_limit,omitempty"`
	Pids         *uint64           `json:"pids,omitempty"`
	Labels       map[string]string `json:"labels,omitempty"`
	RecentEvents []Event           `json:"recent_events,omitempty"`
	Problems     []Problem         `json:"problems,omitempty"`
}

// Port is a published or private port binding.
type Port struct {
	IP          string `json:"ip,omitempty"`
	PrivatePort int    `json:"private_port"`
	PublicPort  int    `json:"public_port,omitempty"`
	Type        string `json:"type,omitempty"`
}

// Mount is a volume or bind mount.
type Mount struct {
	Type        string `json:"type"`
	Name        string `json:"name,omitempty"`
	Source      string `json:"source,omitempty"`
	Destination string `json:"destination"`
	RW          bool   `json:"rw"`
	Mode        string `json:"mode,omitempty"`
}

// Event is a Docker engine event or derived operational note.
type Event struct {
	Time    time.Time `json:"time"`
	Type    string    `json:"type"`
	Action  string    `json:"action"`
	Status  string    `json:"status,omitempty"`
	Actor   string    `json:"actor,omitempty"`
	Message string    `json:"message"`
	Severe  bool      `json:"severe"`
}

// Problem is a classified container or engine issue.
type Problem struct {
	Kind    string `json:"kind"`
	Level   string `json:"level"`
	Message string `json:"message"`
}

// ActionRequest is a typed container or project operation.
type ActionRequest struct {
	Action      string `json:"action"`
	MachineID   string `json:"machine_id"`
	ContainerID string `json:"container_id"`
	Tail        int    `json:"tail,omitempty"`
}

// ActionResult is returned by start/stop/restart/pull/recreate/logs.
type ActionResult struct {
	OK      bool   `json:"ok"`
	Message string `json:"message,omitempty"`
	Logs    string `json:"logs,omitempty"`
}

// ExecRequest starts an interactive exec session.
type ExecRequest struct {
	MachineID   string
	ContainerID string
	Cols        uint16
	Rows        uint16
}
