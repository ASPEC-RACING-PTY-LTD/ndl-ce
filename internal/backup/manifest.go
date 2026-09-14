package backup

import (
	"strings"
	"time"
	"unicode"

	"github.com/no-dal/ndl-ce/internal/backup/cdc"
)

// EntryType classifies a filesystem object in a manifest.
type EntryType string

const (
	EntryFile    EntryType = "file"
	EntryDir     EntryType = "dir"
	EntrySymlink EntryType = "symlink"
)

// FileEntry records one filesystem object and, for regular files, the ordered
// content-defined chunk ids needed to reconstruct it. Enough metadata is kept
// to restore the object faithfully.
type FileEntry struct {
	Path     string    `json:"path"`
	Type     EntryType `json:"type"`
	Mode     uint32    `json:"mode"`
	UID      int       `json:"uid"`
	GID      int       `json:"gid"`
	Size     int64     `json:"size"`
	MTimeNS  int64     `json:"mtime_ns"`
	Linkname string    `json:"linkname,omitempty"`
	Chunks   []KeyID   `json:"chunks,omitempty"`
}

// NetInterface is a network interface captured in the Blueprint.
type NetInterface struct {
	Name   string `json:"name"`
	MAC    string `json:"mac,omitempty"`
	Bridge string `json:"bridge,omitempty"`
	VLAN   int    `json:"vlan,omitempty"`
}

// Mount is a mount point or bind mount captured in the Blueprint.
type Mount struct {
	Source string `json:"source"`
	Target string `json:"target"`
	Bind   bool   `json:"bind,omitempty"`
}

// Blueprint is the versioned machine manifest: enough No-DAL metadata to rebuild
// the workload configuration independently of filesystem contents. It must not
// contain secrets.
type Blueprint struct {
	Kind         string         `json:"kind"`
	Version      int            `json:"version"`
	WorkloadID   string         `json:"workload_id"`
	Name         string         `json:"name"`
	Hostname     string         `json:"hostname,omitempty"`
	WorkloadType string         `json:"workload_type,omitempty"`
	OS           string         `json:"os,omitempty"`
	Arch         string         `json:"arch,omitempty"`
	BaseImage    string         `json:"base_image,omitempty"`
	CPU          int            `json:"cpu,omitempty"`
	MemoryBytes  int64          `json:"memory_bytes,omitempty"`
	StorageBytes int64          `json:"storage_bytes,omitempty"`
	StoragePool  string         `json:"storage_pool,omitempty"`
	Autostart    bool           `json:"autostart,omitempty"`
	Nesting      bool           `json:"nesting,omitempty"`
	Features     []string       `json:"features,omitempty"`
	Interfaces   []NetInterface `json:"interfaces,omitempty"`
	Mounts       []Mount        `json:"mounts,omitempty"`
	Docker       *DockerInfo    `json:"docker,omitempty"`
	Packages     []string       `json:"packages,omitempty"`
	Services     []string       `json:"services,omitempty"`
	CaptureMode  string         `json:"capture_mode,omitempty"`
	Includes     []string       `json:"includes,omitempty"`
	Excludes     []string       `json:"excludes,omitempty"`
}

// Capture modes. Smart and Custom restrict the live tree; Full captures the
// recoverable filesystem minus technical mounts. All three use this engine.
const (
	CaptureModeSmart  = "smart"
	CaptureModeCustom = "custom"
	CaptureModeFull   = "full"
)

// DockerInfo inventories a Docker-enabled workload without exposing secrets.
type DockerInfo struct {
	EngineVersion  string   `json:"engine_version,omitempty"`
	ComposeVersion string   `json:"compose_version,omitempty"`
	ComposeProject []string `json:"compose_projects,omitempty"`
	NamedVolumes   []string `json:"named_volumes,omitempty"`
	BindMounts     []string `json:"bind_mounts,omitempty"`
	Images         []string `json:"images,omitempty"`
	LocalImages    []string `json:"local_images,omitempty"`
}

// CaptureStats instruments a backup so its cost can be explained from real
// numbers rather than guesses.
type CaptureStats struct {
	FilesScanned    int   `json:"files_scanned"`
	FilesUnchanged  int   `json:"files_unchanged"`
	FilesChanged    int   `json:"files_changed"`
	LogicalBytes    int64 `json:"logical_bytes"`
	BytesRead       int64 `json:"bytes_read"`
	ChunksTotal     int   `json:"chunks_total"`
	ChunksNew       int   `json:"chunks_new"`
	ChunksReused    int   `json:"chunks_reused"`
	StoredBytes     int64 `json:"stored_bytes"`
	DurationNanos   int64 `json:"duration_ns"`
	PacksCommitted  int   `json:"packs_committed"`
	PhysicalNewData int64 `json:"physical_new_data"`
}

// Consistency levels. The engine never freezes a guest, so the default is
// crash-consistent; a successful application-aware pre-hook may raise it.
const (
	ConsistencyCrash = "crash-consistent"
	ConsistencyApp   = "application-consistent"
)

// Manifest is the authenticated commit record for one restore point.
type Manifest struct {
	Kind           string       `json:"kind"`
	RepoVersion    int          `json:"repo_version"`
	ChunkAlgorithm string       `json:"chunk_algorithm"`
	ChunkConfig    cdc.Config   `json:"chunk_config"`
	BackupID       string       `json:"backup_id"`
	WorkloadID     string       `json:"workload_id"`
	WorkloadName   string       `json:"workload_name"`
	Namespace      string       `json:"namespace"`
	CreatedAtNS    int64        `json:"created_at_ns"`
	Consistency    string       `json:"consistency"`
	Blueprint      Blueprint    `json:"blueprint"`
	Files          []FileEntry  `json:"files"`
	Stats          CaptureStats `json:"stats"`
}

// RemoteState models where a restore point exists. A locally complete restore
// point is not yet protected off-host.
type RemoteState string

const (
	RemoteNone      RemoteState = "local-only"
	RemoteQueued    RemoteState = "queued"
	RemoteUploading RemoteState = "uploading"
	RemoteVerifying RemoteState = "verifying"
	RemoteProtected RemoteState = "protected"
	RemoteFailed    RemoteState = "failed"
)

// PointState is the unsealed sidecar tracking local and remote completion for a
// restore point. The manifest itself is sealed; this small file drives UI and
// GC without decrypting every manifest.
type PointState struct {
	BackupID      string      `json:"backup_id"`
	Namespace     string      `json:"namespace"`
	WorkloadID    string      `json:"workload_id"`
	WorkloadName  string      `json:"workload_name"`
	CreatedAtNS   int64       `json:"created_at_ns"`
	LocalComplete bool        `json:"local_complete"`
	Remote        RemoteState `json:"remote"`
	Packs         []string    `json:"packs"`
}

// CollectedAt returns the creation time.
func (m Manifest) CollectedAt() time.Time { return time.Unix(0, m.CreatedAtNS).UTC() }

// Namespace turns a workload name plus id into a stable, human-readable path
// element. Immutable ids are stored in metadata; the namespace stays readable.
func Namespace(name, workloadID string) string {
	base := sanitize(name)
	id := shortHex(workloadID)
	if id == "" {
		return base
	}
	return base + "-" + id
}

func sanitize(name string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.TrimSpace(name) {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r) || r == '.' || r == '_':
			b.WriteRune(r)
			dash = false
		default:
			if !dash && b.Len() > 0 {
				b.WriteByte('-')
				dash = true
			}
		}
	}
	out := strings.Trim(b.String(), "-.")
	if out == "" {
		return "workload"
	}
	if len(out) > 64 {
		out = strings.Trim(out[:64], "-.")
	}
	if out == "" {
		return "workload"
	}
	return out
}

func shortHex(id string) string {
	h := strings.ReplaceAll(strings.ToLower(strings.TrimSpace(id)), "-", "")
	if len(h) > 8 {
		return h[:8]
	}
	return h
}
