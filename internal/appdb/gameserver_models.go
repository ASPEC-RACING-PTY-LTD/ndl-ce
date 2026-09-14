package appdb

import "time"

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

type GameEvent struct {
	ID        string
	ServerID  string
	ClusterID string
	UserID    string
	Kind      string
	Summary   string
	Detail    string
	CreatedAt time.Time
}

type GameSchedule struct {
	ID        string
	ServerID  string
	Name      string
	Action    string
	Cron      string
	Payload   string
	Enabled   bool
	LastRun   time.Time
	CreatedAt time.Time
}

type GameBackup struct {
	ID        string
	ServerID  string
	Name      string
	Reason    string
	Path      string
	Bytes     int64
	CreatedAt time.Time
}

type GameContent struct {
	ID         string
	ServerID   string
	Body       []byte
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

type GameContentProfile struct {
	ID        string
	ServerID  string
	Name      string
	Items     []byte
	CreatedAt time.Time
}

type GameConsoleFav struct {
	ID       string
	ServerID string
	Name     string
	Command  string
}
