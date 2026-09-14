package gameserver

import "time"

const (
	StatusPending    = "pending"
	StatusInstalling = "installing"
	StatusReady      = "ready"
	StatusStarting   = "starting"
	StatusRunning    = "running"
	StatusStopping   = "stopping"
	StatusStopped    = "stopped"
	StatusFailed     = "failed"
	StatusOffline    = "offline"
	KindGameServer   = "game-server"
)

// Server is a game server instance owned by a cluster.
type Server struct {
	ID             string            `json:"id"`
	ClusterID      string            `json:"cluster_id"`
	WorkloadID     string            `json:"workload_id,omitempty"`
	NodeID         string            `json:"node_id,omitempty"`
	OwnerUserID    string            `json:"owner_user_id"`
	Name           string            `json:"name"`
	Notes          string            `json:"notes,omitempty"`
	TemplateID     string            `json:"template_id"`
	TemplateName   string            `json:"template_name"`
	Game           string            `json:"game"`
	Implementation string            `json:"implementation"`
	Family         string            `json:"family"`
	Status         string            `json:"status"`
	DesiredPower   string            `json:"desired_power"`
	Image          string            `json:"image"`
	ImageLabel     string            `json:"image_label,omitempty"`
	Startup        string            `json:"startup"`
	Env            map[string]string `json:"env"`
	Ports          []Port            `json:"ports"`
	CPUs           int               `json:"cpus"`
	MemoryBytes    int64             `json:"memory_bytes"`
	DiskBytes      int64             `json:"disk_bytes"`
	Capabilities   []string          `json:"capabilities"`
	ContainerID    string            `json:"container_id,omitempty"`
	DataDir        string            `json:"data_dir,omitempty"`
	InstallPhase   string            `json:"install_phase,omitempty"`
	InstallLog     string            `json:"install_log,omitempty"`
	ErrorHuman     string            `json:"error_human,omitempty"`
	ErrorRaw       string            `json:"error_raw,omitempty"`
	Players        *int              `json:"players,omitempty"`
	PlayersMax     *int              `json:"players_max,omitempty"`
	UptimeSeconds  int64             `json:"uptime_seconds,omitempty"`
	Pinned         bool              `json:"pinned,omitempty"`
	CreatedAt      time.Time         `json:"created_at"`
	UpdatedAt      time.Time         `json:"updated_at"`
}

// ACL grants a user a subset of capabilities on one server.
type ACL struct {
	ServerID  string    `json:"server_id"`
	UserID    string    `json:"user_id"`
	Username  string    `json:"username,omitempty"`
	Grants    []string  `json:"grants"`
	CreatedAt time.Time `json:"created_at"`
}

// Event is an activity/timeline row.
type Event struct {
	ID        string    `json:"id"`
	ServerID  string    `json:"server_id"`
	ClusterID string    `json:"cluster_id"`
	UserID    string    `json:"user_id,omitempty"`
	Kind      string    `json:"kind"`
	Summary   string    `json:"summary"`
	Detail    string    `json:"detail,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

// Schedule is a stored recurring action.
type Schedule struct {
	ID        string    `json:"id"`
	ServerID  string    `json:"server_id"`
	Name      string    `json:"name"`
	Action    string    `json:"action"`
	Cron      string    `json:"cron"`
	Payload   string    `json:"payload,omitempty"`
	Enabled   bool      `json:"enabled"`
	LastRun   time.Time `json:"last_run,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

// Backup is a recovery point of server files plus metadata.
type Backup struct {
	ID        string    `json:"id"`
	ServerID  string    `json:"server_id"`
	Name      string    `json:"name"`
	Reason    string    `json:"reason,omitempty"`
	Path      string    `json:"path,omitempty"`
	Bytes     int64     `json:"bytes"`
	CreatedAt time.Time `json:"created_at"`
}

// ContentItem is an installed or catalogue content row.
type ContentItem struct {
	ID           string    `json:"id"`
	ServerID     string    `json:"server_id,omitempty"`
	Provider     string    `json:"provider"`
	ExternalID   string    `json:"external_id,omitempty"`
	Name         string    `json:"name"`
	Slug         string    `json:"slug,omitempty"`
	Version      string    `json:"version,omitempty"`
	GameVersions []string  `json:"game_versions,omitempty"`
	Summary      string    `json:"summary,omitempty"`
	Enabled      bool      `json:"enabled"`
	Filename     string    `json:"filename,omitempty"`
	Dependencies []string  `json:"dependencies,omitempty"`
	Incompatible bool      `json:"incompatible,omitempty"`
	SourceURL    string    `json:"source_url,omitempty"`
	InstalledAt  time.Time `json:"installed_at,omitempty"`
}

// ContentProfile is a named set of content IDs.
type ContentProfile struct {
	ID        string    `json:"id"`
	ServerID  string    `json:"server_id"`
	Name      string    `json:"name"`
	Items     []string  `json:"items"`
	CreatedAt time.Time `json:"created_at"`
}

// ConsoleFavorite is a named command.
type ConsoleFavorite struct {
	ID       string `json:"id"`
	ServerID string `json:"server_id"`
	Name     string `json:"name"`
	Command  string `json:"command"`
}

// Source is a catalogue repository.
type Source struct {
	ID        string    `json:"id"`
	ClusterID string    `json:"cluster_id"`
	Name      string    `json:"name"`
	URL       string    `json:"url"`
	Kind      string    `json:"kind"`
	Enabled   bool      `json:"enabled"`
	CreatedAt time.Time `json:"created_at"`
}

// StoredTemplate is a cluster-local normalized template.
type StoredTemplate struct {
	Template
	ClusterID string    `json:"cluster_id"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}
