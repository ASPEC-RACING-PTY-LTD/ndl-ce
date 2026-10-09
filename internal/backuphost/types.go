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
	// ActionGC collects garbage, compacts packs and sweeps remote packs.
	ActionGC = "v2-gc"
	// ActionRemoteUsage measures a remote target without changing it.
	ActionRemoteUsage = "v2-remote-usage"
	// ActionRelocate moves an empty repository to another directory. It is
	// handled by the agent, which owns the repository location.
	ActionRelocate = "v2-relocate"
	// ActionVerify checks every local restore point. Read only.
	ActionVerify = "v2-verify"
	// ActionKey exports the repository key so it can be stored safely.
	ActionKey = "v2-key"
	// ActionWipeRemote deletes every backup object in a target.
	ActionWipeRemote = "v2-wipe-remote"

	DefaultRoot                = "/var/lib/ndl/backup-repo"
	DefaultMaxLocalBytes       = 50 << 30
	DefaultMinHostFree         = 10 << 30
	DefaultUploadWorkers       = 4
	DefaultCaptureSlots        = 1
	DefaultCacheRetentionHours = 0
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
	// DeferGC skips collection after an expire; the caller sends ActionGC.
	DeferGC bool `json:"defer_gc,omitempty"`
	// Reason labels an ActionGC run in reports.
	Reason string `json:"reason,omitempty"`
	// Path is the new repository directory for ActionRelocate.
	Path string `json:"path,omitempty"`
	// DiskPath is the VM disk image captured with CaptureModeDisk. Flatten
	// converts it first when it has a backing chain.
	DiskPath string `json:"disk_path,omitempty"`
	Flatten  bool   `json:"flatten,omitempty"`
	// RemoteOnly expires only the remote copy and keeps the local one.
	RemoteOnly bool `json:"remote_only,omitempty"`
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
	MaxLocalBytes       int64 `json:"max_local_bytes,omitempty"`
	MinHostFreeBytes    int64 `json:"min_host_free_bytes,omitempty"`
	CaptureConcurrency  int   `json:"capture_concurrency,omitempty"`
	UploadWorkers       int   `json:"upload_workers,omitempty"`
	BandwidthLimitBPS   int64 `json:"bandwidth_limit_bps,omitempty"`
	CacheRetentionHours int   `json:"cache_retention_hours,omitempty"`
}

// Result is returned in CopyResult.Extra.
type Result struct {
	BackupID        string                     `json:"backup_id,omitempty"`
	Namespace       string                     `json:"namespace,omitempty"`
	WorkloadID      string                     `json:"workload_id,omitempty"`
	WorkloadName    string                     `json:"workload_name,omitempty"`
	LocalComplete   bool                       `json:"local_complete,omitempty"`
	Remote          backup.RemoteState         `json:"remote,omitempty"`
	CaptureMode     string                     `json:"capture_mode,omitempty"`
	Consistency     string                     `json:"consistency,omitempty"`
	ConsistencyInfo backup.ConsistencyReport   `json:"consistency_info,omitempty"`
	LogicalBytes    int64                      `json:"logical_bytes,omitempty"`
	BytesRead       int64                      `json:"bytes_read,omitempty"`
	PhysicalNewData int64                      `json:"physical_new_data,omitempty"`
	ChunksTotal     int                        `json:"chunks_total,omitempty"`
	ChunksNew       int                        `json:"chunks_new,omitempty"`
	ChunksReused    int                        `json:"chunks_reused,omitempty"`
	FilesScanned    int                        `json:"files_scanned,omitempty"`
	FilesUnchanged  int                        `json:"files_unchanged,omitempty"`
	FilesChanged    int                        `json:"files_changed,omitempty"`
	PacksCommitted  int                        `json:"packs_committed,omitempty"`
	DurationNanos   int64                      `json:"duration_ns,omitempty"`
	DedupeRatio     float64                    `json:"dedupe_ratio,omitempty"`
	RepoBytes       int64                      `json:"repo_bytes,omitempty"`
	PendingUploads  int                        `json:"pending_uploads,omitempty"`
	Protected       int                        `json:"protected_count,omitempty"`
	Locator         string                     `json:"locator,omitempty"`
	Blueprint       backup.Blueprint           `json:"blueprint,omitempty"`
	Preview         backupscope.Preview        `json:"preview,omitempty"`
	Workspace       WorkspaceStatus            `json:"workspace,omitempty"`
	Points          []PointView                `json:"points,omitempty"`
	Error           string                     `json:"error,omitempty"`
	GC              *GCReport                  `json:"gc,omitempty"`
	RemoteExpire    *backup.RemoteExpireResult `json:"remote_expire,omitempty"`
	RemoteUsage     *backup.RemoteUsage        `json:"remote_usage,omitempty"`
	Verify          []VerifyResult             `json:"verify,omitempty"`
	Key             string                     `json:"key,omitempty"`
	Wipe            *WipeResult                `json:"wipe,omitempty"`
}

// PointView is a secret-free restore-point summary.
type PointView struct {
	BackupID        string             `json:"backup_id"`
	Namespace       string             `json:"namespace"`
	WorkloadID      string             `json:"workload_id"`
	WorkloadName    string             `json:"workload_name"`
	CreatedAtNS     int64              `json:"created_at_ns"`
	LocalComplete   bool               `json:"local_complete"`
	Remote          backup.RemoteState `json:"remote"`
	CaptureMode     string             `json:"capture_mode,omitempty"`
	Consistency     string             `json:"consistency,omitempty"`
	LogicalBytes    int64              `json:"logical_bytes,omitempty"`
	PhysicalNewData int64              `json:"physical_new_data,omitempty"`
	UploadStartedNS int64              `json:"upload_started_ns,omitempty"`
	UploadEndedNS   int64              `json:"upload_ended_ns,omitempty"`
	Recovered       bool               `json:"recovered,omitempty"`
}

// WorkspaceStatus is the local repository footprint.
type WorkspaceStatus struct {
	Root                string `json:"root"`
	RepoBytes           int64  `json:"repo_bytes"`
	MaxLocalBytes       int64  `json:"max_local_bytes"`
	MinHostFreeBytes    int64  `json:"min_host_free_bytes"`
	HostFreeBytes       int64  `json:"host_free_bytes"`
	PendingUploads      int    `json:"pending_uploads"`
	CaptureBusy         bool   `json:"capture_busy"`
	CaptureActive       int    `json:"capture_active,omitempty"`
	CaptureConcurrency  int    `json:"capture_concurrency,omitempty"`
	UploadWorkers       int    `json:"upload_workers"`
	BandwidthLimitBPS   int64  `json:"bandwidth_limit_bps,omitempty"`
	CacheRetentionHours int    `json:"cache_retention_hours,omitempty"`

	HostTotalBytes int64 `json:"host_total_bytes,omitempty"`
	// EffectiveReserveBytes is the free space captures actually leave: the
	// configured reserve, raised to the host disk protection threshold.
	EffectiveReserveBytes    int64                 `json:"effective_reserve_bytes,omitempty"`
	FailedUploads            int                   `json:"failed_uploads,omitempty"`
	Usage                    *backup.RepoUsage     `json:"usage,omitempty"`
	Recovery                 backup.RecoveryReport `json:"recovery,omitempty"`
	LastGC                   *GCReport             `json:"last_gc,omitempty"`
	GCPending                bool                  `json:"gc_pending,omitempty"`
	PendingRemoteSweep       int                   `json:"pending_remote_sweep,omitempty"`
	StaleRestoreDirsRemoved  int                   `json:"stale_restore_dirs_removed,omitempty"`
	StaleRestoreBytesRemoved int64                 `json:"stale_restore_bytes_removed,omitempty"`
	KeyNote                  string                `json:"key_note,omitempty"`
	KeyExported              bool                  `json:"key_exported"`
}

func IsAction(action string) bool {
	switch action {
	case ActionCapture, ActionRestore, ActionStatus, ActionPreview, ActionExpire, ActionWorkspace, ActionEnqueue,
		ActionGC, ActionRemoteUsage, ActionRelocate, ActionVerify, ActionKey, ActionWipeRemote:
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
