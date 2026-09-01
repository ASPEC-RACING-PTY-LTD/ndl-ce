package storage

import "time"

// BackendDirectory is the Phase 3 Directory driver.
const BackendDirectory = "directory"

// Volume kinds.
const (
	KindBlock      = "block"
	KindFilesystem = "filesystem"
)

// Storage classes (content).
const (
	ClassVMDisk        = "vm-disk"
	ClassContainerRoot = "container-root"
	ClassISO           = "iso"
	ClassTemplate      = "template"
	ClassBackupStaging = "backup-staging"
)

// Library kinds.
const (
	LibraryISO        = "iso"
	LibraryCloudImage = "cloud-image"
)

// Object and pool status values.
const (
	StatusAvailable   = "available"
	StatusWarning     = "warning"
	StatusUnavailable = "unavailable"
	StatusChecking    = "checking"
	StatusFailed      = "failed"
)

// Formats supported by the Directory driver.
const (
	FormatQCOW2     = "qcow2"
	FormatRaw       = "raw"
	FormatDirectory = "directory"
	FormatFile      = "file"
)

// Xattr names and states.
const (
	XattrVolumeID     = "user.ndl.volume_id"
	XattrOK           = "ok"
	XattrMissing      = "missing"
	XattrMismatch     = "mismatch"
	XattrUnsupported  = "unsupported"
	XattrInaccessible = "inaccessible"
)

// Warning codes returned to API/UI/CLI.
const (
	WarnRootFilesystem   = "root_filesystem"
	WarnSharedFilesystem = "shared_filesystem"
	WarnThinOvercommit   = "thin_overcommit"
)

const (
	DefaultPoolName     = "local"
	DefaultPoolPath     = "/var/lib/ndl/storage/local"
	MarkerFile          = ".ndl-pool.json"
	MarkerSchema        = "ndl.storage.pool.v1"
	MinBlockBytes       = 1 << 20
	MaxVolumeBytes      = 8 << 40
	DefaultLibraryMax   = 64 << 30
	MinPoolFreeBytes    = 16 << 20
	QEMUImgPath         = "/usr/bin/qemu-img"
	RootHeadroomMessage = "This Directory pool shares the host root filesystem. Filling this pool can fill the host root filesystem and destabilize No-dal and the host."
	SharedFSMessage     = "This Directory pool appears to reside on a shared or network filesystem. The Directory driver does not provide clustered or distributed storage semantics."
)

// VolumeHandle is the architecture identity for a volume.
// VolumeID is the desired identity. BackendRef is a locator, never the identity.
type VolumeHandle struct {
	VolumeID    string `json:"volume_id"`
	BackendType string `json:"backend_type"`
	BackendRef  string `json:"backend_ref"`
	Kind        string `json:"kind"`
	Class       string `json:"class"`
	Format      string `json:"format"`
}

// Capacity distinguishes physical and logical sizes.
// Unavailable pools must leave these nil rather than report zero.
type Capacity struct {
	UsableBytes      *int64 `json:"usable_bytes"`
	AllocatedBytes   *int64 `json:"allocated_bytes"`
	ProvisionedBytes *int64 `json:"provisioned_bytes"`
	TotalBytes       *int64 `json:"total_bytes"`
}

// Capabilities describe what this backend can actually do.
type Capabilities struct {
	VolumeCreate     bool     `json:"volume_create"`
	SparseFiles      bool     `json:"sparse_files"`
	Snapshots        bool     `json:"snapshots"`
	IncrementalSend  bool     `json:"incremental_send"`
	XattrIdentity    bool     `json:"xattr_identity"`
	SharedWarning    bool     `json:"shared_warning"`
	SupportedClasses []string `json:"supported_classes"`
}

// DirectoryCapabilities is the honest Directory set. Incremental send is always false.
func DirectoryCapabilities(xattr, shared bool) Capabilities {
	return Capabilities{
		VolumeCreate:    true,
		SparseFiles:     true,
		Snapshots:       false,
		IncrementalSend: false,
		XattrIdentity:   xattr,
		SharedWarning:   shared,
		SupportedClasses: []string{
			ClassVMDisk, ClassContainerRoot, ClassISO, ClassTemplate, ClassBackupStaging,
		},
	}
}

// BackingIdentity is enough to detect a missing disk whose mountpoint remains.
type BackingIdentity struct {
	FSUUID     string `json:"fs_uuid"`
	FSType     string `json:"fstype"`
	MountPoint string `json:"mount_point"`
	Device     string `json:"device"`
	Dev        uint64 `json:"dev"`
	RootBacked bool   `json:"root_backed"`
	Shared     bool   `json:"shared"`
}

// PoolMarker is written under a Directory pool root. It is not desired identity.
type PoolMarker struct {
	SchemaVersion string          `json:"schema_version"`
	PoolID        string          `json:"pool_id"`
	BackendType   string          `json:"backend_type"`
	CreatedAt     string          `json:"created_at"`
	Adopted       bool            `json:"adopted"`
	Backing       BackingIdentity `json:"backing"`
}

// PoolHint is what the control plane sends when observing known pools.
type PoolHint struct {
	PoolID      string          `json:"pool_id"`
	BackendType string          `json:"backend_type"`
	RootPath    string          `json:"root_path"`
	Backing     BackingIdentity `json:"backing"`
}

// ObservedPool is agent-side pool state.
type ObservedPool struct {
	PoolID       string          `json:"pool_id"`
	BackendType  string          `json:"backend_type"`
	RootPath     string          `json:"root_path"`
	Status       string          `json:"status"`
	Reason       string          `json:"reason,omitempty"`
	Warnings     []string        `json:"warnings,omitempty"`
	WarningText  []string        `json:"warning_text,omitempty"`
	Capacity     Capacity        `json:"capacity"`
	Capabilities Capabilities    `json:"capabilities"`
	Backing      BackingIdentity `json:"backing"`
	Writable     bool            `json:"writable"`
	ObservedAt   time.Time       `json:"observed_at"`
}

// ObservedVolume is a file or directory found under a pool.
type ObservedVolume struct {
	VolumeID    string `json:"volume_id"`
	PoolID      string `json:"pool_id"`
	BackendRef  string `json:"backend_ref"`
	Class       string `json:"class"`
	Kind        string `json:"kind"`
	Format      string `json:"format"`
	Status      string `json:"status"`
	XattrState  string `json:"xattr_state"`
	Allocated   int64  `json:"allocated_bytes"`
	Provisioned int64  `json:"provisioned_bytes"`
}

// ObservedLibrary is a library object found under a pool.
type ObservedLibrary struct {
	ItemID     string `json:"item_id"`
	PoolID     string `json:"pool_id"`
	BackendRef string `json:"backend_ref"`
	Kind       string `json:"kind"`
	Status     string `json:"status"`
	SizeBytes  int64  `json:"size_bytes"`
}

// Observation is the agent's storage snapshot.
type Observation struct {
	ObservedAt time.Time         `json:"observed_at"`
	Pools      []ObservedPool    `json:"pools"`
	Volumes    []ObservedVolume  `json:"volumes"`
	Library    []ObservedLibrary `json:"library"`
}

// CreatePoolRequest is a typed Directory pool create.
type CreatePoolRequest struct {
	PoolID   string `json:"pool_id"`
	Name     string `json:"name"`
	RootPath string `json:"root_path"`
	Create   bool   `json:"create"`
}

// CreatePoolResult is returned after a successful pool create.
type CreatePoolResult struct {
	PoolID       string          `json:"pool_id"`
	RootPath     string          `json:"root_path"`
	Adopted      bool            `json:"adopted"`
	Status       string          `json:"status"`
	Warnings     []string        `json:"warnings,omitempty"`
	WarningText  []string        `json:"warning_text,omitempty"`
	Capacity     Capacity        `json:"capacity"`
	Capabilities Capabilities    `json:"capabilities"`
	Backing      BackingIdentity `json:"backing"`
}

// CreateVolumeRequest is a typed volume create. Destination path is not caller-chosen.
type CreateVolumeRequest struct {
	VolumeID string `json:"volume_id"`
	PoolID   string `json:"pool_id"`
	RootPath string `json:"root_path"`
	Class    string `json:"class"`
	Size     int64  `json:"size_bytes"`
	Format   string `json:"format"`
}

// CreateVolumeResult is returned after a successful volume create.
type CreateVolumeResult struct {
	Handle     VolumeHandle `json:"handle"`
	Allocated  int64        `json:"allocated_bytes"`
	XattrState string       `json:"xattr_state"`
}

// BeginUploadRequest starts a library write into an agent-owned temp file.
type BeginUploadRequest struct {
	ItemID          string   `json:"item_id"`
	PoolID          string   `json:"pool_id"`
	RootPath        string   `json:"root_path"`
	Kind            string   `json:"kind"`
	DisplayName     string   `json:"display_name"`
	MaxBytes        int64    `json:"max_bytes"`
	RejectChecksums []string `json:"reject_checksums,omitempty"`
}

// FinishUploadRequest finalizes a streamed library upload.
type FinishUploadRequest struct {
	ItemID         string `json:"item_id"`
	ExpectedSHA256 string `json:"expected_sha256"`
}

// UploadResult is a finished library object.
type UploadResult struct {
	ItemID      string `json:"item_id"`
	PoolID      string `json:"pool_id"`
	Kind        string `json:"kind"`
	DisplayName string `json:"display_name"`
	BackendRef  string `json:"backend_ref"`
	SizeBytes   int64  `json:"size_bytes"`
	SHA256      string `json:"sha256"`
}

func int64ptr(v int64) *int64 { return &v }

func copyCapacity(c Capacity) Capacity {
	out := Capacity{}
	if c.UsableBytes != nil {
		v := *c.UsableBytes
		out.UsableBytes = &v
	}
	if c.AllocatedBytes != nil {
		v := *c.AllocatedBytes
		out.AllocatedBytes = &v
	}
	if c.ProvisionedBytes != nil {
		v := *c.ProvisionedBytes
		out.ProvisionedBytes = &v
	}
	if c.TotalBytes != nil {
		v := *c.TotalBytes
		out.TotalBytes = &v
	}
	return out
}
