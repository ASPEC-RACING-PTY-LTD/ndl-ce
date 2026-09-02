package migration

import "time"

const (
	ManifestSchema = "ndl.migration.manifest.v1"

	KindVM        = "vm"
	KindContainer = "system-container"

	AdapterNodal   = "nodal"
	AdapterProxmox = "proxmox"
	AdapterLibvirt = "libvirt"
	AdapterDisk    = "disk"
	AdapterOVF     = "ovf"
	AdapterBackup  = "backup"

	ModeOffline  = "offline"
	ModeSnapshot = "snapshot"
	ModeLive     = "live"
	ModeBackup   = "backup"
	ModeDisk     = "disk"

	ConsistencySafe    = "SAFE"
	ConsistencyLowRisk = "LOW RISK"
	ConsistencyRisky   = "RISKY"
	ConsistencyDepends = "SOURCE SAFE"

	SourceProtected = "PROTECTED"

	CompatReady           = "READY"
	CompatWarning         = "WARNING"
	CompatRequiresMapping = "REQUIRES MAPPING"
	CompatUnsupported     = "UNSUPPORTED"
	CompatBlocked         = "BLOCKED"

	ExportDirect    = "DIRECT EXPORT"
	ExportPackage   = "COMPATIBLE EXPORT PACKAGE"
	ExportBundle    = "nodal-bundle"
	ExportVMImage   = "vm-image"
	ExportCTArchive = "container-archive"
	ExportOVF       = "ovf"

	VerifyTransfer = "transfer_complete"
	VerifyConfig   = "configuration_verified"
	VerifyBoot     = "boot_verified"
	VerifyGuest    = "guest_reachable"
	VerifyApp      = "application_verified"

	StagingRoot = "/var/lib/ndl/migration"

	TempSnapshotPrefix = "ndl-mig-"
)

// ModeInfo is the operator-facing description of a migration mode.
type ModeInfo struct {
	ID                string   `json:"id"`
	Label             string   `json:"label"`
	Consistency       string   `json:"consistency"`
	SourceSafety      string   `json:"source_safety"`
	Summary           string   `json:"summary"`
	Benefits          []string `json:"benefits,omitempty"`
	Risks             []string `json:"risks,omitempty"`
	RequiresAck       bool     `json:"requires_ack"`
	RequiresStopped   bool     `json:"requires_stopped"`
	SourceMutation    string   `json:"source_mutation,omitempty"`
	Available         bool     `json:"available"`
	UnavailableReason string   `json:"unavailable_reason,omitempty"`
}

// Manifest is the versioned portable No-dal migration document.
type Manifest struct {
	SchemaVersion string            `json:"schema_version"`
	Kind          string            `json:"kind"`
	Identity      Identity          `json:"identity"`
	Source        SourceMeta        `json:"source"`
	VM            *VMSection        `json:"vm,omitempty"`
	Container     *ContainerSection `json:"container,omitempty"`
	Tags          []string          `json:"tags,omitempty"`
	Description   string            `json:"description,omitempty"`
	Startup       *Startup          `json:"startup,omitempty"`
	Checksums     map[string]string `json:"checksums,omitempty"`
	Export        *ExportMeta       `json:"export,omitempty"`
	Warnings      []Finding         `json:"warnings,omitempty"`
}

// Identity names the workload without implying destination UUID reuse.
type Identity struct {
	Name     string `json:"name"`
	SourceID string `json:"source_id,omitempty"`
}

// SourceMeta records where the manifest came from.
type SourceMeta struct {
	Adapter  string `json:"adapter"`
	Endpoint string `json:"endpoint,omitempty"`
	Node     string `json:"node,omitempty"`
	Type     string `json:"type,omitempty"`
	Running  bool   `json:"running,omitempty"`
	Notes    string `json:"notes,omitempty"`
}

// VMSection is VM-specific intent. It is not used for containers.
type VMSection struct {
	CPUs        int        `json:"cpus"`
	Sockets     int        `json:"sockets,omitempty"`
	Cores       int        `json:"cores,omitempty"`
	MemoryBytes int64      `json:"memory_bytes"`
	Firmware    string     `json:"firmware,omitempty"`
	SecureBoot  bool       `json:"secure_boot,omitempty"`
	Machine     string     `json:"machine,omitempty"`
	CPUType     string     `json:"cpu_type,omitempty"`
	BootOrder   []string   `json:"boot_order,omitempty"`
	Disks       []Disk     `json:"disks,omitempty"`
	NICs        []NIC      `json:"nics,omitempty"`
	CloudInit   *CloudInit `json:"cloud_init,omitempty"`
	TPM         *TPMMeta   `json:"tpm,omitempty"`
}

// ContainerSection is system-container intent. It is not used for VMs.
type ContainerSection struct {
	CPUs         int       `json:"cpus"`
	MemoryBytes  int64     `json:"memory_bytes"`
	Privileged   bool      `json:"privileged"`
	UIDMap       string    `json:"uid_map,omitempty"`
	GIDMap       string    `json:"gid_map,omitempty"`
	Hostname     string    `json:"hostname,omitempty"`
	Rootfs       *Artifact `json:"rootfs,omitempty"`
	Mounts       []Mount   `json:"mounts,omitempty"`
	NICs         []NIC     `json:"nics,omitempty"`
	Capabilities []string  `json:"capabilities,omitempty"`
}

// Disk describes one virtual disk.
type Disk struct {
	ID           string `json:"id,omitempty"`
	Role         string `json:"role,omitempty"`
	Source       string `json:"source,omitempty"`
	Format       string `json:"format,omitempty"`
	Bus          string `json:"bus,omitempty"`
	SizeBytes    int64  `json:"size_bytes,omitempty"`
	Storage      string `json:"storage,omitempty"`
	BootIndex    int    `json:"boot_index,omitempty"`
	Artifact     string `json:"artifact,omitempty"`
	Checksum     string `json:"checksum,omitempty"`
	VolID        string `json:"vol_id,omitempty"`
	Downloadable bool   `json:"downloadable,omitempty"`
}

// NIC describes one network interface.
type NIC struct {
	ID      string `json:"id,omitempty"`
	Model   string `json:"model,omitempty"`
	MAC     string `json:"mac,omitempty"`
	Bridge  string `json:"bridge,omitempty"`
	VLAN    int    `json:"vlan,omitempty"`
	Network string `json:"network,omitempty"`
}

// CloudInit is transferable guest seed data.
type CloudInit struct {
	UserData string `json:"user_data,omitempty"`
	MetaData string `json:"meta_data,omitempty"`
}

// TPMMeta records TPM presence. Secrets are never embedded.
type TPMMeta struct {
	Present bool   `json:"present"`
	Notes   string `json:"notes,omitempty"`
}

// Mount is a container mount point.
type Mount struct {
	Source string `json:"source,omitempty"`
	Target string `json:"target,omitempty"`
	Type   string `json:"type,omitempty"`
}

// Artifact names a file in a bundle or staging tree.
type Artifact struct {
	Path     string `json:"path"`
	Format   string `json:"format,omitempty"`
	Size     int64  `json:"size,omitempty"`
	Checksum string `json:"checksum,omitempty"`
}

// Startup is transferable start metadata.
type Startup struct {
	Autostart bool `json:"autostart"`
	Order     int  `json:"order,omitempty"`
}

// ExportMeta is producer information for a portable bundle.
type ExportMeta struct {
	CreatedAt time.Time `json:"created_at"`
	Producer  string    `json:"producer"`
	Kind      string    `json:"kind"`
}

// Finding is a compatibility or plan note. It is never silent.
type Finding struct {
	Level   string `json:"level"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

// DiscoveredWorkload is a pre-migration inventory row.
type DiscoveredWorkload struct {
	SourceID       string   `json:"source_id"`
	Name           string   `json:"name"`
	Kind           string   `json:"kind"`
	TypeLabel      string   `json:"type_label"`
	Running        bool     `json:"running"`
	Node           string   `json:"node,omitempty"`
	CPUs           int      `json:"cpus,omitempty"`
	MemoryBytes    int64    `json:"memory_bytes,omitempty"`
	DiskBytes      int64    `json:"disk_bytes,omitempty"`
	Storage        []string `json:"storage,omitempty"`
	Networks       []string `json:"networks,omitempty"`
	IP             string   `json:"ip,omitempty"`
	Firmware       string   `json:"firmware,omitempty"`
	Snapshots      int      `json:"snapshots,omitempty"`
	Backups        int      `json:"backups,omitempty"`
	Caps           []string `json:"capabilities,omitempty"`
	EstimatedBytes int64    `json:"estimated_bytes,omitempty"`
}

// Discovery is the result of Connect plus Discover. It does not start a transfer.
type Discovery struct {
	Adapter   string               `json:"adapter"`
	Endpoint  string               `json:"endpoint,omitempty"`
	Workloads []DiscoveredWorkload `json:"workloads"`
	Storages  []NamedRef           `json:"storages,omitempty"`
	Networks  []NamedRef           `json:"networks,omitempty"`
	Warnings  []Finding            `json:"warnings,omitempty"`
}

// NamedRef is a source storage or network identifier.
type NamedRef struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Kind string `json:"kind,omitempty"`
}

// Mapping binds source identifiers onto No-dal resources.
type Mapping struct {
	Storage map[string]string `json:"storage,omitempty"`
	Network map[string]string `json:"network,omitempty"`
	VLAN    map[string]string `json:"vlan,omitempty"`
}

// ItemPlan is one selected workload in a migration plan.
type ItemPlan struct {
	SourceID            string    `json:"source_id"`
	Name                string    `json:"name"`
	Kind                string    `json:"kind"`
	Mode                string    `json:"mode"`
	Manifest            Manifest  `json:"manifest"`
	Compatibility       string    `json:"compatibility"`
	Findings            []Finding `json:"findings"`
	EstimatedBytes      int64     `json:"estimated_bytes,omitempty"`
	StartAfter          bool      `json:"start_after"`
	OverrideMapping     *Mapping  `json:"override_mapping,omitempty"`
	LiveAck             bool      `json:"live_ack,omitempty"`
	IdentityConflictAck bool      `json:"identity_conflict_ack,omitempty"`
}

// Plan is the operator-reviewed intent. The engine executes this document.
type Plan struct {
	ID              string     `json:"id"`
	Direction       string     `json:"direction"`
	Adapter         string     `json:"adapter"`
	SourceID        string     `json:"source_id,omitempty"`
	DestinationNode string     `json:"destination_node,omitempty"`
	Mapping         Mapping    `json:"mapping"`
	Items           []ItemPlan `json:"items"`
	StartAfter      bool       `json:"start_after"`
}

// Report is post-migration verification. Claims only observed levels.
type Report struct {
	WorkloadID    string            `json:"workload_id,omitempty"`
	Name          string            `json:"name"`
	Fields        map[string]string `json:"fields"`
	Consistency   string            `json:"consistency"`
	SourceSafety  string            `json:"source_safety"`
	SourceState   string            `json:"source_state"`
	Destination   string            `json:"destination"`
	Observed      []string          `json:"observed"`
	Unobserved    []string          `json:"unobserved"`
	SourceChanges string            `json:"source_changes"`
}

// JobStatus is the operator-visible transfer state.
type JobStatus struct {
	ID              string   `json:"id"`
	State           string   `json:"state"`
	Stage           string   `json:"stage"`
	Workload        string   `json:"workload,omitempty"`
	BytesDone       int64    `json:"bytes_done"`
	BytesTotal      int64    `json:"bytes_total,omitempty"`
	Percent         int      `json:"percent,omitempty"`
	RateBps         int64    `json:"rate_bps,omitempty"`
	ElapsedMS       int64    `json:"elapsed_ms,omitempty"`
	ETASeconds      int64    `json:"eta_seconds,omitempty"`
	Message         string   `json:"message,omitempty"`
	SourceUntouched bool     `json:"source_untouched"`
	PartialDest     string   `json:"partial_dest,omitempty"`
	Retryable       bool     `json:"retryable"`
	Reports         []Report `json:"reports,omitempty"`
}

// AdapterInfo is catalog metadata. Unsupported paths are not listed as working.
type AdapterInfo struct {
	ID         string   `json:"id"`
	Label      string   `json:"label"`
	Role       string   `json:"role"`
	Discovery  bool     `json:"discovery"`
	Import     bool     `json:"import"`
	Export     bool     `json:"export"`
	ExportKind string   `json:"export_kind,omitempty"`
	Modes      []string `json:"modes"`
	Notes      string   `json:"notes"`
	Credential string   `json:"credential,omitempty"`
}
