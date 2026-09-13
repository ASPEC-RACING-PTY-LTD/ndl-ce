package backuphost

import (
	"github.com/no-dal/ndl-ce/internal/backup"
	"github.com/no-dal/ndl-ce/internal/backupscope"
)

const (
	ActionCapture   = "v2-capture"
	ActionRestore   = "v2-restore"
	ActionStatus    = "v2-status"
	ActionPreview   = "v2-preview"
	ActionExpire    = "v2-expire"
	ActionWorkspace = "v2-workspace"
	ActionEnqueue   = "v2-enqueue"

	DefaultRoot          = "/var/lib/ndl/backup-repo"
	DefaultMaxLocalBytes = 50 << 30
	DefaultMinHostFree   = 10 << 30
	DefaultUploadWorkers = 4
	DefaultCaptureSlots  = 1
)

// Request is the JSON body passed as dest_path for V2 backup-copy actions.
// Credentials are request-scoped and must never be logged.
type Request struct {
	Action       string                `json:"action,omitempty"`
	WorkloadID   string                `json:"workload_id,omitempty"`
	WorkloadName string                `json:"workload_name,omitempty"`
	Unit         string                `json:"unit,omitempty"`
	Consistency  string                `json:"consistency,omitempty"`
	CaptureMode  string                `json:"capture_mode,omitempty"`
	Blueprint    backup.Blueprint      `json:"blueprint,omitempty"`
	Selection    backupscope.Selection `json:"selection,omitempty"`
	BackupID     string                `json:"backup_id,omitempty"`
	Namespace    string                `json:"namespace,omitempty"`
	Dest         string                `json:"dest,omitempty"`
	Chown        bool                  `json:"chown,omitempty"`
	Settings     Settings              `json:"settings,omitempty"`
	Target       TargetSpec            `json:"target,omitempty"`
}

// TargetSpec describes a destination. Secrets stay on this request only.
type TargetSpec struct {
	ID        string `json:"id,omitempty"`
	Kind      string `json:"kind,omitempty"`
	Locator   string `json:"locator,omitempty"`
	Endpoint  string `json:"endpoint,omitempty"`
	Region    string `json:"region,omitempty"`
	Bucket    string `json:"bucket,omitempty"`
	Prefix    string `json:"prefix,omitempty"`
	AccessKey string `json:"access_key,omitempty"`
	SecretKey string `json:"secret_key,omitempty"`
}

// Settings bounds the local workspace. Zero values use defaults.
type Settings struct {
	MaxLocalBytes      int64 `json:"max_local_bytes,omitempty"`
	MinHostFreeBytes   int64 `json:"min_host_free_bytes,omitempty"`
	CaptureConcurrency int   `json:"capture_concurrency,omitempty"`
	UploadWorkers      int   `json:"upload_workers,omitempty"`
	BandwidthLimitBPS  int64 `json:"bandwidth_limit_bps,omitempty"`
}

// Result is returned in CopyResult.Extra.
type Result struct {
	BackupID        string              `json:"backup_id,omitempty"`
	Namespace       string              `json:"namespace,omitempty"`
	WorkloadID      string              `json:"workload_id,omitempty"`
	WorkloadName    string              `json:"workload_name,omitempty"`
	LocalComplete   bool                `json:"local_complete,omitempty"`
	Remote          backup.RemoteState  `json:"remote,omitempty"`
	CaptureMode     string              `json:"capture_mode,omitempty"`
	Consistency     string              `json:"consistency,omitempty"`
	LogicalBytes    int64               `json:"logical_bytes,omitempty"`
	BytesRead       int64               `json:"bytes_read,omitempty"`
	PhysicalNewData int64               `json:"physical_new_data,omitempty"`
	ChunksTotal     int                 `json:"chunks_total,omitempty"`
	ChunksNew       int                 `json:"chunks_new,omitempty"`
	ChunksReused    int                 `json:"chunks_reused,omitempty"`
	FilesScanned    int                 `json:"files_scanned,omitempty"`
	FilesUnchanged  int                 `json:"files_unchanged,omitempty"`
	FilesChanged    int                 `json:"files_changed,omitempty"`
	PacksCommitted  int                 `json:"packs_committed,omitempty"`
	DurationNanos   int64               `json:"duration_ns,omitempty"`
	DedupeRatio     float64             `json:"dedupe_ratio,omitempty"`
	RepoBytes       int64               `json:"repo_bytes,omitempty"`
	PendingUploads  int                 `json:"pending_uploads,omitempty"`
	Protected       int                 `json:"protected_count,omitempty"`
	Locator         string              `json:"locator,omitempty"`
	Blueprint       backup.Blueprint    `json:"blueprint,omitempty"`
	Preview         backupscope.Preview `json:"preview,omitempty"`
	Workspace       WorkspaceStatus     `json:"workspace,omitempty"`
	Points          []PointView         `json:"points,omitempty"`
	Error           string              `json:"error,omitempty"`
}

// PointView is a secret-free restore-point summary.
type PointView struct {
	BackupID      string             `json:"backup_id"`
	Namespace     string             `json:"namespace"`
	WorkloadID    string             `json:"workload_id"`
	WorkloadName  string             `json:"workload_name"`
	CreatedAtNS   int64              `json:"created_at_ns"`
	LocalComplete bool               `json:"local_complete"`
	Remote        backup.RemoteState `json:"remote"`
	CaptureMode   string             `json:"capture_mode,omitempty"`
}

// WorkspaceStatus is the local repository footprint.
type WorkspaceStatus struct {
	Root             string `json:"root"`
	RepoBytes        int64  `json:"repo_bytes"`
	MaxLocalBytes    int64  `json:"max_local_bytes"`
	MinHostFreeBytes int64  `json:"min_host_free_bytes"`
	HostFreeBytes    int64  `json:"host_free_bytes"`
	PendingUploads   int    `json:"pending_uploads"`
	CaptureBusy      bool   `json:"capture_busy"`
	UploadWorkers    int    `json:"upload_workers"`
}

func IsAction(action string) bool {
	switch action {
	case ActionCapture, ActionRestore, ActionStatus, ActionPreview, ActionExpire, ActionWorkspace, ActionEnqueue:
		return true
	default:
		return false
	}
}

func (s Settings) withDefaults() Settings {
	if s.MaxLocalBytes <= 0 {
		s.MaxLocalBytes = DefaultMaxLocalBytes
	}
	if s.MinHostFreeBytes <= 0 {
		s.MinHostFreeBytes = DefaultMinHostFree
	}
	if s.CaptureConcurrency <= 0 {
		s.CaptureConcurrency = DefaultCaptureSlots
	}
	if s.UploadWorkers <= 0 {
		s.UploadWorkers = DefaultUploadWorkers
	}
	return s
}
