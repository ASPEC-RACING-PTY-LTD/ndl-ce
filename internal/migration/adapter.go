package migration

import (
	"fmt"
	"strings"
)

// SourceAdapter discovers and reads source infrastructure.
// The interface intentionally omits any method that deletes, stops,
// decommissions, or cleans up source workloads, disks, snapshots, or backups.
type SourceAdapter interface {
	ID() string
	Info() AdapterInfo
	Discover(src SourceConn) (Discovery, error)
	Manifest(src SourceConn, sourceID string) (Manifest, error)
	Capabilities(src SourceConn, sourceID string) (Caps, error)
	OpenArtifact(src SourceConn, sourceID, artifact string) (Readable, error)
}

// DestAdapter writes No-dal-owned destination artifacts. It never touches source.
type DestAdapter interface {
	ID() string
	Info() AdapterInfo
	ExportKind() string
	Write(dest DestConn, m Manifest, artifacts map[string]string) (ExportResult, error)
}

// Readable is a source artifact stream. Closing it must not delete the source.
type Readable interface {
	Read(p []byte) (int, error)
	Close() error
	Size() int64
}

// SourceConn is a connected source. Token is never logged.
type SourceConn struct {
	ID       string
	Adapter  string
	Endpoint string
	Token    string
	Username string
	Extra    map[string]string
	Insecure bool
}

// DestConn is a destination write target owned by No-dal or a portable path.
type DestConn struct {
	Dir      string
	NodeID   string
	PoolID   string
	Format   string
	Compress bool
}

// ExportResult is a completed export. Direct creation is distinguished from a package.
type ExportResult struct {
	Kind     string
	Path     string
	Warnings []Finding
}

// Caps describes what a source can actually do for one workload.
type Caps struct {
	Offline            bool
	Snapshot           bool
	Live               bool
	Backup             bool
	Disk               bool
	SnapshotNote       string
	LiveNote           string
	BackupNote         string
	TemporaryMutations []string
}

func (c Caps) ModeAvailable(mode string) (bool, string) {
	switch mode {
	case ModeOffline:
		if c.Offline {
			return true, ""
		}
		return false, "Offline migration is unavailable for this source workload."
	case ModeSnapshot:
		if c.Snapshot {
			return true, ""
		}
		return false, firstNonEmpty(c.SnapshotNote, "Source storage does not expose the capabilities required for snapshot-assisted transfer.")
	case ModeLive:
		if c.Live {
			return true, ""
		}
		return false, firstNonEmpty(c.LiveNote, "Live is unavailable. Source storage does not expose the capabilities required for live transfer.")
	case ModeBackup:
		if c.Backup {
			return true, ""
		}
		return false, firstNonEmpty(c.BackupNote, "No completed backup artifact is available for this workload.")
	case ModeDisk:
		if c.Disk {
			return true, ""
		}
		return false, "Disk or archive import is unavailable for this source."
	default:
		return false, "Unknown migration mode."
	}
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

// Catalog lists adapters that have a real path. Placeholder success is not listed.
func Catalog() []AdapterInfo {
	return []AdapterInfo{
		{
			ID: AdapterNodal, Label: "No-dal portable bundle", Role: "both",
			Discovery: true, Import: true, Export: true, ExportKind: ExportBundle,
			Modes: []string{ModeDisk, ModeBackup},
			Notes: "Open documented bundle. Round-trip import and export. CE does not require Cloud.",
		},
		{
			ID: AdapterProxmox, Label: "Proxmox VE", Role: "source",
			Discovery: true, Import: true, Export: true, ExportKind: ExportPackage,
			Modes:      []string{ModeOffline, ModeBackup, ModeDisk},
			Notes:      "REST discovery and QEMU/LXC config translation. Offline copies a disk only when the source storage exposes a downloadable file (directory/NFS/CIFS). LVM-thin, ZFS zvols, and RBD are not HTTP-downloadable; copy the disk on the source host and use Disk import. LXC vzdump tar/tar.gz/tar.zst backups can be downloaded. VM vma vzdump is blocked (no vma extractor). Live and snapshot-assisted are unavailable. Export writes a compatible package, not a remote qm/pct create.",
			Credential: "API token. Prefer a token limited to VM.Audit, VM.Backup, and Datastore.Allocate where the platform allows. Broader tokens are disclosed when the platform cannot grant export-only rights.",
		},
		{
			ID: AdapterLibvirt, Label: "libvirt/KVM (XML plus disks)", Role: "source",
			Discovery: false, Import: true, Export: false,
			Modes: []string{ModeDisk},
			Notes: "Parses domain XML and QEMU-compatible disks. No-dal does not speak libvirt as a runtime and does not call virsh.",
		},
		{
			ID: AdapterDisk, Label: "VM disk or container archive", Role: "source",
			Discovery: false, Import: true, Export: true, ExportKind: ExportVMImage,
			Modes: []string{ModeDisk},
			Notes: "QCOW2, RAW, VMDK, VHD (vpc), VHDX when qemu-img supports the format. Container tar, tar.gz, and tar.zst archives. Missing VM hardware must be supplied by the operator.",
		},
		{
			ID: AdapterOVF, Label: "OVF / OVA", Role: "both",
			Discovery: false, Import: true, Export: true, ExportKind: ExportPackage,
			Modes: []string{ModeDisk},
			Notes: "OVF XML plus disks, or OVA tar. Disks convert through qemu-img. Export writes an OVF package, not a remote hypervisor create.",
		},
		{
			ID: AdapterBackup, Label: "Existing backup artifact", Role: "source",
			Discovery: false, Import: true, Export: false,
			Modes: []string{ModeBackup},
			Notes: "Imports a completed backup that this engine can validate. The original backup file is never deleted.",
		},
	}
}

func AdapterByID(id string) (AdapterInfo, error) {
	for _, a := range Catalog() {
		if a.ID == id {
			return a, nil
		}
	}
	return AdapterInfo{}, fmt.Errorf("adapter %s is not supported", id)
}

// Modes returns the operator catalog of migration modes.
func Modes() []ModeInfo {
	return []ModeInfo{
		{
			ID: ModeOffline, Label: "Offline", Consistency: ConsistencySafe, SourceSafety: SourceProtected,
			Summary: "Migrates a workload from a stable, stopped state.",
			Benefits: []string{
				"stable disk/filesystem state",
				"lowest consistency risk",
				"easier verification",
				"best option when downtime is acceptable",
			},
			RequiresStopped: true, Available: true,
		},
		{
			ID: ModeSnapshot, Label: "Snapshot-assisted", Consistency: ConsistencyLowRisk, SourceSafety: SourceProtected,
			Summary: "Uses a stable storage snapshot where supported.",
			Risks: []string{
				"source workload may remain running",
				"snapshot provides a stable storage point",
				"crash-consistent does not necessarily mean application-consistent",
				"guest freeze/quiescing may improve consistency where supported",
			},
			SourceMutation:    "May create a temporary snapshot named with prefix ndl-mig- on the source, only when you select this mode. Pre-existing snapshots are never deleted.",
			Available:         false,
			UnavailableReason: "V1 adapters do not create or export from source snapshots. Use Offline on a stopped workload, or Existing Backup.",
		},
		{
			ID: ModeLive, Label: "Live", Consistency: ConsistencyRisky, SourceSafety: SourceProtected,
			Summary: "Transfers while the workload remains active. RISKY. NO GUARANTEES.",
			Risks: []string{
				"source remains active",
				"data may change during transfer",
				"application consistency can be affected",
				"high-write workloads increase risk",
				"successful transfer does not guarantee a healthy destination",
			},
			RequiresAck:       true,
			Available:         false,
			UnavailableReason: "V1 does not perform live transfer. Live remains listed so the risk is visible. Use Offline or Existing Backup.",
		},
		{
			ID: ModeBackup, Label: "Existing Backup", Consistency: ConsistencySafe, SourceSafety: SourceProtected,
			Summary: "Imports a previously captured workload state.",
			Benefits: []string{
				"original running workload does not need to be accessed",
				"backup already represents a captured point in time",
			},
			Available: true,
		},
		{
			ID: ModeDisk, Label: "Disk / Archive Import", Consistency: ConsistencyDepends, SourceSafety: SourceProtected,
			Summary:   "Imports disks or archives you provide. Destination compatibility depends on the input.",
			Available: true,
		},
	}
}
