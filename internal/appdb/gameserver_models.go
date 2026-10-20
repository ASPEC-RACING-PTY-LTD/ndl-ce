package appdb

import (
	"encoding/json"
	"time"
)

type GameServer struct {
	ID             string
	ClusterID      string
	WorkloadID     string
	NodeID         string
	OwnerUserID    string
	Name           string
	Notes          string
	TemplateID     string
	TemplateName   string
	Game           string
	Implementation string
	Family         string
	Status         string
	DesiredPower   string
	Image          string
	ImageLabel     string
	Startup        string
	EnvJSON        []byte
	PortsJSON      []byte
	CPUs           int
	MemoryBytes    int64
	DiskBytes      int64
	Capabilities   []byte
	ContainerID    string
	DataDir        string
	InstallPhase   string
	InstallLog     string
	ErrorHuman     string
	ErrorRaw       string
	Pinned         bool
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

type GameTemplate struct {
	ID        string
	ClusterID string
	Body      []byte
	CreatedAt time.Time
	UpdatedAt time.Time
}

type GameSource struct {
	ID        string
	ClusterID string
	Name      string
	URL       string
	Kind      string
	Enabled   bool
	CreatedAt time.Time
}

type GameACL struct {
	ServerID  string
	UserID    string
	Grants    []byte
	CreatedAt time.Time
}

// The game sub-resources below are written to the API as-is, so they carry
// snake_case json tags; host paths and cluster ids stay server side.

type GameEvent struct {
	ID        string    `json:"id"`
	ServerID  string    `json:"server_id"`
	ClusterID string    `json:"-"`
	UserID    string    `json:"user_id,omitempty"`
	Kind      string    `json:"kind"`
	Summary   string    `json:"summary"`
	Detail    string    `json:"detail,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

type GameSchedule struct {
	ID        string    `json:"id"`
	ServerID  string    `json:"server_id"`
	Name      string    `json:"name"`
	Action    string    `json:"action"`
	Cron      string    `json:"cron"`
	Payload   string    `json:"payload,omitempty"`
	Enabled   bool      `json:"enabled"`
	LastRun   time.Time `json:"last_run"`
	CreatedAt time.Time `json:"created_at"`
}

type GameBackup struct {
	ID        string    `json:"id"`
	ServerID  string    `json:"server_id"`
	Name      string    `json:"name"`
	Reason    string    `json:"reason"`
	Path      string    `json:"-"`
	Bytes     int64     `json:"bytes"`
	CreatedAt time.Time `json:"created_at"`
}

type GameContent struct {
	ID        string
	ServerID  string
	Body      []byte
	CreatedAt time.Time
	UpdatedAt time.Time
}

type GameContentProfile struct {
	ID        string    `json:"id"`
	ServerID  string    `json:"server_id"`
	Name      string    `json:"name"`
	Items     []byte    `json:"-"`
	CreatedAt time.Time `json:"created_at"`
}

// MarshalJSON writes Items as the JSON list it holds rather than base64.
func (p GameContentProfile) MarshalJSON() ([]byte, error) {
	type plain GameContentProfile
	items := json.RawMessage(p.Items)
	if !json.Valid(items) {
		items = json.RawMessage("[]")
	}
	return json.Marshal(struct {
		plain
		Items json.RawMessage `json:"items"`
	}{plain(p), items})
}

type GameConsoleFav struct {
	ID       string `json:"id"`
	ServerID string `json:"server_id"`
	Name     string `json:"name"`
	Command  string `json:"command"`
}
